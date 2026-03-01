package keg_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

type snapshotRepo struct {
	name string
	repo interface {
		keg.Repository
		keg.RepositorySnapshots
	}
	root string
}

func TestRepositorySnapshots_Contract(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		new  func(*testing.T) (context.Context, snapshotRepo)
	}{
		{name: "memory", new: newMemorySnapshotRepo},
		{name: "filesystem", new: newFilesystemSnapshotRepo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, sr := tc.new(t)
			id := keg.NodeId{ID: 7}

			stats1 := snapshotStats(time.Date(2026, 2, 26, 9, 0, 0, 0, time.UTC), "alpha", "alpha lead", "h1")
			writeSnapshotState(t, ctx, sr.repo, id, "# Alpha\n\nFirst.\n", "title: Alpha\n", stats1)
			snap1CreatedAt := time.Date(2026, 2, 26, 9, 5, 0, 0, time.UTC)

			snap1, err := sr.repo.AppendSnapshot(ctx, id, keg.SnapshotWrite{
				ExpectedParent: 0,
				Message:        "initial",
				CreatedAt:      snap1CreatedAt,
				Meta:           []byte("title: Alpha\n"),
				Stats:          stats1,
				Content: keg.SnapshotContentWrite{
					Kind: keg.SnapshotContentKindFull,
					Data: []byte("# Alpha\n\nFirst.\n"),
				},
			})
			require.NoError(t, err)
			require.Equal(t, keg.RevisionID(1), snap1.ID)
			require.True(t, snap1.CreatedAt.Equal(snap1CreatedAt))
			require.True(t, snap1.IsCheckpoint)

			stats2 := snapshotStats(time.Date(2026, 2, 26, 10, 0, 0, 0, time.UTC), "beta", "beta lead", "h2")
			writeSnapshotState(t, ctx, sr.repo, id, "# Beta\n\nSecond.\n", "title: Beta\n", stats2)

			snap2, err := sr.repo.AppendSnapshot(ctx, id, keg.SnapshotWrite{
				ExpectedParent: snap1.ID,
				Message:        "update",
				Meta:           []byte("title: Beta\n"),
				Stats:          stats2,
				Content: keg.SnapshotContentWrite{
					Kind: keg.SnapshotContentKindPatch,
					Base: snap1.ID,
					Data: []byte("# Beta\n\nSecond.\n"),
				},
			})
			require.NoError(t, err)
			require.Equal(t, keg.RevisionID(2), snap2.ID)

			list, err := sr.repo.ListSnapshots(ctx, id)
			require.NoError(t, err)
			require.Len(t, list, 2)
			require.Equal(t, snap1.ID, list[0].ID)
			require.True(t, list[0].CreatedAt.Equal(snap1CreatedAt))
			require.Equal(t, snap2.ID, list[1].ID)

			content1, err := sr.repo.ReadContentAt(ctx, id, snap1.ID)
			require.NoError(t, err)
			require.Equal(t, "# Alpha\n\nFirst.\n", string(content1))

			content2, err := sr.repo.ReadContentAt(ctx, id, snap2.ID)
			require.NoError(t, err)
			require.Equal(t, "# Beta\n\nSecond.\n", string(content2))

			gotSnap2, resolvedContent2, resolvedMeta2, resolvedStats2, err := sr.repo.GetSnapshot(ctx, id, snap2.ID, keg.SnapshotReadOptions{ResolveContent: true})
			require.NoError(t, err)
			require.Equal(t, snap2.ID, gotSnap2.ID)
			require.Equal(t, "# Beta\n\nSecond.\n", string(resolvedContent2))
			require.Equal(t, "title: Beta\n", string(resolvedMeta2))
			require.Equal(t, "beta", resolvedStats2.Title())

			require.NoError(t, sr.repo.RestoreSnapshot(ctx, id, snap1.ID, false))
			liveContent, err := sr.repo.ReadContent(ctx, id)
			require.NoError(t, err)
			require.Equal(t, "# Alpha\n\nFirst.\n", string(liveContent))
			liveMeta, err := sr.repo.ReadMeta(ctx, id)
			require.NoError(t, err)
			require.Equal(t, "title: Alpha\n", string(liveMeta))
			liveStats, err := sr.repo.ReadStats(ctx, id)
			require.NoError(t, err)
			require.Equal(t, "alpha", liveStats.Title())

			list, err = sr.repo.ListSnapshots(ctx, id)
			require.NoError(t, err)
			require.Len(t, list, 2)

			require.NoError(t, sr.repo.RestoreSnapshot(ctx, id, snap2.ID, true))
			list, err = sr.repo.ListSnapshots(ctx, id)
			require.NoError(t, err)
			require.Len(t, list, 3)
			require.Equal(t, "restore from rev 2", list[2].Message)
			liveContent, err = sr.repo.ReadContent(ctx, id)
			require.NoError(t, err)
			require.Equal(t, "# Beta\n\nSecond.\n", string(liveContent))
		})
	}
}

