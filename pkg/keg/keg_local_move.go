package keg

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Move renames a node from src to dst and rewrites in-content links that
// target src (../N) across the keg. It returns the ids of nodes whose content
// was rewritten to follow the move.
func (k *LocalKeg) Move(ctx context.Context, opts NodeMoveOptions) ([]NodeId, error) {
	return withKegWriteValue(ctx, k, func(ctx context.Context) ([]NodeId, error) { return k.move(ctx, opts) })
}

func (k *LocalKeg) move(ctx context.Context, opts NodeMoveOptions) ([]NodeId, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to move node: %w", err)
	}

	src := NodeId{ID: opts.Source.ID, Code: opts.Source.Code}
	dst := NodeId{ID: opts.Destination.ID, Code: opts.Destination.Code}
	if !src.Valid() || !dst.Valid() {
		return nil, fmt.Errorf("invalid node id: %w", ErrInvalid)
	}
	if src.ID == 0 || dst.ID == 0 {
		return nil, fmt.Errorf("node 0 cannot be moved: %w", ErrInvalid)
	}
	current, err := k.ReadNode(ctx, src)
	if err != nil {
		return nil, err
	}
	if err := checkExpectedHash("node "+src.Path(), nodeHashSource, opts.ExpectedHash, current.Hash(), nodeRecoveryContent(current)); err != nil {
		return nil, err
	}
	if src.Equals(dst) {
		return nil, nil
	}

	// Use content-aware existence checks so shadow reservations created by
	// MemoryRepository.Next() / MemoryRepository.WithNodeLock() do not masquerade as real nodes.
	// These are pre-lock gates; the under-lock authoritative check runs
	// inside Repo.MoveNode.
	srcExists, err := k.nodeExistsWithContent(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("failed to check source node: %w", err)
	}
	if !srcExists {
		return nil, fmt.Errorf("source node %s not found: %w", src.Path(), ErrNotExist)
	}

	dstExists, err := k.nodeExistsWithContent(ctx, dst)
	if err != nil {
		return nil, fmt.Errorf("failed to check destination node: %w", err)
	}
	if dstExists {
		return nil, fmt.Errorf("destination node %s already exists: %w", dst.Path(), ErrDestinationExists)
	}
	dstTrashed, err := k.idTrashed(ctx, dst)
	if err != nil {
		return nil, fmt.Errorf("failed to check the trash for the destination node: %w", err)
	}
	if dstTrashed {
		return nil, fmt.Errorf("node %s is in the trash; restore or purge it before moving a node there: %w", dst.Path(), ErrDestinationExists)
	}

	if err := k.Repo.MoveNode(ctx, src, dst); err != nil {
		return nil, fmt.Errorf("failed to move node %s to %s: %w", src.Path(), dst.Path(), err)
	}

	changedNodes, errs := k.rewriteLinksNoDex(ctx, src, dst)

	// Single batched dex update: load fresh dex, apply all changes, write once.
	dex, err := k.ensureDexFresh(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to retrieve dex after move: %w", err))
	} else {
		if err := dex.Remove(ctx, src); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove stale dex entry for %s: %w", src.Path(), err))
		}
		movedData, err := k.getNode(ctx, dst)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to load moved node %s: %w", dst.Path(), err))
		} else if err := dex.Add(ctx, movedData); err != nil {
			errs = append(errs, fmt.Errorf("failed to add moved node %s to dex: %w", dst.Path(), err))
		}
		for _, nd := range changedNodes {
			// Remove stale entry first so Add replaces rather than merges
			// with outdated link/tag data.
			if err := dex.Remove(ctx, nd.ID); err != nil {
				errs = append(errs, fmt.Errorf("failed to remove stale dex entry for %s: %w", nd.ID.Path(), err))
			}
			if err := dex.Add(ctx, nd); err != nil {
				errs = append(errs, fmt.Errorf("failed to add changed node %s to dex: %w", nd.ID.Path(), err))
			}
		}
		if err := dex.Write(ctx, k.Repo); err != nil {
			errs = append(errs, fmt.Errorf("failed to write dex after move: %w", err))
		} else {
			k.dexMu.Lock()
			k.recordDexWrite()
			k.dexMu.Unlock()
		}
	}

	now := k.Runtime.Clock().Now()
	if err := k.touchSettingsUpdated(ctx, now); err != nil {
		errs = append(errs, fmt.Errorf("failed to update settings after move: %w", err))
	}
	if err := k.refreshSnapshotGeneratedIndexes(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to refresh snapshot indexes after move: %w", err))
	}

	rewritten := make([]NodeId, 0, len(changedNodes))
	for _, nd := range changedNodes {
		rewritten = append(rewritten, nd.ID)
	}
	return rewritten, errors.Join(errs...)
}

