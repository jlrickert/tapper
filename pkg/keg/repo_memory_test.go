package keg_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestMemoryRepo_WriteReadMetaAndContent(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	r := keg.NewMemoryRepo(fx.Runtime())
	ctx := fx.Context()

	id := keg.NodeId{ID: 10}
	content := []byte("# hello\n")
	meta := []byte("title: test\nupdated: 2025-08-11 00:00:00Z\n")

	require.NoError(t, r.WriteContent(ctx, id, content))
	require.NoError(t, r.WriteMeta(ctx, id, meta))

	gotMeta, err := r.ReadMeta(ctx, id)
	require.NoError(t, err)
	require.Equal(t, meta, gotMeta, "meta bytes should match")

	gotContent, err := r.ReadContent(ctx, id)
	require.NoError(t, err)
	require.Equal(t, content, gotContent, "content bytes should match")

	ids, err := r.ListNodes(ctx)
	require.NoError(t, err)
	require.Contains(t, ids, id, "expected ListNodes to contain written id")
}

func TestMemoryRepo_HasNode(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	r := keg.NewMemoryRepo(fx.Runtime())
	ctx := fx.Context()
	id := keg.NodeId{ID: 10}

	exists, err := r.HasNode(ctx, id)
	require.NoError(t, err)
	require.False(t, exists)

	require.NoError(t, r.WriteContent(ctx, id, []byte("hello")))

	exists, err = r.HasNode(ctx, id)
	require.NoError(t, err)
	require.True(t, exists)
}

func TestMemoryRepo_WriteReadStats(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	r := keg.NewMemoryRepo(fx.Runtime())
	ctx := fx.Context()
	id := keg.NodeId{ID: 77}

	require.NoError(t, r.WriteMeta(ctx, id, []byte("title: keep-me\nfoo: bar\n")))

	now := time.Date(2026, 2, 14, 12, 0, 0, 0, time.UTC)
	stats := keg.NewStats(now)
	stats.SetHash("h1", &now)
	stats.SetLead("lead text")
	stats.SetLinks([]keg.NodeId{{ID: 1}, {ID: 2}})
	stats.SetAccessed(now)

	require.NoError(t, r.WriteStats(ctx, id, stats))

	gotStats, err := r.ReadStats(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "h1", gotStats.Hash())
	require.Equal(t, "lead text", gotStats.Lead())
	require.Len(t, gotStats.Links(), 2)

	gotMeta, err := r.ReadMeta(ctx, id)
	require.NoError(t, err)
	require.Contains(t, string(gotMeta), "title: keep-me")
	require.Contains(t, string(gotMeta), "foo: bar")
	require.NotContains(t, string(gotMeta), "hash:")
}

func TestMemoryRepo_ReadMissingReturnsNotFound(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	r := keg.NewMemoryRepo(fx.Runtime())
	ctx := fx.Context()

	missing := keg.NodeId{ID: 9999}

	_, err := r.ReadContent(ctx, missing)
	require.Error(t, err)
	require.ErrorIs(t, err, keg.ErrNotExist)
}

func TestMemoryRepo_WriteAndListIndexes_GetIndex(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	r := keg.NewMemoryRepo(fx.Runtime())
	ctx := fx.Context()

	name := "dex/nodes.tsv"
	data := []byte("1\t2025-08-11 00:00:00Z\tTitle\n")
	require.NoError(t, r.WriteIndex(ctx, name, data))

	got, err := r.GetIndex(ctx, name)
	require.NoError(t, err)
	require.Equal(t, data, got, "index data mismatch")

	list, err := r.ListIndexes(ctx)
	require.NoError(t, err)
	require.Contains(t, list, name, "expected ListIndexes to include written index name")
}