func TestRepositorySnapshots_Conflict(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		new  func(*testing.T) (context.Context, snapshotRepo)
	}{
		{name: "memory", new: newMemorySnapshotRepo},
		{name: "filesystem", new: newFilesystemSnapshotRepo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, sr := tc.new(t)
			id := keg.NodeId{ID: 8}
			stats := snapshotStats(time.Date(2026, 2, 26, 11, 0, 0, 0, time.UTC), "alpha", "alpha lead", "h1")
			writeSnapshotState(t, ctx, sr.repo, id, "# Alpha\n", "title: Alpha\n", stats)

			_, err := sr.repo.AppendSnapshot(ctx, id, keg.SnapshotWrite{
				ExpectedParent: 0,
				Message:        "initial",
				Meta:           []byte("title: Alpha\n"),
				Stats:          stats,
				Content: keg.SnapshotContentWrite{
					Kind: keg.SnapshotContentKindFull,
					Data: []byte("# Alpha\n"),
				},
			})
			require.NoError(t, err)

			_, err = sr.repo.AppendSnapshot(ctx, id, keg.SnapshotWrite{
				ExpectedParent: 0,
				Message:        "stale",
				Meta:           []byte("title: Alpha\n"),
				Stats:          stats,
				Content: keg.SnapshotContentWrite{
					Kind: keg.SnapshotContentKindFull,
					Data: []byte("# Alpha\n"),
				},
			})
			require.ErrorIs(t, err, keg.ErrConflict)
		})
	}
}

func TestFsRepo_SnapshotCheckpointRollover(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)
	ctx := fx.Context()
	root := t.TempDir()

	repo := keg.NewFsRepo(root, fx.Runtime())
	repo.SnapshotCheckpointInterval = 1

	id := keg.NodeId{ID: 9}
	stats1 := snapshotStats(time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC), "one", "one lead", "h1")
	writeSnapshotState(t, ctx, repo, id, "# One\n", "title: One\n", stats1)
	_, err := repo.AppendSnapshot(ctx, id, keg.SnapshotWrite{
		ExpectedParent: 0,
		Message:        "one",
		Meta:           []byte("title: One\n"),
		Stats:          stats1,
		Content:        keg.SnapshotContentWrite{Kind: keg.SnapshotContentKindFull, Data: []byte("# One\n")},
	})
	require.NoError(t, err)

	stats2 := snapshotStats(time.Date(2026, 2, 26, 13, 0, 0, 0, time.UTC), "two", "two lead", "h2")
	writeSnapshotState(t, ctx, repo, id, "# Two\n", "title: Two\n", stats2)
	_, err = repo.AppendSnapshot(ctx, id, keg.SnapshotWrite{
		ExpectedParent: 1,
		Message:        "two",
		Meta:           []byte("title: Two\n"),
		Stats:          stats2,
		Content:        keg.SnapshotContentWrite{Kind: keg.SnapshotContentKindPatch, Base: 1, Data: []byte("# Two\n")},
	})
	require.NoError(t, err)

	stats3 := snapshotStats(time.Date(2026, 2, 26, 14, 0, 0, 0, time.UTC), "three", "three lead", "h3")
	writeSnapshotState(t, ctx, repo, id, "# Three\n", "title: Three\n", stats3)
	_, err = repo.AppendSnapshot(ctx, id, keg.SnapshotWrite{
		ExpectedParent: 2,
		Message:        "three",
		Meta:           []byte("title: Three\n"),
		Stats:          stats3,
		Content:        keg.SnapshotContentWrite{Kind: keg.SnapshotContentKindPatch, Base: 2, Data: []byte("# Three\n")},
	})
	require.NoError(t, err)

	_, err = repo.Runtime().Stat(filepath.Join(root, id.Path(), "snapshots", "1.full"), false)
	require.NoError(t, err)
	_, err = repo.Runtime().Stat(filepath.Join(root, id.Path(), "snapshots", "2.patch"), false)
	require.NoError(t, err)
	_, err = repo.Runtime().Stat(filepath.Join(root, id.Path(), "snapshots", "3.full"), false)
	require.NoError(t, err)
	_, err = repo.Runtime().Stat(filepath.Join(root, id.Path(), "snapshots", "3.patch"), false)
	require.Error(t, err)
	require.True(t, os.IsNotExist(err))
}

func newMemorySnapshotRepo(t *testing.T) (context.Context, snapshotRepo) {
	t.Helper()
	fx := NewSandbox(t)
	return fx.Context(), snapshotRepo{
		name: "memory",
		repo: keg.NewMemoryRepo(fx.Runtime()),
	}
}

func newFilesystemSnapshotRepo(t *testing.T) (context.Context, snapshotRepo) {
	t.Helper()
	fx := NewSandbox(t)
	root := t.TempDir()
	return fx.Context(), snapshotRepo{
		name: "filesystem",
		repo: keg.NewFsRepo(root, fx.Runtime()),
		root: root,
	}
}

func writeSnapshotState(t *testing.T, ctx context.Context, repo keg.Repository, id keg.NodeId, content string, meta string, stats *keg.NodeStats) {
	t.Helper()
	require.NoError(t, repo.WriteContent(ctx, id, []byte(content)))
	require.NoError(t, repo.WriteMeta(ctx, id, []byte(meta)))
	require.NoError(t, repo.WriteStats(ctx, id, stats))
}

func snapshotStats(now time.Time, title string, lead string, hash string) *keg.NodeStats {
	stats := keg.NewStats(now)
	stats.SetTitle(title)
	stats.SetLead(lead)
	stats.SetHash(hash, &now)
	stats.SetCreated(now)
	stats.SetUpdated(now)
	stats.SetAccessed(now)
	return stats
}