// Remove deletes a node from the repository and updates dex/settings artifacts.
// It returns the ids of nodes whose content was rewritten to drop links to the
// removed node.
func (k *LocalKeg) Remove(ctx context.Context, opts NodeRemoveOptions) ([]NodeId, error) {
	return withKegWriteValue(ctx, k, func(ctx context.Context) ([]NodeId, error) { return k.remove(ctx, opts) })
}

func (k *LocalKeg) remove(ctx context.Context, opts NodeRemoveOptions) ([]NodeId, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to remove node: %w", err)
	}

	id := NodeId{ID: opts.ID.ID, Code: opts.ID.Code}
	if !id.Valid() {
		return nil, fmt.Errorf("invalid node id: %w", ErrInvalid)
	}
	if id.ID == 0 {
		return nil, fmt.Errorf("node 0 cannot be removed: %w", ErrInvalid)
	}
	current, err := k.ReadNode(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := checkExpectedHash("node "+id.Path(), nodeHashSource, opts.ExpectedHash, current.Hash(), nodeRecoveryContent(current)); err != nil {
		return nil, err
	}
	return k.removeUnchecked(ctx, id, manualRemoval)
}

// removeUnchecked removes id without checking its hash. A repository with a
// trash keeps the node there and leaves links to it alone, so restoring it is
// lossless; otherwise the node is deleted and links to it point at node 0.
func (k *LocalKeg) removeUnchecked(ctx context.Context, id NodeId, info trashInfo) ([]NodeId, error) {
	if !id.Valid() {
		return nil, fmt.Errorf("invalid node id: %w", ErrInvalid)
	}
	if id.ID == 0 {
		return nil, fmt.Errorf("node 0 cannot be removed: %w", ErrInvalid)
	}

	// Check existence before acquiring the lock. WithNodeLock will also
	// return ErrNotExist for missing nodes, but this check provides a
	// clearer error message. Use the content-aware helper so shadow
	// reservations (bare directories from MemoryRepository.Next() / WithNodeLock)
	// are not mistaken for real nodes.
	exists, err := k.nodeExistsWithContent(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to check node existence: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("node %s not found: %w", id.Path(), ErrNotExist)
	}

	if trash, ok := repoTrash(k.Repo); ok {
		if err := k.trashNode(ctx, trash, id, info); err != nil {
			return nil, err
		}
		return nil, k.afterRemove(ctx, id, nil)
	}

	// Acquire node lock to prevent concurrent writes from resurrecting
	// the node directory after deletion. Re-check existence under lock.
	if err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		exists, err := k.nodeExistsWithContent(lockCtx, id)
		if err != nil {
			return fmt.Errorf("failed to check node existence: %w", err)
		}
		if !exists {
			return fmt.Errorf("node %s not found: %w", id.Path(), ErrNotExist)
		}
		return k.Repo.DeleteNode(lockCtx, id)
	}); err != nil {
		return nil, err
	}

	// Rewrite all links that pointed to the removed node so they point to
	// the zero node (../0) instead of dangling. These are cosmetic content
	// rewrites that don't need per-node dex updates -- the Remove dex update
	// handles the index cleanup via dex.Remove(id).
	var errs []error
	var rewritten []NodeId
	zeroID := NodeId{ID: 0}
	linkRE := compileNodeLinkPattern(id)
	nodeIDs, listErr := k.Repo.ListNodes(ctx)
	if listErr != nil {
		return nil, fmt.Errorf("failed to list nodes for link rewrite after remove: %w", listErr)
	}
	for _, otherID := range nodeIDs {
		if err := k.withNodeLock(ctx, otherID, func(lockCtx context.Context) error {
			raw, readErr := k.Repo.ReadContent(lockCtx, otherID)
			if errors.Is(readErr, ErrNotExist) {
				return nil
			}
			if readErr != nil {
				return readErr
			}
			updated, changed := rewriteNodeLinks(raw, linkRE, zeroID)
			if !changed {
				return nil
			}
			if err := k.Repo.WriteContent(lockCtx, otherID, updated); err != nil {
				return err
			}
			rewritten = append(rewritten, otherID)
			return nil
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to rewrite links in node %s: %w", otherID.Path(), err))
		}
	}

	return rewritten, k.afterRemove(ctx, id, errs)
}

