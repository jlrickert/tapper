package keg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Why a node went to the trash.
const (
	// TrashReasonManual is a node someone removed.
	TrashReasonManual = "manual"
	// TrashReasonMerge is a node merged into another (TrashEntry.MergedInto).
	TrashReasonMerge = "merge"
	// TrashReasonArchiveRestore is a node an archive restore removed because
	// the archive did not carry it.
	TrashReasonArchiveRestore = "archive-restore"
)

// MaxTrashBundleSize caps a trash bundle, a node with its history and files,
// unless the trash sets its own limit (RepositoryTrashLimit). Trashing a node
// whose bundle would pass the cap is refused with ErrTrashBundleTooLarge
// rather than failing opaquely in the repository.
const MaxTrashBundleSize = 256 << 20

// RepositoryTrashLimit is an optional RepositoryTrash capability: a trash
// that holds bundles up to its own limit, in bytes, in place of
// MaxTrashBundleSize. A limit of zero or less keeps the default.
type RepositoryTrashLimit interface {
	// TrashBundleLimit returns the largest bundle, in bytes, the trash holds.
	TrashBundleLimit() int
}

func trashBundleLimit(trash RepositoryTrash) int {
	if limited, ok := trash.(RepositoryTrashLimit); ok && limited.TrashBundleLimit() > 0 {
		return limited.TrashBundleLimit()
	}
	return MaxTrashBundleSize
}

// formatTrashLimit names a bundle limit for a person.
func formatTrashLimit(limit int) string {
	if limit >= 1<<20 && limit%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", limit>>20)
	}
	return fmt.Sprintf("%d bytes", limit)
}

// ErrTrashBundleTooLarge refuses to trash a node too large for the trash. It
// wraps ErrInvalid.
var ErrTrashBundleTooLarge = fmt.Errorf("too large for the trash: %w", ErrInvalid)

// cappedBuffer is a bytes.Buffer that refuses to grow past max, so building
// an oversized bundle stops at the cap instead of holding all of it.
type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, ErrTrashBundleTooLarge
	}
	return b.Buffer.Write(p)
}

// TrashEntry describes a removed node a repository keeps for restoring.
type TrashEntry struct {
	ID     NodeId `json:"id"`
	Title  string `json:"title"`
	Reason string `json:"reason"`
	// MergedInto is the node a merge folded this one into.
	MergedInto *NodeId   `json:"merged_into,omitempty"`
	TrashedAt  time.Time `json:"trashed_at"`
	// ExpiresAt is when the repository will purge the entry, nil for never.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// Size is the stored bundle's size in bytes.
	Size int64 `json:"size"`
	// DisplacedFrom is the id the entry was trashed at, when an archive
	// restore needed that id and the entry moved to ID to make way. Its
	// bundle still names the old id; restoring puts it back at ID.
	DisplacedFrom *NodeId `json:"displaced_from,omitempty"`
}

// TrashWrite is what a repository stores for a node it trashes. Bundle is a
// keg archive of the node with its history and assets, which restoring
// imports back at the node's id.
type TrashWrite struct {
	Title      string
	Reason     string
	MergedInto *NodeId
	Bundle     []byte
}

// RepositoryTrash is an optional repository capability: removed nodes go to
// a trash they can be restored from rather than being deleted outright.
type RepositoryTrash interface {
	// TrashNode stores in and deletes the node, as one write. It refuses
	// with ErrDestinationExists when id already has a trash entry, and never
	// overwrites one.
	TrashNode(ctx context.Context, id NodeId, in TrashWrite) error
	// ListTrash returns the trashed nodes, newest first.
	ListTrash(ctx context.Context) ([]TrashEntry, error)
	// ReadTrash returns one entry and its bundle; ErrNotExist when absent.
	ReadTrash(ctx context.Context, id NodeId) (TrashEntry, []byte, error)
	// TrashInfo returns one entry without its bundle; ErrNotExist when
	// absent. It is the cheap check for whether an id is trashed.
	TrashInfo(ctx context.Context, id NodeId) (TrashEntry, error)
	// MoveTrash re-keys the entry at from to the free id to, recording from
	// as its DisplacedFrom. It refuses with ErrDestinationExists when to
	// already has an entry, and ErrNotExist when from has none.
	MoveTrash(ctx context.Context, from, to NodeId) error
	// DeleteTrash drops an entry; absent entries are not an error.
	DeleteTrash(ctx context.Context, id NodeId) error
}

