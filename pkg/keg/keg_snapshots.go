package keg

import (
	"context"
	"errors"
	"fmt"
)

func (k *Keg) AppendSnapshot(ctx context.Context, id NodeId, msg string) (Snapshot, error) {
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
	return out, err
}

func (k *Keg) ListSnapshots(ctx context.Context, id NodeId) ([]Snapshot, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to list snapshots: %w", err)
	}
	snapshots, ok := repoSnapshots(k.Repo)
	if !ok {
		return nil, ErrNotSupported
	}
	return snapshots.ListSnapshots(ctx, id)
}

func (k *Keg) ReadContentAt(ctx context.Context, id NodeId, rev RevisionID) ([]byte, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to read snapshot content: %w", err)
	}
	snapshots, ok := repoSnapshots(k.Repo)
	if !ok {
		return nil, ErrNotSupported
	}
	return snapshots.ReadContentAt(ctx, id, rev)
}

func (k *Keg) RestoreSnapshot(ctx context.Context, id NodeId, rev RevisionID) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to restore snapshot: %w", err)
	}
	snapshots, ok := repoSnapshots(k.Repo)
	if !ok {
		return ErrNotSupported
	}
	if err := snapshots.RestoreSnapshot(ctx, id, rev, true); err != nil {
		return err
	}

	data, err := k.getNode(ctx, id)
	if err != nil {
		return err
	}
	return k.writeNodeToDex(ctx, id, data)
}

func contentOrNil(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	return append([]byte(nil), data...)
}