// afterRemove drops a removed node from the dex and stamps the keg, adding
// any failure to errs.
func (k *LocalKeg) afterRemove(ctx context.Context, id NodeId, errs []error) error {
	// Single batched dex update: remove deleted node, write once.
	dex, err := k.ensureDexFresh(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to retrieve dex after remove: %w", err))
	} else {
		if err := dex.Remove(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove %s from dex: %w", id.Path(), err))
		}
		if err := dex.Write(ctx, k.Repo); err != nil {
			errs = append(errs, fmt.Errorf("failed to write dex after remove: %w", err))
		} else {
			k.dexMu.Lock()
			k.recordDexWrite()
			k.dexMu.Unlock()
		}
	}

	now := k.Runtime.Clock().Now()
	if err := k.touchSettingsUpdated(ctx, now); err != nil {
		errs = append(errs, fmt.Errorf("failed to update settings after remove: %w", err))
	}
	if err := k.refreshSnapshotGeneratedIndexes(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to refresh snapshot indexes after remove: %w", err))
	}
	return errors.Join(errs...)
}

// rewriteLinksNoDex points every ../from link in the keg at to, returning the
// nodes it rewrote for the caller's single dex update. Link rewrites belong to
// the operation that caused them rather than being live edits, so strict
// explicit-selection does not apply. Nodes in skip are neither read nor
// rewritten.
func (k *LocalKeg) rewriteLinksNoDex(ctx context.Context, from, to NodeId, skip ...NodeId) ([]*NodeData, []error) {
	return k.rewriteMatchingLinksNoDex(ctx, compileNodeLinkPattern(from), to, skip...)
}

// rewriteMatchingLinksNoDex points every link linkRE matches at to, in one
// scan of the keg.
func (k *LocalKeg) rewriteMatchingLinksNoDex(ctx context.Context, linkRE *regexp.Regexp, to NodeId, skip ...NodeId) ([]*NodeData, []error) {
	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return nil, []error{fmt.Errorf("failed to list nodes for link rewrite: %w", err)}
	}
	var errs []error
	var changedNodes []*NodeData
	for _, id := range ids {
		if slices.Contains(skip, id) {
			continue
		}
		raw, readErr := k.Repo.ReadContent(ctx, id)
		if readErr != nil {
			if errors.Is(readErr, ErrNotExist) {
				continue
			}
			errs = append(errs, fmt.Errorf("failed to read node content %s: %w", id.Path(), readErr))
			continue
		}
		updated, changed := rewriteNodeLinks(raw, linkRE, to)
		if !changed {
			continue
		}
		nd, err := k.setContentNoDex(WithValidationMode(ctx, ValidationModeOff), id, updated)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to rewrite links for node %s: %w", id.Path(), err))
			continue
		}
		if nd != nil {
			changedNodes = append(changedNodes, nd)
		}
	}
	return changedNodes, errs
}

