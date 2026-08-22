package keg_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestSnapshotPolicy_MissingSnapshotAfterIdle(t *testing.T) {
	fx, k := newSnapshotPolicyTestKeg(t)
	ctx := fx.Context()

	id, err := k.Create(ctx, &kegpkg.CreateOptions{Title: "Policy Target"})
	require.NoError(t, err)

	fx.Advance(59 * time.Minute)
	result, err := k.RunSnapshotPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.CreatedCount())

	fx.Advance(2 * time.Minute)
	result, err = k.RunSnapshotPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, kegpkg.SnapshotModeAuto, result.Mode)
	require.Equal(t, time.Hour, result.IdleAfter)
	require.Len(t, result.Created, 1)
	require.True(t, result.Created[0].Node.Equals(id.ID))
	require.Equal(t, kegpkg.AutoSnapshotMessage(time.Hour), result.Created[0].Message)
	timeline, err := k.Repo.GetIndex(ctx, kegpkg.TimelineIndexName)
	require.NoError(t, err)
	require.Contains(t, string(timeline), kegpkg.AutoSnapshotMessage(time.Hour))
	dirty, err := k.Repo.GetIndex(ctx, kegpkg.DirtyIndexName)
	require.NoError(t, err)
	require.Empty(t, strings.TrimSpace(string(dirty)))

	result, err = k.RunSnapshotPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.CreatedCount(), "repeated scans should not duplicate snapshots")
}

func TestSnapshotPolicy_OffModeSkipsSnapshots(t *testing.T) {
	fx, k := newSnapshotPolicyTestKeg(t)
	ctx := fx.Context()

	require.NoError(t, k.UpdateConfig(ctx, func(cfg *kegpkg.Config) {
		cfg.Snapshots = &kegpkg.SnapshotConfig{Mode: kegpkg.SnapshotModeOff}
	}))
	_, err := k.Create(ctx, &kegpkg.CreateOptions{Title: "No Auto Snapshot"})
	require.NoError(t, err)

	fx.Advance(2 * time.Hour)
	result, err := k.RunSnapshotPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, kegpkg.SnapshotModeOff, result.Mode)
	require.Equal(t, 0, result.CreatedCount())
}

func TestSnapshotPolicy_ContentDriftCreatesSnapshot(t *testing.T) {
	fx, k := newSnapshotPolicyTestKeg(t)
	ctx := fx.Context()

	id, err := k.Create(ctx, &kegpkg.CreateOptions{Title: "Drift Target"})
	require.NoError(t, err)
	fx.Advance(2 * time.Hour)
	result, err := k.RunSnapshotPolicy(ctx)
	require.NoError(t, err)
	require.Len(t, result.Created, 1)

	fx.Advance(time.Minute)
	require.NoError(t, k.SetContent(ctx, id.ID, []byte("# Drift Target\n\nnew content\n")))
	fx.Advance(2 * time.Hour)
	result, err = k.RunSnapshotPolicy(ctx)
	require.NoError(t, err)
	require.Len(t, result.Created, 1)
	require.Equal(t, kegpkg.RevisionID(2), result.Created[0].ID)
}

func TestSnapshotPolicy_MetadataDriftDoesNotCreateSnapshotAndRemainsDirty(t *testing.T) {
	fx, k := newSnapshotPolicyTestKeg(t)
	ctx := fx.Context()

	id, err := k.Create(ctx, &kegpkg.CreateOptions{Title: "Metadata Target"})
	require.NoError(t, err)
	fx.Advance(2 * time.Hour)
	result, err := k.RunSnapshotPolicy(ctx)
	require.NoError(t, err)
	require.Len(t, result.Created, 1)

	fx.Advance(time.Minute)
	require.NoError(t, k.UpdateMeta(ctx, id.ID, func(meta *kegpkg.NodeMeta) {
		_ = meta.Set(ctx, "status", "ready")
	}))
	stats, err := k.GetStats(ctx, id.ID)
	require.NoError(t, err)
	require.Equal(t, fx.Now(), stats.Updated())

	fx.Advance(2 * time.Hour)
	result, err = k.RunSnapshotPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.CreatedCount())

	snaps, err := k.ListSnapshots(ctx, id.ID)
	require.NoError(t, err)
	require.Len(t, snaps, 1)

	dirty, err := k.Repo.GetIndex(ctx, kegpkg.DirtyIndexName)
	require.NoError(t, err)
	require.Contains(t, string(dirty), `"node":"`+id.ID.Path()+`"`)
}

func TestSnapshotPolicy_TouchDoesNotCreateSnapshot(t *testing.T) {
	fx, k := newSnapshotPolicyTestKeg(t)
	ctx := fx.Context()

	id, err := k.Create(ctx, &kegpkg.CreateOptions{Title: "Touch Target"})
	require.NoError(t, err)
	fx.Advance(2 * time.Hour)
	result, err := k.RunSnapshotPolicy(ctx)
	require.NoError(t, err)
	require.Len(t, result.Created, 1)

	fx.Advance(time.Minute)
	require.NoError(t, k.Touch(ctx, id.ID))
	fx.Advance(2 * time.Hour)
	result, err = k.RunSnapshotPolicy(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, result.CreatedCount())
}

func newSnapshotPolicyTestKeg(t *testing.T) (*sandbox.Sandbox, *kegpkg.LocalKeg) {
	t.Helper()

	fx := NewSandbox(t)
	repo := kegpkg.NewMemoryRepo(fx.Runtime())
	k := kegpkg.NewLocalKeg(repo, fx.Runtime())
	initNonStrictTestKeg(t, k, context.Background())
	_, err := k.AppendSnapshot(context.Background(), kegpkg.NodeId{ID: 0}, "seed zero")
	require.NoError(t, err)
	return fx, k
}
