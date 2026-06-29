package keg

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

// Move renames a node from src to dst and rewrites in-content links that
// target src (../N) across the keg. It returns the ids of nodes whose content
// was rewritten to follow the move.
func (k *LocalKeg) Move(ctx context.Context, src NodeId, dst NodeId) ([]NodeId, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to move node: %w", err)
	}

	src = NodeId{ID: src.ID, Code: src.Code}
	dst = NodeId{ID: dst.ID, Code: dst.Code}
	if !src.Valid() || !dst.Valid() {
		return nil, fmt.Errorf("invalid node id: %w", ErrInvalid)
	}
	if src.ID == 0 || dst.ID == 0 {
		return nil, fmt.Errorf("node 0 cannot be moved: %w", ErrInvalid)
	}
	if src.Equals(dst) {
		return nil, nil
	}

	// Use content-aware existence checks so shadow reservations created by
	// FsRepo.Next() / FsRepo.WithNodeLock() do not masquerade as real nodes.
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

	if err := k.Repo.MoveNode(ctx, src, dst); err != nil {
		return nil, fmt.Errorf("failed to move node %s to %s: %w", src.Path(), dst.Path(), err)
	}

	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes for link rewrite: %w", err)
	}

	// Rewrite links in all nodes, collecting changed NodeData without
	// writing the dex after each individual change. A single batched dex
	// write follows the loop.
	var errs []error
	var changedNodes []*NodeData
	linkRE := compileNodeLinkPattern(src)
	for _, id := range ids {
		raw, readErr := k.Repo.ReadContent(ctx, id)
		if readErr != nil {
			if errors.Is(readErr, ErrNotExist) {
				continue
			}
			errs = append(errs, fmt.Errorf("failed to read node content %s: %w", id.Path(), readErr))
			continue
		}

		updated, changed := rewriteNodeLinks(raw, linkRE, dst)
		if !changed {
			continue
		}
		nd, err := k.setContentNoDex(ctx, id, updated)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to rewrite links for node %s: %w", id.Path(), err))
			continue
		}
		if nd != nil {
			changedNodes = append(changedNodes, nd)
		}
	}

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
	if err := k.touchConfigUpdated(ctx, now); err != nil {
		errs = append(errs, fmt.Errorf("failed to update config after move: %w", err))
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

// Remove deletes a node from the repository and updates dex/config artifacts.
// It returns the ids of nodes whose content was rewritten to drop links to the
// removed node.
func (k *LocalKeg) Remove(ctx context.Context, id NodeId) ([]NodeId, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to remove node: %w", err)
	}

	id = NodeId{ID: id.ID, Code: id.Code}
	if !id.Valid() {
		return nil, fmt.Errorf("invalid node id: %w", ErrInvalid)
	}
	if id.ID == 0 {
		return nil, fmt.Errorf("node 0 cannot be removed: %w", ErrInvalid)
	}

	// Check existence before acquiring the lock. WithNodeLock will also
	// return ErrNotExist for missing nodes, but this check provides a
	// clearer error message. Use the content-aware helper so shadow
	// reservations (bare directories from FsRepo.Next() / WithNodeLock)
	// are not mistaken for real nodes.
	exists, err := k.nodeExistsWithContent(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to check node existence: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("node %s not found: %w", id.Path(), ErrNotExist)
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
		raw, readErr := k.Repo.ReadContent(ctx, otherID)
		if readErr != nil {
			if !errors.Is(readErr, ErrNotExist) {
				errs = append(errs, fmt.Errorf("failed to read node %s for link rewrite: %w", otherID.Path(), readErr))
			}
			continue
		}
		updated, changed := rewriteNodeLinks(raw, linkRE, zeroID)
		if changed {
			if err := k.withNodeLock(ctx, otherID, func(lockCtx context.Context) error {
				exists, exErr := k.nodeExistsWithContent(lockCtx, otherID)
				if exErr != nil || !exists {
					return nil // node was concurrently removed, skip rewrite
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
	}

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
	if err := k.touchConfigUpdated(ctx, now); err != nil {
		errs = append(errs, fmt.Errorf("failed to update config after remove: %w", err))
	}
	if err := k.refreshSnapshotGeneratedIndexes(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to refresh snapshot indexes after remove: %w", err))
	}

	return rewritten, errors.Join(errs...)
}

// compileNodeLinkPattern builds a regexp that matches canonical relative node
// links "../N" for the given source node ID. Pre-compile once and pass to
// rewriteNodeLinks to avoid recompilation per call.
func compileNodeLinkPattern(src NodeId) *regexp.Regexp {
	oldID := src.Path()
	delimiters := `[[:space:]\)\]\}\>\.,;:!?'\"#]`
	pattern := `\.\./\s*` + regexp.QuoteMeta(oldID) + `(` + delimiters + `|$)`
	return regexp.MustCompile(pattern)
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