func repoTrash(repo Repository) (RepositoryTrash, bool) {
	trash, ok := repo.(RepositoryTrash)
	return trash, ok
}

// idTrashed reports whether id has an entry in the keg's trash. A trashed
// node's id is reserved: nothing but a restore may put a node there.
func (k *LocalKeg) idTrashed(ctx context.Context, id NodeId) (bool, error) {
	trash, ok := repoTrash(k.Repo)
	if !ok {
		return false, nil
	}
	_, err := trash.TrashInfo(ctx, id)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, ErrNotExist):
		return false, nil
	default:
		return false, err
	}
}

// displaceTrashed makes way for an import landing on id: a trash entry
// there moves to a fresh id from the repository's counter, never one in
// avoid (the ids the import lands on), so the import can take id and the
// trashed node stays restorable at its new one. It reports the new id, or
// ok false when id held no entry.
func (k *LocalKeg) displaceTrashed(ctx context.Context, id NodeId, avoid map[NodeId]bool) (NodeId, bool, error) {
	trash, ok := repoTrash(k.Repo)
	if !ok {
		return NodeId{}, false, nil
	}
	isTrashed, err := k.idTrashed(ctx, id)
	if err != nil || !isTrashed {
		return NodeId{}, false, err
	}
	var to NodeId
	for {
		next, err := k.Repo.Next(ctx)
		if err != nil {
			return NodeId{}, false, fmt.Errorf("unable to allocate an id for trashed node %s: %w", id.Path(), err)
		}
		// Next reserves the id as an empty node; the trash entry takes it,
		// not a node.
		k.cleanupUnusedImportReservations(ctx, []NodeId{next})
		// The import raised the counter past its own ids first; this is the
		// second guard against handing the entry one of them.
		if !avoid[next] {
			to = next
			break
		}
	}
	if err := trash.MoveTrash(ctx, id, to); err != nil {
		return NodeId{}, false, fmt.Errorf("unable to move trashed node %s out of the way: %w", id.Path(), err)
	}
	return to, true, nil
}

// trashInfo says why removeUnchecked is removing a node.
type trashInfo struct {
	reason     string
	mergedInto *NodeId
}

var manualRemoval = trashInfo{reason: TrashReasonManual}

// trashNode bundles id and hands it to the repository's trash, which deletes
// the live node. Inbound links are left pointing at the trashed id so a
// restore is lossless; purging rewrites them. A node whose bundle would pass
// MaxTrashBundleSize is refused with ErrTrashBundleTooLarge and left alone.
func (k *LocalKeg) trashNode(ctx context.Context, trash RepositoryTrash, id NodeId, info trashInfo) error {
	view, err := k.ReadNode(ctx, id)
	if err != nil {
		return err
	}
	snapshots, withHistory := repoSnapshots(k.Repo)
	opts := ExportNodesOptions{NodeIDs: []NodeId{id}, WithHistory: withHistory, HistoryIfSupported: true, WithAssets: true}
	limit := trashBundleLimit(trash)
	bundle := cappedBuffer{max: limit}
	if err := k.writeArchive(ctx, &bundle, []NodeId{id}, snapshots, opts); err != nil {
		if errors.Is(err, ErrTrashBundleTooLarge) {
			return fmt.Errorf("node %s with its history and files passes the trash's %s limit, so it cannot go to the trash; remove large files or snapshots first: %w",
				id.Path(), formatTrashLimit(limit), ErrTrashBundleTooLarge)
		}
		return fmt.Errorf("failed to bundle node %s for the trash: %w", id.Path(), err)
	}
	return k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		return trash.TrashNode(lockCtx, id, TrashWrite{
			Title:      ExplicitMarkdownTitle(view.Content),
			Reason:     info.reason,
			MergedInto: info.mergedInto,
			Bundle:     bundle.Bytes(),
		})
	})
}

// TrashNodesResult says what TrashNodes did.
type TrashNodesResult struct {
	// Trashed is the nodes now in the trash.
	Trashed []NodeId
	// TooLarge is the nodes left in place because their bundle would pass
	// MaxTrashBundleSize.
	TooLarge []NodeId
}

// TrashNodes moves nodes to the trash for reason, one at a time, leaving
// links to them alone, as a removal does, and updates the dex once at the
// end. Ids with no live node are skipped, node 0 is refused, and a node too
// large for the trash stays where it is and is reported in TooLarge. It is
// for bulk removals with a reason of their own, such as an archive restore
// dropping what the archive did not carry; a merge goes through MergeNodes.
// ErrNotSupported when the repository keeps no trash.
func (k *LocalKeg) TrashNodes(ctx context.Context, ids []NodeId, reason string) (TrashNodesResult, error) {
	return withKegWriteValue(ctx, k, func(ctx context.Context) (TrashNodesResult, error) {
		return k.trashNodes(ctx, ids, reason)
	})
}

