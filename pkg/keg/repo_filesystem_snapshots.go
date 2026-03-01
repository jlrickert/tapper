package keg

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const defaultSnapshotCheckpointInterval = 20

func (f *FsRepo) snapshotCheckpointInterval() int {
	if f == nil || f.SnapshotCheckpointInterval <= 0 {
		return defaultSnapshotCheckpointInterval
	}
	return f.SnapshotCheckpointInterval
}

func (f *FsRepo) AppendSnapshot(ctx context.Context, id NodeId, in SnapshotWrite) (Snapshot, error) {
	if contextHasNodeLock(ctx, id) {
		return f.appendSnapshotLocked(ctx, id, in)
	}

	var out Snapshot
	err := f.WithNodeLock(ctx, id, func(lockCtx context.Context) error {
		snap, err := f.appendSnapshotLocked(lockCtx, id, in)
		if err != nil {
			return err
		}
		out = snap
		return nil
	})
	return out, err
}

func (f *FsRepo) appendSnapshotLocked(ctx context.Context, id NodeId, in SnapshotWrite) (Snapshot, error) {
	exists, err := f.HasNode(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	if !exists {
		return Snapshot{}, ErrNotExist
	}

	index, err := f.readSnapshotIndex(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}

	var parent RevisionID
	if len(index) > 0 {
		parent = index[len(index)-1].ID
	}
	if in.ExpectedParent != parent {
		return Snapshot{}, fmt.Errorf("expected parent %d, got %d: %w", in.ExpectedParent, parent, ErrConflict)
	}

	content, meta, statsBytes, err := normalizeSnapshotWrite(ctx, f.runtime, in)
	if err != nil {
		return Snapshot{}, err
	}
	contentHash, metaHash, statsHash := snapshotWriteHashes(f.runtime, content, meta, statsBytes)
	createdAt := in.CreatedAt
	if createdAt.IsZero() {
		createdAt = f.runtime.Clock().Now()
	}

	storeFull := len(index) == 0 || in.Content.Kind == SnapshotContentKindFull
	if !storeFull && f.snapshotCheckpointInterval() > 0 {
		patches := 0
		for i := len(index) - 1; i >= 0; i-- {
			if index[i].IsCheckpoint {
				break
			}
			patches++
		}
		if patches >= f.snapshotCheckpointInterval() {
			storeFull = true
		}
	}

	snapshot := Snapshot{
		ID:           parent + 1,
		Node:         id,
		Parent:       parent,
		CreatedAt:    createdAt,
		Message:      in.Message,
		ContentHash:  contentHash,
		MetaHash:     metaHash,
		StatsHash:    statsHash,
		IsCheckpoint: storeFull,
	}

	if err := f.runtime.Mkdir(f.snapshotDir(id), 0o755, true); err != nil {
		return Snapshot{}, NewBackendError(f.Name(), "AppendSnapshotMkdir", 0, err, false)
	}

	if storeFull {
		if err := f.runtime.AtomicWriteFile(f.snapshotContentPath(id, snapshot.ID, SnapshotContentKindFull), content, 0o644); err != nil {
			return Snapshot{}, NewBackendError(f.Name(), "AppendSnapshotWriteContent", 0, err, false)
		}
	} else {
		baseContent, err := f.readContentAtIndex(ctx, id, index, parent)
		if err != nil {
			return Snapshot{}, err
		}
		patchBytes, err := buildSnapshotPatch(f.runtime.Hasher(), baseContent, content)
		if err != nil {
			return Snapshot{}, err
		}
		if err := f.runtime.AtomicWriteFile(f.snapshotContentPath(id, snapshot.ID, SnapshotContentKindPatch), patchBytes, 0o644); err != nil {
			return Snapshot{}, NewBackendError(f.Name(), "AppendSnapshotWritePatch", 0, err, false)
		}
	}

	if err := f.runtime.AtomicWriteFile(f.snapshotMetaPath(id, snapshot.ID), meta, 0o644); err != nil {
		return Snapshot{}, NewBackendError(f.Name(), "AppendSnapshotWriteMeta", 0, err, false)
	}
	if err := f.runtime.AtomicWriteFile(f.snapshotStatsPath(id, snapshot.ID), statsBytes, 0o644); err != nil {
		return Snapshot{}, NewBackendError(f.Name(), "AppendSnapshotWriteStats", 0, err, false)
	}

	index = append(index, snapshot)
	if err := f.writeSnapshotIndex(id, index); err != nil {
		return Snapshot{}, err
	}

	return snapshot, nil
}

func (f *FsRepo) GetSnapshot(ctx context.Context, id NodeId, rev RevisionID, opts SnapshotReadOptions) (Snapshot, []byte, []byte, *NodeStats, error) {
	index, err := f.readSnapshotIndex(ctx, id)
	if err != nil {
		return Snapshot{}, nil, nil, nil, err
	}

	snap, err := snapshotFromIndex(index, rev)
	if err != nil {
		return Snapshot{}, nil, nil, nil, err
	}

	var content []byte
	if opts.ResolveContent {
		content, err = f.readContentAtIndex(ctx, id, index, rev)
		if err != nil {
			return Snapshot{}, nil, nil, nil, err
		}
	}

	meta, err := f.runtime.ReadFile(f.snapshotMetaPath(id, rev))
	if err != nil {
		if os.IsNotExist(err) {
			return Snapshot{}, nil, nil, nil, ErrNotExist
		}
		return Snapshot{}, nil, nil, nil, NewBackendError(f.Name(), "GetSnapshotMeta", 0, err, false)
	}

	statsBytes, err := f.runtime.ReadFile(f.snapshotStatsPath(id, rev))
	if err != nil {
		if os.IsNotExist(err) {
			return Snapshot{}, nil, nil, nil, ErrNotExist
		}
		return Snapshot{}, nil, nil, nil, NewBackendError(f.Name(), "GetSnapshotStats", 0, err, false)
	}
	stats, err := snapshotStatsFromBytes(ctx, statsBytes)
	if err != nil {
		return Snapshot{}, nil, nil, nil, err
	}

	return snap, content, meta, stats, nil
}

func (f *FsRepo) ListSnapshots(ctx context.Context, id NodeId) ([]Snapshot, error) {
	index, err := f.readSnapshotIndex(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]Snapshot, len(index))
	copy(out, index)
	return out, nil
}