// retargetLinks points every ../from link in the keg at to and brings the dex
// up to date in one write. It returns the ids of the rewritten nodes. Nodes
// in skip are left as they are.
func (k *LocalKeg) retargetLinks(ctx context.Context, from, to NodeId, skip ...NodeId) ([]NodeId, error) {
	return k.retargetLinksMany(ctx, []NodeId{from}, to, skip...)
}

// retargetLinksMany is retargetLinks for several ids at once: one scan of
// the keg and one dex write.
func (k *LocalKeg) retargetLinksMany(ctx context.Context, from []NodeId, to NodeId, skip ...NodeId) ([]NodeId, error) {
	changedNodes, errs := k.rewriteMatchingLinksNoDex(ctx, compileNodeLinksPattern(from), to, skip...)
	rewritten := make([]NodeId, 0, len(changedNodes))
	for _, nd := range changedNodes {
		rewritten = append(rewritten, nd.ID)
	}
	if len(changedNodes) == 0 {
		return rewritten, errors.Join(errs...)
	}
	dex, err := k.ensureDexFresh(ctx)
	if err != nil {
		return rewritten, errors.Join(append(errs, fmt.Errorf("failed to retrieve dex after link rewrite: %w", err))...)
	}
	for _, nd := range changedNodes {
		if err := dex.Remove(ctx, nd.ID); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove stale dex entry for %s: %w", nd.ID.Path(), err))
		}
		if err := dex.Add(ctx, nd); err != nil {
			errs = append(errs, fmt.Errorf("failed to add changed node %s to dex: %w", nd.ID.Path(), err))
		}
	}
	if err := dex.Write(ctx, k.Repo); err != nil {
		errs = append(errs, fmt.Errorf("failed to write dex after link rewrite: %w", err))
	} else {
		k.dexMu.Lock()
		k.recordDexWrite()
		k.dexMu.Unlock()
	}
	if err := k.touchSettingsUpdated(ctx, k.Runtime.Clock().Now()); err != nil {
		errs = append(errs, fmt.Errorf("failed to update settings after link rewrite: %w", err))
	}
	return rewritten, errors.Join(errs...)
}

// compileNodeLinkPattern builds a regexp that matches canonical relative node
// links "../N" for the given source node ID. Pre-compile once and pass to
// rewriteNodeLinks to avoid recompilation per call.
func compileNodeLinkPattern(src NodeId) *regexp.Regexp {
	return compileNodeLinksPattern([]NodeId{src})
}

// compileNodeLinksPattern matches canonical "../N" links to any of ids.
func compileNodeLinksPattern(ids []NodeId) *regexp.Regexp {
	alternatives := make([]string, len(ids))
	for i, id := range ids {
		alternatives[i] = regexp.QuoteMeta(id.Path())
	}
	delimiters := `[[:space:]\)\]\}\>\.,;:!?'\"#]`
	pattern := `\.\./\s*(?:` + strings.Join(alternatives, "|") + `)(` + delimiters + `|$)`
	return regexp.MustCompile(pattern)
}

// RetargetLinks returns content with every canonical "../from" node link
// pointed at to, as moving or merging a node rewrites the keg's links.
func RetargetLinks(content []byte, from, to NodeId) []byte {
	updated, _ := rewriteNodeLinks(content, compileNodeLinkPattern(from), to)
	return updated
}

func rewriteNodeLinks(raw []byte, re *regexp.Regexp, dst NodeId) ([]byte, bool) {
	newID := dst.Path()
	if newID == "" || len(raw) == 0 {
		return raw, false
	}

	original := string(raw)
	rewritten := re.ReplaceAllString(original, "../"+newID+`$1`)
	if rewritten == original {
		return raw, false
	}
	return []byte(rewritten), true
}