func (k *LocalKeg) trashNodes(ctx context.Context, ids []NodeId, reason string) (TrashNodesResult, error) {
	trash, ok := repoTrash(k.Repo)
	if !ok {
		return TrashNodesResult{}, ErrNotSupported
	}
	if reason == "" || reason == TrashReasonMerge {
		return TrashNodesResult{}, fmt.Errorf("trash reason %q: %w", reason, ErrInvalid)
	}
	result := TrashNodesResult{Trashed: []NodeId{}, TooLarge: []NodeId{}}
	var errs []error
	for _, id := range ids {
		if id.ID == 0 {
			return result, fmt.Errorf("node 0 cannot be removed: %w", ErrInvalid)
		}
		live, err := k.nodeExistsWithContent(ctx, id)
		if err != nil {
			errs = append(errs, err)
			break
		}
		if !live {
			continue
		}
		err = k.trashNode(ctx, trash, id, trashInfo{reason: reason})
		if errors.Is(err, ErrTrashBundleTooLarge) {
			result.TooLarge = append(result.TooLarge, id)
			continue
		}
		if err != nil {
			errs = append(errs, err)
			break
		}
		result.Trashed = append(result.Trashed, id)
	}
	if len(result.Trashed) > 0 {
		errs = append(errs, k.afterRemoveMany(ctx, result.Trashed))
	}
	return result, errors.Join(errs...)
}

// afterRemoveMany drops removed nodes from the dex in one write and stamps
// the keg.
func (k *LocalKeg) afterRemoveMany(ctx context.Context, ids []NodeId) error {
	var errs []error
	dex, err := k.ensureDexFresh(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to retrieve dex after remove: %w", err))
	} else {
		for _, id := range ids {
			if err := dex.Remove(ctx, id); err != nil {
				errs = append(errs, fmt.Errorf("failed to remove %s from dex: %w", id.Path(), err))
			}
		}
		if err := dex.Write(ctx, k.Repo); err != nil {
			errs = append(errs, fmt.Errorf("failed to write dex after remove: %w", err))
		} else {
			k.dexMu.Lock()
			k.recordDexWrite()
			k.dexMu.Unlock()
		}
	}
	if err := k.touchSettingsUpdated(ctx, k.Runtime.Clock().Now()); err != nil {
		errs = append(errs, fmt.Errorf("failed to update settings after remove: %w", err))
	}
	return errors.Join(errs...)
}

// ListTrash returns the keg's trashed nodes, newest first, or ErrNotSupported
// when its repository keeps no trash.
func (k *LocalKeg) ListTrash(ctx context.Context) ([]TrashEntry, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) ([]TrashEntry, error) {
		trash, ok := repoTrash(k.Repo)
		if !ok {
			return nil, ErrNotSupported
		}
		return trash.ListTrash(ctx)
	})
}

// TrashInfo returns id's trash entry without its bundle: ErrNotExist when id
// is not trashed, ErrNotSupported when the repository keeps no trash.
func (k *LocalKeg) TrashInfo(ctx context.Context, id NodeId) (TrashEntry, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) (TrashEntry, error) {
		trash, ok := repoTrash(k.Repo)
		if !ok {
			return TrashEntry{}, ErrNotSupported
		}
		return trash.TrashInfo(ctx, id)
	})
}

// RestoreTrashed puts trashed nodes back at their own ids, with the history
// and assets they had. Links to them were left in place when they were
// trashed, so they resolve again. All or nothing.
func (k *LocalKeg) RestoreTrashed(ctx context.Context, ids []NodeId) error {
	_, err := withKegAtomicWriteValue(ctx, k, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, k.restoreTrashed(ctx, ids)
	})
	return err
}