func (f *FsRepo) ReadContentAt(ctx context.Context, id NodeId, rev RevisionID) ([]byte, error) {
	index, err := f.readSnapshotIndex(ctx, id)
	if err != nil {
		return nil, err
	}
	return f.readContentAtIndex(ctx, id, index, rev)
}

func (f *FsRepo) RestoreSnapshot(ctx context.Context, id NodeId, rev RevisionID, createRestoreSnapshot bool) error {
	if contextHasNodeLock(ctx, id) {
		return f.restoreSnapshotLocked(ctx, id, rev, createRestoreSnapshot)
	}
	return f.WithNodeLock(ctx, id, func(lockCtx context.Context) error {
		return f.restoreSnapshotLocked(lockCtx, id, rev, createRestoreSnapshot)
	})
}

func (f *FsRepo) restoreSnapshotLocked(ctx context.Context, id NodeId, rev RevisionID, createRestoreSnapshot bool) error {
	index, err := f.readSnapshotIndex(ctx, id)
	if err != nil {
		return err
	}
	if _, err := snapshotFromIndex(index, rev); err != nil {
		return err
	}

	content, err := f.readContentAtIndex(ctx, id, index, rev)
	if err != nil {
		return err
	}
	meta, err := f.runtime.ReadFile(f.snapshotMetaPath(id, rev))
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotExist
		}
		return NewBackendError(f.Name(), "RestoreSnapshotMeta", 0, err, false)
	}
	statsBytes, err := f.runtime.ReadFile(f.snapshotStatsPath(id, rev))
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotExist
		}
		return NewBackendError(f.Name(), "RestoreSnapshotStats", 0, err, false)
	}
	stats, err := snapshotStatsFromBytes(ctx, statsBytes)
	if err != nil {
		return err
	}

	if err := f.WriteContent(ctx, id, content); err != nil {
		return err
	}
	if err := f.WriteMeta(ctx, id, meta); err != nil {
		return err
	}
	if err := f.WriteStats(ctx, id, stats); err != nil {
		return err
	}
	if !createRestoreSnapshot {
		return nil
	}

	var parent RevisionID
	if len(index) > 0 {
		parent = index[len(index)-1].ID
	}
	_, err = f.appendSnapshotLocked(ctx, id, SnapshotWrite{
		ExpectedParent: parent,
		Message:        fmt.Sprintf("restore from rev %d", rev),
		Meta:           meta,
		Stats:          stats,
		Content: SnapshotContentWrite{
			Kind: SnapshotContentKindFull,
			Data: content,
			Hash: hashSnapshotBytes(f.runtime, content),
		},
	})
	return err
}

