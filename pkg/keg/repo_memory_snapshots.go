package keg

import (
	"context"
	"fmt"
)

func (r *MemoryRepo) AppendSnapshot(ctx context.Context, id NodeId, in SnapshotWrite) (Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.appendSnapshotLocked(ctx, id, in)
}

func (r *MemoryRepo) appendSnapshotLocked(ctx context.Context, id NodeId, in SnapshotWrite) (Snapshot, error) {
	if _, ok := r.nodes[id]; !ok {
		return Snapshot{}, ErrNotExist
	}

	entries := r.snapshots[id]
	var parent RevisionID
	if len(entries) > 0 {
		parent = entries[len(entries)-1].snapshot.ID
	}
	if in.ExpectedParent != parent {
		return Snapshot{}, fmt.Errorf("expected parent %d, got %d: %w", in.ExpectedParent, parent, ErrConflict)
	}

	content, meta, statsBytes, err := normalizeSnapshotWrite(ctx, r.runtime, in)
	if err != nil {
		return Snapshot{}, err
	}
	contentHash, metaHash, statsHash := snapshotWriteHashes(r.runtime, content, meta, statsBytes)

	snapshot := Snapshot{
		ID:           parent + 1,
		Node:         id,
		Parent:       parent,
		CreatedAt:    r.runtime.Clock().Now(),
		Message:      in.Message,
		ContentHash:  contentHash,
		MetaHash:     metaHash,
		StatsHash:    statsHash,
		IsCheckpoint: true,
	}
	r.snapshots[id] = append(entries, memorySnapshotEntry{
		snapshot: snapshot,
		content:  content,
		meta:     meta,
		stats:    statsBytes,
	})
	return snapshot, nil
}

func (r *MemoryRepo) GetSnapshot(ctx context.Context, id NodeId, rev RevisionID, opts SnapshotReadOptions) (Snapshot, []byte, []byte, *NodeStats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, err := r.snapshotEntryLocked(id, rev)
	if err != nil {
		return Snapshot{}, nil, nil, nil, err
	}
	snap := entry.snapshot

	var content []byte
	if opts.ResolveContent {
		content = cloneBytes(entry.content)
	}
	meta := cloneBytes(entry.meta)
	stats, err := snapshotStatsFromBytes(ctx, entry.stats)
	if err != nil {
		return Snapshot{}, nil, nil, nil, err
	}
	return snap, content, meta, stats, nil
}

func (r *MemoryRepo) ListSnapshots(ctx context.Context, id NodeId) ([]Snapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.nodes[id]; !ok {
		return nil, ErrNotExist
	}

	entries := r.snapshots[id]
	out := make([]Snapshot, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.snapshot)
	}
	return out, nil
}

func (r *MemoryRepo) ReadContentAt(ctx context.Context, id NodeId, rev RevisionID) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, err := r.snapshotEntryLocked(id, rev)
	if err != nil {
		return nil, err
	}
	return cloneBytes(entry.content), nil
}

func (r *MemoryRepo) RestoreSnapshot(ctx context.Context, id NodeId, rev RevisionID, createRestoreSnapshot bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entries := r.snapshots[id]
	var parent RevisionID
	if len(entries) > 0 {
		parent = entries[len(entries)-1].snapshot.ID
	}

	entry, err := r.snapshotEntryLocked(id, rev)
	if err != nil {
		return err
	}
	node, ok := r.nodes[id]
	if !ok {
		return ErrNotExist
	}

	node.content = cloneBytes(entry.content)
	node.meta = cloneBytes(entry.meta)
	node.stats = cloneBytes(entry.stats)

	if !createRestoreSnapshot {
		return nil
	}

	stats, err := snapshotStatsFromBytes(ctx, entry.stats)
	if err != nil {
		return err
	}
	_, err = r.appendSnapshotLocked(ctx, id, SnapshotWrite{
		ExpectedParent: parent,
		Message:        fmt.Sprintf("restore from rev %d", rev),
		Meta:           cloneBytes(entry.meta),
		Stats:          stats,
		Content: SnapshotContentWrite{
			Kind: SnapshotContentKindFull,
			Data: cloneBytes(entry.content),
			Hash: entry.snapshot.ContentHash,
		},
	})
	return err
}

func (r *MemoryRepo) snapshotEntryLocked(id NodeId, rev RevisionID) (memorySnapshotEntry, error) {
	if _, ok := r.nodes[id]; !ok {
		return memorySnapshotEntry{}, ErrNotExist
	}
	for _, entry := range r.snapshots[id] {
		if entry.snapshot.ID == rev {
			return entry, nil
		}
	}
	return memorySnapshotEntry{}, ErrNotExist
}

var _ RepositorySnapshots = (*MemoryRepo)(nil)