func (k *LocalKeg) restoreTrashed(ctx context.Context, ids []NodeId) error {
	trash, ok := repoTrash(k.Repo)
	if !ok {
		return ErrNotSupported
	}
	if err := validateMutationBatchSize(len(ids)); err != nil {
		return err
	}
	// The node is put back as it was; the keg's schemas may have moved on
	// since, and a restore must not be refused for it.
	ctx = WithValidationMode(ctx, ValidationModeOff)
	seen := make(map[NodeId]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return fmt.Errorf("node %s is listed twice: %w", id.Path(), ErrInvalid)
		}
		seen[id] = true
	}
	for _, id := range ids {
		_, bundle, err := trash.ReadTrash(ctx, id)
		if err != nil {
			return fmt.Errorf("trashed node %s: %w", id.Path(), err)
		}
		exists, err := k.nodeExistsWithContent(ctx, id)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("node %s exists, so its trashed copy cannot be restored over it: %w", id.Path(), ErrDestinationExists)
		}
		// The bundle names the id the node was trashed at, which an archive
		// restore may since have given to another node; the node comes back
		// at the entry's current id, its links to itself with it. Other
		// nodes' links to the old id belong to whatever holds it. The import
		// leaves the dex alone: it is updated once, below, for the batch.
		to := id
		opts := ImportNodesOptions{HistoryIfSupported: true, restoreFromTrash: true, restoreTo: &to}
		if _, err := k.importNodes(ctx, bytes.NewReader(bundle), opts); err != nil {
			return fmt.Errorf("failed to restore node %s: %w", id.Path(), err)
		}
		if err := trash.DeleteTrash(ctx, id); err != nil {
			return err
		}
	}
	return k.indexRestored(ctx, ids)
}

// indexRestored adds restored nodes to the dex without rebuilding it from
// every node: each restored node is added, and the links other nodes hold to
// it, which the removal dropped from the link indexes, are found in one
// batched read of the keg's stats. A repository without batched reads gets
// one full rebuild for the whole restore.
func (k *LocalKeg) indexRestored(ctx context.Context, ids []NodeId) error {
	now := k.Runtime.Clock().Now()
	batch, ok := repositoryBatchRead(k.Repo)
	if !ok {
		if err := k.rebuildDexFromRepo(ctx); err != nil {
			return err
		}
		return k.touchSettingsUpdated(ctx, now)
	}
	restored := make(map[string]bool, len(ids))
	nodes := make([]*NodeData, 0, len(ids))
	for _, id := range ids {
		data, err := k.loadNodeDataForDex(ctx, id)
		if err != nil {
			return fmt.Errorf("unable to read restored node %s for the dex: %w", id.Path(), err)
		}
		restored[id.Path()] = true
		nodes = append(nodes, data)
	}
	all, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return fmt.Errorf("unable to list nodes after restore: %w", err)
	}
	stats, err := batch.ReadStatsBatch(ctx, all)
	if err != nil {
		return fmt.Errorf("unable to read node links after restore: %w", err)
	}
	type inbound struct{ src, dst NodeId }
	var links []inbound
	for _, src := range all {
		if restored[src.Path()] {
			continue
		}
		for _, dst := range stats[src.Path()].Links() {
			if restored[dst.Path()] {
				links = append(links, inbound{src: src, dst: dst})
			}
		}
	}
	dex, err := k.ensureDexFresh(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve dex after restore: %w", err)
	}
	for _, data := range nodes {
		if err := dex.Add(ctx, data); err != nil {
			return fmt.Errorf("failed to add restored node %s to dex: %w", data.ID.Path(), err)
		}
	}
	dex.mu.Lock()
	for _, link := range links {
		addDexLink(dex, link.src, link.dst)
	}
	dex.mu.Unlock()
	if err := dex.Write(ctx, k.Repo); err != nil {
		return fmt.Errorf("failed to write dex after restore: %w", err)
	}
	k.dexMu.Lock()
	k.recordDexWrite()
	k.dexMu.Unlock()
	return k.touchSettingsUpdated(ctx, now)
}

// addDexLink records src linking to dst in the dex's link and backlink
// indexes. The caller holds dex.mu.
func addDexLink(dex *Dex, src, dst NodeId) {
	if dex.links.data == nil {
		dex.links.data = map[string][]NodeId{}
	}
	if !slices.ContainsFunc(dex.links.data[src.Path()], dst.Equals) {
		dex.links.data[src.Path()] = append(dex.links.data[src.Path()], dst)
	}
	if dex.backlinks.data == nil {
		dex.backlinks.data = map[string][]NodeId{}
	}
	if !slices.ContainsFunc(dex.backlinks.data[dst.Path()], src.Equals) {
		dex.backlinks.data[dst.Path()] = append(dex.backlinks.data[dst.Path()], src)
	}
}