func (f *FsRepo) readSnapshotIndex(ctx context.Context, id NodeId) ([]Snapshot, error) {
	exists, err := f.HasNode(ctx, id)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotExist
	}

	path := f.snapshotIndexPath(id)
	raw, err := f.runtime.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []Snapshot{}, nil
		}
		return nil, NewBackendError(f.Name(), "ReadSnapshotIndex", 0, err, false)
	}
	if len(raw) == 0 {
		return []Snapshot{}, nil
	}

	var index []Snapshot
	if err := json.Unmarshal(raw, &index); err != nil {
		return nil, NewBackendError(f.Name(), "ReadSnapshotIndex", 0, err, false)
	}
	return index, nil
}

func (f *FsRepo) writeSnapshotIndex(id NodeId, index []Snapshot) error {
	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return NewBackendError(f.Name(), "WriteSnapshotIndex", 0, err, false)
	}
	if err := f.runtime.AtomicWriteFile(f.snapshotIndexPath(id), raw, 0o644); err != nil {
		return NewBackendError(f.Name(), "WriteSnapshotIndex", 0, err, false)
	}
	return nil
}

func (f *FsRepo) readContentAtIndex(ctx context.Context, id NodeId, index []Snapshot, rev RevisionID) ([]byte, error) {
	if _, err := snapshotFromIndex(index, rev); err != nil {
		return nil, err
	}

	var start int = -1
	for i := len(index) - 1; i >= 0; i-- {
		if index[i].ID > rev {
			continue
		}
		if index[i].IsCheckpoint {
			start = i
			break
		}
	}
	if start == -1 {
		return nil, fmt.Errorf("snapshot %d has no checkpoint: %w", rev, ErrInvalid)
	}

	fullPath := f.snapshotContentPath(id, index[start].ID, SnapshotContentKindFull)
	content, err := f.runtime.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotExist
		}
		return nil, NewBackendError(f.Name(), "ReadSnapshotFull", 0, err, false)
	}

	for i := start + 1; i < len(index); i++ {
		snap := index[i]
		if snap.ID > rev {
			break
		}
		patchPath := f.snapshotContentPath(id, snap.ID, SnapshotContentKindPatch)
		patchBytes, err := f.runtime.ReadFile(patchPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, ErrNotExist
			}
			return nil, NewBackendError(f.Name(), "ReadSnapshotPatch", 0, err, false)
		}
		content, err = applySnapshotPatch(f.runtime.Hasher(), content, patchBytes)
		if err != nil {
			return nil, err
		}
		if snap.ContentHash != "" && snap.ContentHash != hashSnapshotBytes(f.runtime, content) {
			return nil, fmt.Errorf("snapshot content hash mismatch for rev %d: %w", snap.ID, ErrConflict)
		}
	}

	if expected, err := snapshotFromIndex(index, rev); err == nil && expected.ContentHash != "" && expected.ContentHash != hashSnapshotBytes(f.runtime, content) {
		return nil, fmt.Errorf("snapshot content hash mismatch for rev %d: %w", rev, ErrConflict)
	}
	_ = ctx
	return content, nil
}

func snapshotFromIndex(index []Snapshot, rev RevisionID) (Snapshot, error) {
	for _, snap := range index {
		if snap.ID == rev {
			return snap, nil
		}
	}
	return Snapshot{}, ErrNotExist
}

func (f *FsRepo) snapshotDir(id NodeId) string {
	return filepath.Join(f.Root, id.Path(), "snapshots")
}

func (f *FsRepo) snapshotIndexPath(id NodeId) string {
	return filepath.Join(f.snapshotDir(id), "index.json")
}

func (f *FsRepo) snapshotMetaPath(id NodeId, rev RevisionID) string {
	return filepath.Join(f.snapshotDir(id), fmt.Sprintf("%d.meta", rev))
}

func (f *FsRepo) snapshotStatsPath(id NodeId, rev RevisionID) string {
	return filepath.Join(f.snapshotDir(id), fmt.Sprintf("%d.stats", rev))
}

func (f *FsRepo) snapshotContentPath(id NodeId, rev RevisionID, kind SnapshotContentKind) string {
	ext := ".full"
	if kind == SnapshotContentKindPatch {
		ext = ".patch"
	}
	return filepath.Join(f.snapshotDir(id), fmt.Sprintf("%d%s", rev, ext))
}

var _ RepositorySnapshots = (*FsRepo)(nil)
