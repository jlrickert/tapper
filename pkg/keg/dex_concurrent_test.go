package keg

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/stretchr/testify/require"
)

// TestDexAdd_ConcurrentSafe verifies that parallel Add calls to a Dex
// result in all entries being present.
func TestDexAdd_ConcurrentSafe(t *testing.T) {
	t.Parallel()

	rt, err := toolkit.NewRuntime()
	require.NoError(t, err)

	ctx := t.Context()
	dex := &Dex{}
	now := time.Date(2025, 10, 15, 12, 0, 0, 0, time.UTC)

	const N = 20
	errs := make([]error, N)
	var wg sync.WaitGroup

	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			stats := NewStats(now)
			stats.SetTitle(fmt.Sprintf("Node %d", idx+1))
			stats.SetHash(rt.Hasher().Hash([]byte(fmt.Sprintf("content %d", idx+1))), &now)

			data := &NodeData{
				ID: NodeId{ID: idx + 1},
				Content: &NodeContent{
					Title: fmt.Sprintf("Node %d", idx+1),
					Lead:  fmt.Sprintf("Lead %d", idx+1),
				},
				Meta:  NewMeta(ctx, now),
				Stats: stats,
			}
			errs[idx] = dex.Add(ctx, data)
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "Add goroutine %d failed", i)
	}

	nodes := dex.Nodes(ctx)
	require.Equal(t, N, len(nodes), "expected %d nodes in dex", N)
}

// TestDexWrite_ConcurrentSafe verifies that concurrent Dex.Write calls do not
// race on the shared error collection or corrupt index output.
func TestDexWrite_ConcurrentSafe(t *testing.T) {
	t.Parallel()

	rt, err := toolkit.NewRuntime()
	require.NoError(t, err)

	ctx := t.Context()
	repo := NewMemoryRepo(rt)
	dex := &Dex{}

	now := time.Date(2025, 10, 15, 12, 0, 0, 0, time.UTC)
	for i := range 5 {
		stats := NewStats(now)
		stats.SetTitle(fmt.Sprintf("Node %d", i))
		stats.SetHash(rt.Hasher().Hash([]byte(fmt.Sprintf("content %d", i))), &now)

		data := &NodeData{
			ID: NodeId{ID: i},
			Content: &NodeContent{
				Title: fmt.Sprintf("Node %d", i),
				Lead:  fmt.Sprintf("Lead %d", i),
			},
			Meta:  NewMeta(ctx, now),
			Stats: stats,
		}
		require.NoError(t, dex.Add(ctx, data))
	}

	// Concurrent Write calls should serialize via dex.mu and not race.
	const N = 5
	errs := make([]error, N)
	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = dex.Write(ctx, repo)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "Write goroutine %d failed", i)
	}

	// Verify indexes were written.
	_, err = repo.GetIndex(ctx, "nodes.tsv")
	require.NoError(t, err)
	_, err = repo.GetIndex(ctx, "tags")
	require.NoError(t, err)
}