func TestMemoryRepo_AssetsAPI(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	r := keg.NewMemoryRepo(fx.Runtime())
	ctx := fx.Context()
	id := keg.NodeId{ID: 41}

	require.NoError(t, r.WriteAsset(ctx, id, keg.AssetKindImage, "a.png", []byte("png")))
	require.NoError(t, r.WriteAsset(ctx, id, keg.AssetKindItem, "doc.txt", []byte("txt")))

	images, err := r.ListAssets(ctx, id, keg.AssetKindImage)
	require.NoError(t, err)
	require.Equal(t, []string{"a.png"}, images)

	items, err := r.ListAssets(ctx, id, keg.AssetKindItem)
	require.NoError(t, err)
	require.Equal(t, []string{"doc.txt"}, items)

	require.NoError(t, r.DeleteAsset(ctx, id, keg.AssetKindItem, "doc.txt"))
	items, err = r.ListAssets(ctx, id, keg.AssetKindItem)
	require.NoError(t, err)
	require.Empty(t, items)
}

func TestMemoryRepo_MoveNodeAndDestinationExists(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	r := keg.NewMemoryRepo(fx.Runtime())
	ctx := fx.Context()

	src := keg.NodeId{ID: 20}
	dst := keg.NodeId{ID: 30}
	other := keg.NodeId{ID: 31}
	content := []byte("content")

	// prepare src node
	require.NoError(t, r.WriteContent(ctx, src, content))
	require.NoError(t, r.WriteMeta(ctx, src, []byte("title: src\n")))

	// moving to an unused dst should succeed
	require.NoError(t, r.MoveNode(ctx, src, dst))

	// src should no longer exist
	_, err := r.ReadContent(ctx, src)
	require.ErrorIs(t, err, keg.ErrNotExist)

	// dst should exist with same content
	got, err := r.ReadContent(ctx, dst)
	require.NoError(t, err)
	require.Equal(t, content, got, "moved content mismatch")

	// create another node at 'other' and attempt to move dst -> other to force destination-exists
	require.NoError(t, r.WriteContent(ctx, other, []byte("x")))
	require.NoError(t, r.WriteMeta(ctx, other, []byte("title: other\n")))

	err = r.MoveNode(ctx, dst, other)
	require.Error(t, err)
	require.ErrorIs(t, err, keg.ErrDestinationExists)
}

func TestMemoryRepo_NextProducesIncreasingIDs(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	r := keg.NewMemoryRepo(fx.Runtime())
	ctx := fx.Context()

	// Obtain the next available ID.
	first, err := r.Next(ctx)
	require.NoError(t, err)

	// Allocate the first ID by writing content to it so subsequent Next() reflects the updated state.
	require.NoError(t, r.WriteContent(ctx, first, []byte("first")))

	// Now Next should return a strictly larger id.
	second, err := r.Next(ctx)
	require.NoError(t, err)
	require.Greater(t, int(second.ID), int(first.ID), "expected second Next() > first Next()")

	// Write content at the second id and ensure the node exists afterwards.
	content := []byte("next-test")
	require.NoError(t, r.WriteContent(ctx, second, content))
	got, err := r.ReadContent(ctx, second)
	require.NoError(t, err)
	require.Equal(t, content, got, "content mismatch for Next id")

	// Ensure ListNodes includes the written IDs.
	ids, err := r.ListNodes(ctx)
	require.NoError(t, err)
	require.Contains(t, ids, first)
	require.Contains(t, ids, second)

	// sanity: ensure bytes.Equal works as expected for content comparisons used earlier
	require.True(t, bytes.Equal(content, got))
}

func TestMemoryRepo_WithNodeLockTimeout(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	r := keg.NewMemoryRepo(fx.Runtime())
	id := keg.NodeId{ID: 55}

	locked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- r.WithNodeLock(ctx, id, func(context.Context) error {
			close(locked)
			<-release
			return nil
		})
	}()

	<-locked

	lockCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	err := r.WithNodeLock(lockCtx, id, func(context.Context) error {
		return nil
	})
	require.Error(t, err)
	require.ErrorIs(t, err, keg.ErrLockTimeout)

	close(release)
	require.NoError(t, <-done)
}

