package keg

import (
	"context"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

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

			corruptLatestMemorySnapshot(t, repo, id, func(entry *memorySnapshotEntry) {
				entry.snapshot.ContentHash = tc.hash
			})

			result, err = k.RunSnapshotPolicy(ctx)
			require.NoError(t, err)
			require.Equal(t, 0, result.CreatedCount())

			snaps, err := k.ListSnapshots(ctx, id)
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

			corruptLatestMemorySnapshot(t, repo, id, func(entry *memorySnapshotEntry) {
				entry.snapshot.ContentHash = tc.hash
				entry.content = []byte("# Legacy Drift Target\n\nolder content\n")
			})

			result, err = k.RunSnapshotPolicy(ctx)
			require.NoError(t, err)
			require.Len(t, result.Created, 1)
			require.Equal(t, RevisionID(2), result.Created[0].ID)

			snaps, err := k.ListSnapshots(ctx, id)
			require.NoError(t, err)
			require.Len(t, snaps, 2)
		})
	}
}

func newInternalSnapshotPolicyTestKeg(t *testing.T) (*sandbox.Sandbox, *LocalKeg, *MemoryRepo) {
	t.Helper()

	fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	repo := NewMemoryRepo(fx.Runtime())
	k := NewLocalKeg(repo, fx.Runtime())
	require.NoError(t, k.Init(context.Background()))
	_, err := k.AppendSnapshot(context.Background(), NodeId{ID: 0}, "seed zero")
	require.NoError(t, err)
	return fx, k, repo
}

func corruptLatestMemorySnapshot(t *testing.T, repo *MemoryRepo, id NodeId, mutate func(*memorySnapshotEntry)) {
	t.Helper()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	entries := repo.snapshots[id]
	require.NotEmpty(t, entries)
	mutate(&entries[len(entries)-1])
	repo.snapshots[id] = entries
}
