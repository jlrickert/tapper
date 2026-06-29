package keg

import (
	"context"
	"errors"
	"fmt"
)

func (k *LocalKeg) AppendSnapshot(ctx context.Context, id NodeId, msg string) (Snapshot, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return Snapshot{}, fmt.Errorf("failed to snapshot node: %w", err)
	}

	snapshots, ok := repoSnapshots(k.Repo)
	if !ok {
		return Snapshot{}, ErrNotSupported
	}

	var out Snapshot
	err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		existing, err := snapshots.ListSnapshots(lockCtx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return err
		}

		content, err := k.Repo.ReadContent(lockCtx, id)
		if err != nil {
			return err
		}
		meta, err := k.Repo.ReadMeta(lockCtx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return err
		}
		stats, err := k.Repo.ReadStats(lockCtx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return err
		}
		if stats == nil {
			stats = &NodeStats{}
		}
		if err := k.applyComputedOmega(lockCtx, id, stats); err != nil {
			return fmt.Errorf("failed to compute omega: %w", err)
		}

		var parent RevisionID
		if len(existing) > 0 {
			parent = existing[len(existing)-1].ID
		}

		out, err = snapshots.AppendSnapshot(lockCtx, id, SnapshotWrite{
			ExpectedParent: parent,
			Message:        msg,
			Meta:           contentOrNil(meta),
			Stats:          stats,
			Content: SnapshotContentWrite{
				Kind: SnapshotContentKindFull,
				Base: parent,
				Data: contentOrNil(content),
				Hash: hashSnapshotBytes(k.Runtime, content),
			},
		})
		return err
	})
	if err != nil {
		return out, err
	}
	if err := k.refreshSnapshotGeneratedIndexes(ctx); err != nil {
		return out, fmt.Errorf("failed to refresh snapshot indexes: %w", err)
	}
	return out, nil
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
	if err := proposed.UpdateMeta(ctx, nil); err != nil {
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
