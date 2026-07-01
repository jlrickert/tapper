package keg

import (
	"context"
	"errors"
	"fmt"
)

func (k *LocalKeg) AppendSnapshot(ctx context.Context, id NodeId, msg string) (Snapshot, error) {
	return k.appendSnapshot(ctx, id, msg, true)
}

func (k *LocalKeg) appendSnapshot(ctx context.Context, id NodeId, msg string, refreshIndexes bool) (Snapshot, error) {
	out, err := k.appendSnapshotNoRefresh(ctx, id, msg)
	if err != nil {
		return out, err
	}
	if refreshIndexes {
		if err := k.refreshSnapshotGeneratedIndexes(ctx); err != nil {
			return out, fmt.Errorf("failed to refresh snapshot indexes: %w", err)
		}
	}
	return out, nil
}

func (k *LocalKeg) appendSnapshotLocked(ctx context.Context, id NodeId, msg string) (Snapshot, error) {
	snapshots, ok := repoSnapshots(k.Repo)
	if !ok {
		return Snapshot{}, ErrNotSupported
	}

	existing, err := snapshots.ListSnapshots(ctx, id)
	if err != nil && !errors.Is(err, ErrNotExist) {
		return Snapshot{}, err
	}

	content, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	meta, err := k.Repo.ReadMeta(ctx, id)
	if err != nil && !errors.Is(err, ErrNotExist) {
		return Snapshot{}, err
	}
	stats, err := k.Repo.ReadStats(ctx, id)
	if err != nil && !errors.Is(err, ErrNotExist) {
		return Snapshot{}, err
	}
	if stats == nil {
		stats = &NodeStats{}
	}

	var parent RevisionID
	if len(existing) > 0 {
		parent = existing[len(existing)-1].ID
	}
	createdAt := k.Runtime.Clock().Now()
	contentHash := hashSnapshotBytes(k.Runtime, content)
	pending, err := timelineSnapshotStateFromPayload(ctx, k.Runtime, Snapshot{
		ID:           parent + 1,
		Node:         id,
		Parent:       parent,
		CreatedAt:    createdAt,
		Message:      msg,
		ContentHash:  contentHash,
		IsCheckpoint: true,
	}, content, meta, stats)
	if err != nil {
		return Snapshot{}, fmt.Errorf("failed to prepare snapshot timeline event: %w", err)
	}
	omega, err := k.computePendingSnapshotOmega(ctx, pending)
	if err != nil {
		return Snapshot{}, fmt.Errorf("failed to compute snapshot omega: %w", err)
	}
	if omega == nil {
		stats.ClearOmega()
	} else {
		stats.SetOmega(*omega)
	}

	out, err := snapshots.AppendSnapshot(ctx, id, SnapshotWrite{
		ExpectedParent: parent,
		Message:        msg,
		CreatedAt:      createdAt,
		Meta:           contentOrNil(meta),
		Stats:          stats,
		Content: SnapshotContentWrite{
			Kind: SnapshotContentKindFull,
			Base: parent,
			Data: contentOrNil(content),
			Hash: contentHash,
		},
	})
	if err != nil {
		return Snapshot{}, err
	}
	return out, nil
}

func (k *LocalKeg) appendSnapshotNoRefresh(ctx context.Context, id NodeId, msg string) (Snapshot, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return Snapshot{}, fmt.Errorf("failed to snapshot node: %w", err)
	}
	if _, ok := repoSnapshots(k.Repo); !ok {
		return Snapshot{}, ErrNotSupported
	}
	var out Snapshot
	err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		snap, err := k.appendSnapshotLocked(lockCtx, id, msg)
		if err != nil {
			return err
		}
		out = snap
		return nil
	})
	if err != nil {
		return out, err
	}
	return out, nil
}

func (k *LocalKeg) refreshSnapshotIndexesAfterPolicy(ctx context.Context, created int) error {
	if created == 0 {
		return nil
	}
	if err := k.refreshSnapshotGeneratedIndexes(ctx); err != nil {
		return fmt.Errorf("failed to refresh snapshot indexes: %w", err)
	}
	return nil
}

func (k *LocalKeg) ListSnapshots(ctx context.Context, id NodeId) ([]Snapshot, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to list snapshots: %w", err)
	}
	snapshots, ok := repoSnapshots(k.Repo)
	if !ok {
		return nil, ErrNotSupported
	}
	return snapshots.ListSnapshots(ctx, id)
}

// GetSnapshot returns revision metadata and, per opts, resolved content,
// meta, and stats payloads.
func (k *LocalKeg) GetSnapshot(ctx context.Context, id NodeId, rev RevisionID, opts SnapshotReadOptions) (Snapshot, []byte, []byte, *NodeStats, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return Snapshot{}, nil, nil, nil, fmt.Errorf("failed to read snapshot: %w", err)
	}
	snapshots, ok := repoSnapshots(k.Repo)
	if !ok {
		return Snapshot{}, nil, nil, nil, ErrNotSupported
	}
	return snapshots.GetSnapshot(ctx, id, rev, opts)
}

func (k *LocalKeg) ReadContentAt(ctx context.Context, id NodeId, rev RevisionID) ([]byte, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to read snapshot content: %w", err)
	}
	snapshots, ok := repoSnapshots(k.Repo)
	if !ok {
		return nil, ErrNotSupported
	}
	return snapshots.ReadContentAt(ctx, id, rev)
}

func (k *LocalKeg) RestoreSnapshot(ctx context.Context, id NodeId, rev RevisionID) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to restore snapshot: %w", err)
	}
	snapshots, ok := repoSnapshots(k.Repo)
	if !ok {
		return ErrNotSupported
	}
	_, contentBytes, metaBytes, stats, err := snapshots.GetSnapshot(ctx, id, rev, SnapshotReadOptions{ResolveContent: true})
	if err != nil {
		return err
	}
	content, err := ParseContent(k.Runtime, contentBytes, MarkdownContentFilename)
	if err != nil {
		return fmt.Errorf("snapshot content is invalid: %w", err)
	}
	meta, err := ParseMeta(ctx, metaBytes)
	if err != nil {
		return fmt.Errorf("snapshot metadata is invalid: %w", err)
	}
	if stats == nil {
		stats = &NodeStats{}
	}
	proposed := &NodeData{ID: id, Content: content, Meta: meta, Stats: stats}
	if err := proposed.updateMeta(ctx, k.Runtime, nil); err != nil {
		return fmt.Errorf("snapshot metadata is invalid: %w", err)
	}
	if err := k.validateForWrite(ctx, schemaWriteRestore, id, proposed); err != nil {
		return err
	}
	if err := snapshots.RestoreSnapshot(ctx, id, rev, true); err != nil {
		return err
	}

	data, err := k.getNode(ctx, id)
	if err != nil {
		return err
	}
	if err := k.writeNodeToDex(ctx, id, data); err != nil {
		return err
	}
	return k.refreshSnapshotGeneratedIndexes(ctx)
}

func contentOrNil(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	return append([]byte(nil), data...)
}