// mapPurgedLinks points a trash bundle's links to nodes that are gone for
// good -- purged since the node was trashed, neither live nor in the trash --
// at node 0, as purging did to the links live nodes held. Links to a live
// node or to another trashed node are left alone.
func (k *LocalKeg) mapPurgedLinks(ctx context.Context, entries map[string][]byte, ordered []string, mapping map[string]NodeId) error {
	for _, sourceID := range ordered {
		base := "keg-archive/nodes/" + sourceID
		var targets []NodeId
		if raw, ok := entries[base+"/README.md"]; ok {
			if content, err := ParseContent(k.Runtime, raw, MarkdownContentFilename); err == nil {
				targets = append(targets, content.Links...)
			}
		}
		if raw, ok := entries[base+"/stats.json"]; ok {
			if stats, err := ParseStats(ctx, raw); err == nil {
				targets = append(targets, stats.Links()...)
			}
		}
		for _, target := range targets {
			if _, mapped := mapping[target.Path()]; mapped || target.ID == 0 || target.Code != "" || target.Alias != "" {
				continue
			}
			live, err := k.nodeExistsWithContent(ctx, target)
			if err != nil {
				return err
			}
			trashed, err := k.idTrashed(ctx, target)
			if err != nil {
				return err
			}
			if !live && !trashed {
				mapping[target.Path()] = NodeId{ID: 0}
			}
		}
	}
	return nil
}

// PurgeTrashOptions names the trashed nodes to delete for good.
type PurgeTrashOptions struct {
	IDs []NodeId `json:"nodes"`
	// ExpiredAt, when set, purges only entries whose ExpiresAt is set and
	// not after it. The expiry is checked inside the write, so an entry
	// restored and trashed again since it was listed is left alone.
	ExpiredAt *time.Time `json:"-"`
}

// PurgeTrashResult says what a purge did.
type PurgeTrashResult struct {
	// Purged is the ids whose trash entries were deleted. Ids with no entry,
	// or not yet expired under ExpiredAt, are absent.
	Purged []NodeId `json:"purged"`
	// Rewritten is the nodes whose links to a purged id now point at node 0.
	Rewritten []NodeId `json:"rewritten"`
}

// PurgeTrashed deletes 1-100 trashed nodes for good, as one write, and
// points links that still name them at the zero node, as removing a node
// without a trash does. Links are left alone for an id a live node holds.
func (k *LocalKeg) PurgeTrashed(ctx context.Context, opts PurgeTrashOptions) (PurgeTrashResult, error) {
	return withKegAtomicWriteValue(ctx, k, func(ctx context.Context) (PurgeTrashResult, error) { return k.purgeTrashed(ctx, opts) })
}

func (k *LocalKeg) purgeTrashed(ctx context.Context, opts PurgeTrashOptions) (PurgeTrashResult, error) {
	trash, ok := repoTrash(k.Repo)
	if !ok {
		return PurgeTrashResult{}, ErrNotSupported
	}
	if err := validateMutationBatchSize(len(opts.IDs)); err != nil {
		return PurgeTrashResult{}, err
	}
	seen := make(map[NodeId]bool, len(opts.IDs))
	for _, id := range opts.IDs {
		if seen[id] {
			return PurgeTrashResult{}, fmt.Errorf("node %s is listed twice: %w", id.Path(), ErrInvalid)
		}
		seen[id] = true
	}
	result := PurgeTrashResult{Purged: []NodeId{}, Rewritten: []NodeId{}}
	var orphaned []NodeId
	for _, id := range opts.IDs {
		entry, err := trash.TrashInfo(ctx, id)
		if errors.Is(err, ErrNotExist) {
			continue
		}
		if err != nil {
			return PurgeTrashResult{}, err
		}
		if opts.ExpiredAt != nil && (entry.ExpiresAt == nil || entry.ExpiresAt.After(*opts.ExpiredAt)) {
			continue
		}
		if err := trash.DeleteTrash(ctx, id); err != nil {
			return PurgeTrashResult{}, err
		}
		result.Purged = append(result.Purged, id)
		// Links to an id a live node holds are that node's links.
		live, err := k.nodeExistsWithContent(ctx, id)
		if err != nil {
			return PurgeTrashResult{}, err
		}
		if !live {
			orphaned = append(orphaned, id)
		}
	}
	if len(orphaned) == 0 {
		return result, nil
	}
	rewritten, err := k.retargetLinksMany(ctx, orphaned, NodeId{ID: 0})
	if err != nil {
		return PurgeTrashResult{}, err
	}
	result.Rewritten = append(result.Rewritten, rewritten...)
	return result, nil
}
