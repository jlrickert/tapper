package keg

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

type snapshotBoundaryRepo struct {
	*testMemoryRepository
	reads              atomic.Int32
	writes             atomic.Int32
	settingsUnderWrite atomic.Int32
	inWrite            atomic.Bool
}

func (r *snapshotBoundaryRepo) WithKegRead(ctx context.Context, fn func(context.Context) error) error {
	r.reads.Add(1)
	return r.testMemoryRepository.WithKegRead(ctx, fn)
}

func (r *snapshotBoundaryRepo) WithKegWrite(ctx context.Context, fn func(context.Context) error) error {
	r.writes.Add(1)
	return r.testMemoryRepository.WithKegWrite(ctx, func(writeCtx context.Context) error {
		r.inWrite.Store(true)
		defer r.inWrite.Store(false)
		return fn(writeCtx)
	})
}

func (r *snapshotBoundaryRepo) ReadSettings(ctx context.Context) (*Settings, error) {
	if r.inWrite.Load() {
		r.settingsUnderWrite.Add(1)
	}
	return r.testMemoryRepository.ReadSettings(ctx)
}

func (r *snapshotBoundaryRepo) reset() {
	r.reads.Store(0)
	r.writes.Store(0)
	r.settingsUnderWrite.Store(0)
}

func TestSnapshotPolicy_PreflightsOffWithoutWriteAndRechecksEnabledUnderWrite(t *testing.T) {
	fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	base := newTestMemoryRepo(fx.Runtime())
	repo := &snapshotBoundaryRepo{testMemoryRepository: base}
	k := NewLocalKeg(repo, fx.Runtime())
	require.NoError(t, k.Init(t.Context()))
	require.NoError(t, k.UpdateSettings(t.Context(), func(cfg *Settings) {
		cfg.SchemaPolicy.Strict = false
		cfg.Snapshots = &SnapshotSettings{Mode: SnapshotModeOff}
	}))

	repo.reset()
	result, err := k.RunSnapshotPolicy(t.Context())
	require.NoError(t, err)
	require.Equal(t, SnapshotModeOff, result.Mode)
	require.Equal(t, int32(1), repo.reads.Load())
	require.Zero(t, repo.writes.Load(), "disabled policies must never enter the write boundary")

	require.NoError(t, k.UpdateSettings(t.Context(), func(cfg *Settings) {
		cfg.Snapshots = &SnapshotSettings{Mode: SnapshotModeAuto, IdleAfter: "1h"}
	}))
	repo.reset()
	_, err = k.RunSnapshotPolicy(t.Context())
	require.NoError(t, err)
	require.Equal(t, int32(1), repo.reads.Load())
	require.Equal(t, int32(1), repo.writes.Load())
	require.Equal(t, int32(1), repo.settingsUnderWrite.Load(), "enabled policy must re-read settings after acquiring the write boundary")
}

func TestSnapshotPolicy_BadLatestContentHashWithIdenticalContentDoesNotDuplicate(t *testing.T) {
	for _, tc := range []struct {
		name string
		hash string
	}{
		{name: "empty", hash: ""},
		{name: "wrong", hash: "legacy-wrong-hash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx, k, repo := newInternalSnapshotPolicyTestKeg(t)
			ctx := fx.Context()

			id, err := k.Create(ctx, &CreateOptions{Title: "Legacy Hash Target"})
			require.NoError(t, err)
			fx.Advance(2 * time.Hour)
			result, err := k.RunSnapshotPolicy(ctx)
			require.NoError(t, err)
			require.Len(t, result.Created, 1)

			corruptLatestMemorySnapshot(t, repo, id.ID, func(snapshot *Snapshot, _ *[]byte) {
				snapshot.ContentHash = tc.hash
			})

			result, err = k.RunSnapshotPolicy(ctx)
			require.NoError(t, err)
			require.Equal(t, 0, result.CreatedCount())

			snaps, err := k.ListSnapshots(ctx, id.ID)
			require.NoError(t, err)
			require.Len(t, snaps, 1)
		})
	}
}

func TestSnapshotPolicy_BadLatestContentHashWithDifferentContentCreatesSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name string
		hash string
	}{
		{name: "empty", hash: ""},
		{name: "wrong", hash: "legacy-wrong-hash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx, k, repo := newInternalSnapshotPolicyTestKeg(t)
			ctx := fx.Context()

			id, err := k.Create(ctx, &CreateOptions{Title: "Legacy Drift Target"})
			require.NoError(t, err)
			fx.Advance(2 * time.Hour)
			result, err := k.RunSnapshotPolicy(ctx)
			require.NoError(t, err)
			require.Len(t, result.Created, 1)

			corruptLatestMemorySnapshot(t, repo, id.ID, func(snapshot *Snapshot, content *[]byte) {
				snapshot.ContentHash = tc.hash
				*content = []byte("# Legacy Drift Target\n\nolder content\n")
			})

			result, err = k.RunSnapshotPolicy(ctx)
			require.NoError(t, err)
			require.Len(t, result.Created, 1)
			require.Equal(t, RevisionID(2), result.Created[0].ID)

			snaps, err := k.ListSnapshots(ctx, id.ID)
			require.NoError(t, err)
			require.Len(t, snaps, 2)
		})
	}
}

func newInternalSnapshotPolicyTestKeg(t *testing.T) (*sandbox.Sandbox, *LocalKeg, *testMemoryRepository) {
	t.Helper()

	fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	repo := newTestMemoryRepo(fx.Runtime())
	k := NewLocalKeg(repo, fx.Runtime())
	require.NoError(t, k.Init(context.Background()))
	require.NoError(t, k.UpdateSettings(context.Background(), func(cfg *Settings) {
		cfg.SchemaPolicy.Strict = false
	}))
	_, err := k.AppendSnapshot(context.Background(), NodeId{ID: 0}, "seed zero")
	require.NoError(t, err)
	return fx, k, repo
}

func corruptLatestMemorySnapshot(t *testing.T, repo *testMemoryRepository, id NodeId, mutate func(*Snapshot, *[]byte)) {
	t.Helper()
	require.NoError(t, repo.corruptLatestSnapshot(id, mutate))
}