func TestMemoryRepo_WithNodeLockReentrant(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	r := keg.NewMemoryRepo(fx.Runtime())
	id := keg.NodeId{ID: 56}

	err := r.WithNodeLock(ctx, id, func(lockCtx context.Context) error {
		return r.WithNodeLock(lockCtx, id, func(context.Context) error {
			return nil
		})
	})
	require.NoError(t, err)
}

// TestMemoryRepo_WithNodeLockContentionWakesWithoutWallClock verifies that a
// goroutine blocked on WithNodeLock acquires the lock as soon as the current
// holder releases it, with no dependence on a wall-clock retry interval. The
// test drives contention on a frozen sandbox clock so any reliance on
// time.Ticker / time.After would deadlock the waiter.
func TestMemoryRepo_WithNodeLockContentionWakesWithoutWallClock(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	r := keg.NewMemoryRepo(fx.Runtime())
	id := keg.NodeId{ID: 57}

	holderEntered := make(chan struct{})
	releaseHolder := make(chan struct{})
	holderDone := make(chan error, 1)

	go func() {
		holderDone <- r.WithNodeLock(ctx, id, func(context.Context) error {
			close(holderEntered)
			<-releaseHolder
			return nil
		})
	}()

	// Wait until the holder has acquired the lock before starting the waiter.
	select {
	case <-holderEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("holder never acquired the lock")
	}

	waiterAcquired := make(chan struct{})
	waiterDone := make(chan error, 1)
	go func() {
		waiterDone <- r.WithNodeLock(ctx, id, func(context.Context) error {
			close(waiterAcquired)
			return nil
		})
	}()

	// Give the waiter a moment to park on the release signal. This is purely
	// a scheduler yield; the waiter must not be able to acquire the lock
	// until the holder releases it.
	select {
	case <-waiterAcquired:
		t.Fatal("waiter acquired lock while holder still held it")
	case <-time.After(50 * time.Millisecond):
	}

	// Release the holder. The waiter should wake immediately via the
	// channel broadcast, with no dependence on a polling retry interval.
	close(releaseHolder)

	select {
	case <-waiterAcquired:
	case <-time.After(5 * time.Second):
		t.Fatal("waiter did not wake after holder released the lock")
	}

	require.NoError(t, <-holderDone)
	require.NoError(t, <-waiterDone)
}

// TestMemoryRepo_AcquireLockContentionWakesWithoutWallClock is the cross-lock
// analog of the WithNodeLock contention test above: a waiter blocked in
// AcquireLock must wake as soon as the current holder calls ReleaseLock, with
// no wall-clock retry interval involved.
func TestMemoryRepo_AcquireLockContentionWakesWithoutWallClock(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	r := keg.NewMemoryRepo(fx.Runtime())
	id := keg.NodeId{ID: 58}

	firstToken, err := r.AcquireLock(ctx, id)
	require.NoError(t, err)

	waiterAcquired := make(chan keg.LockToken, 1)
	waiterErr := make(chan error, 1)
	go func() {
		tok, err := r.AcquireLock(ctx, id)
		if err != nil {
			waiterErr <- err
			return
		}
		waiterAcquired <- tok
	}()

	// Waiter must not acquire while the first holder is still active.
	select {
	case tok := <-waiterAcquired:
		t.Fatalf("waiter acquired lock while first holder still held it: %q", tok)
	case err := <-waiterErr:
		t.Fatalf("waiter errored before release: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	require.NoError(t, r.ReleaseLock(ctx, id, firstToken))

	select {
	case tok := <-waiterAcquired:
		require.NotEqual(t, firstToken, tok, "waiter should receive a fresh token")
		require.NoError(t, r.ReleaseLock(ctx, id, tok))
	case err := <-waiterErr:
		t.Fatalf("waiter errored after release: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("waiter did not wake after first holder released")
	}
}
