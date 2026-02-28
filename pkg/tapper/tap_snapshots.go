package tapper

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jlrickert/tapper/pkg/keg"
)

type NodeHistoryOptions struct {
	KegTargetOptions
	NodeID string
}

type NodeSnapshotOptions struct {
	KegTargetOptions
	NodeID  string
	Message string
}

type NodeRestoreOptions struct {
	KegTargetOptions
	NodeID string
	Rev    string
}

func (t *Tap) NodeHistory(ctx context.Context, opts NodeHistoryOptions) ([]keg.Snapshot, error) {
	k, id, err := t.resolveSnapshotNode(ctx, opts.KegTargetOptions, opts.NodeID)
	if err != nil {
		return nil, err
	}
	snapshots, err := k.ListSnapshots(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("unable to list snapshots: %w", err)
	}
	return snapshots, nil
}

func (t *Tap) NodeSnapshot(ctx context.Context, opts NodeSnapshotOptions) (keg.Snapshot, error) {
	k, id, err := t.resolveSnapshotNode(ctx, opts.KegTargetOptions, opts.NodeID)
	if err != nil {
		return keg.Snapshot{}, err
	}
	snap, err := k.AppendSnapshot(ctx, id, opts.Message)
	if err != nil {
		return keg.Snapshot{}, fmt.Errorf("unable to append snapshot: %w", err)
	}
	return snap, nil
}

func (t *Tap) NodeRestore(ctx context.Context, opts NodeRestoreOptions) error {
	k, id, err := t.resolveSnapshotNode(ctx, opts.KegTargetOptions, opts.NodeID)
	if err != nil {
		return err
	}
	rev, err := parseRevision(opts.Rev)
	if err != nil {
		return err
	}
	if err := k.RestoreSnapshot(ctx, id, rev); err != nil {
		return fmt.Errorf("unable to restore snapshot: %w", err)
	}
	return nil
}

func (t *Tap) resolveSnapshotNode(ctx context.Context, targetOpts KegTargetOptions, nodeID string) (*keg.Keg, keg.NodeId, error) {
	k, err := t.resolveKeg(ctx, targetOpts)
	if err != nil {
		return nil, keg.NodeId{}, fmt.Errorf("unable to open keg: %w", err)
	}
	id, err := parseNodeID(nodeID)
	if err != nil {
		return nil, keg.NodeId{}, err
	}
	return k, id, nil
}

func parseNodeID(raw string) (keg.NodeId, error) {
	node, err := keg.ParseNode(raw)
	if err != nil {
		return keg.NodeId{}, fmt.Errorf("invalid node ID %q: %w", raw, err)
	}
	if node == nil {
		return keg.NodeId{}, fmt.Errorf("invalid node ID %q: %w", raw, keg.ErrInvalid)
	}
	return *node, nil
}

func parseRevision(raw string) (keg.RevisionID, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid revision %q: %w", raw, keg.ErrInvalid)
	}
	return keg.RevisionID(value), nil
}
