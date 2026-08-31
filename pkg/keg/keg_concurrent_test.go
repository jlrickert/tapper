package keg_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// TestConcurrentCreate_UniqueIDs verifies that 20 goroutines creating nodes
// concurrently through one repository all get unique IDs.
func TestConcurrentCreate_UniqueIDs(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	const N = 20
	ids := make([]kegpkg.NodeId, N)
	errs := make([]error, N)

	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
				Title: fmt.Sprintf("Node %d", idx),
			})
			ids[idx] = id.ID
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d failed to create", i)
	}

	seen := make(map[int]bool)
	for i, id := range ids {
		require.False(t, seen[id.ID], "duplicate ID %d from goroutine %d", id.ID, i)
		seen[id.ID] = true
	}
}

// TestConcurrentCreate_MemoryRepository verifies that 10 goroutines creating nodes
// concurrently via MemoryRepository sandbox all get unique IDs.
func TestConcurrentCreate_MemoryRepository(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repo"), f.Runtime())
	require.NoError(t, err)
	initNonStrictTestKeg(t, k, f.Context())

	const N = 10
	ids := make([]kegpkg.NodeId, N)
	errs := make([]error, N)

	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
				Title: fmt.Sprintf("FsNode %d", idx),
			})
			ids[idx] = id.ID
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d failed to create", i)
	}

	seen := make(map[int]bool)
	for i, id := range ids {
		require.False(t, seen[id.ID], "duplicate ID %d from goroutine %d", id.ID, i)
		seen[id.ID] = true
	}
}

// TestConcurrentSetContent_DifferentNodes verifies that N goroutines writing
// to distinct nodes don't cross-contaminate content.
func TestConcurrentSetContent_DifferentNodes(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	const N = 10
	ids := make([]kegpkg.NodeId, N)
	for i := range N {
		id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
			Title: fmt.Sprintf("Node %d", i),
		})
		require.NoError(t, err)
		ids[i] = id.ID
	}

	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			content := fmt.Sprintf("# Updated Node %d\n\nContent for node %d.\n", idx, idx)
			errs[idx] = k.SetContent(f.Context(), ids[idx], []byte(content))
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d failed SetContent", i)
	}

	// Verify each node has its own content.
	for i, id := range ids {
		data, err := k.GetContent(f.Context(), id)
		require.NoError(t, err)
		expected := fmt.Sprintf("# Updated Node %d\n\nContent for node %d.\n", i, i)
		require.Equal(t, expected, string(data), "node %d has wrong content", i)
	}
}

// TestConcurrentSetContent_SameNode verifies that 5 goroutines writing to the
// same node are serialized by the lock and one of them wins.
func TestConcurrentSetContent_SameNode(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Shared"})
	require.NoError(t, err)

	const N = 5
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			content := fmt.Sprintf("# Writer %d\n\nContent from writer %d.\n", idx, idx)
			errs[idx] = k.SetContent(f.Context(), id.ID, []byte(content))
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d failed SetContent", i)
	}

	// One of the writers should have won — content should match one of them.
	data, err := k.GetContent(f.Context(), id.ID)
	require.NoError(t, err)
	require.Contains(t, string(data), "# Writer")
}

// TestConcurrentSetMeta_SameNode verifies that concurrent SetMeta calls on
// the same node are serialized.
func TestConcurrentSetMeta_SameNode(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Shared Meta"})
	require.NoError(t, err)

	const N = 5
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = k.UpdateMeta(f.Context(), id.ID, func(m *kegpkg.NodeMeta) {
				m.SetTags([]string{fmt.Sprintf("tag%d", idx)})
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d failed UpdateMeta", i)
	}
}

// TestConcurrentCreateAndEdit runs a mixed workload of creates and edits
// concurrently and verifies the keg is left in a consistent state.
func TestConcurrentCreateAndEdit(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	// Pre-create some nodes for editing.
	const preCreated = 5
	preIDs := make([]kegpkg.NodeId, preCreated)
	for i := range preCreated {
		id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
			Title: fmt.Sprintf("Pre %d", i),
		})
		require.NoError(t, err)
		preIDs[i] = id.ID
	}

	const creators = 5
	const editors = 5
	var wg sync.WaitGroup
	createErrs := make([]error, creators)
	editErrs := make([]error, editors)

	// Creators.
	for i := range creators {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := k.Create(f.Context(), &kegpkg.CreateOptions{
				Title: fmt.Sprintf("Created %d", idx),
			})
			createErrs[idx] = err
		}(i)
	}

	// Editors.
	for i := range editors {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			content := fmt.Sprintf("# Edited Pre %d\n\nEdited content.\n", idx)
			editErrs[idx] = k.SetContent(f.Context(), preIDs[idx], []byte(content))
		}(i)
	}

	wg.Wait()

	for i, err := range createErrs {
		require.NoError(t, err, "creator %d failed", i)
	}
	for i, err := range editErrs {
		require.NoError(t, err, "editor %d failed", i)
	}

	// Verify total node count: zero node + preCreated + creators.
	ids, err := k.Repo.ListNodes(f.Context())
	require.NoError(t, err)
	require.Equal(t, 1+preCreated+creators, len(ids))
}

// TestTwoKegInstances_DexNotOverwritten verifies that two Keg instances
// sharing the same MemoryRepository do not overwrite each other's dex entries.
// Reproduction test for bug 327/328 (stale dex cache in MCP server).
func TestTwoKegInstances_DexNotOverwritten(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())

	// Create two Keg instances sharing the same repo (simulates MCP server + CLI)
	k1 := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k1, f.Context())

	k2 := kegpkg.NewLocalKeg(repo, f.Runtime())

	// k1 creates node 1
	id1, err := k1.Create(f.Context(), &kegpkg.CreateOptions{Title: "From K1"})
	require.NoError(t, err)
	require.Equal(t, 1, id1.ID.ID)

	// k2 creates node 2 -- its dex should include node 1 from k1
	id2, err := k2.Create(f.Context(), &kegpkg.CreateOptions{Title: "From K2"})
	require.NoError(t, err)
	require.Equal(t, 2, id2.ID.ID)

	// Now load the dex fresh from the repo to verify both nodes are present
	dex, err := kegpkg.NewDexFromRepo(f.Context(), repo)
	require.NoError(t, err)

	ref1 := dex.GetRef(f.Context(), id1.ID)
	require.NotNil(t, ref1, "node 1 (created by k1) should be in the dex")

	ref2 := dex.GetRef(f.Context(), id2.ID)
	require.NotNil(t, ref2, "node 2 (created by k2) should be in the dex")
}

func TestConcurrentCrossLock_OnlyOneWins(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Lock Race"})
	require.NoError(t, err)

	const N = 10
	tokens := make([]kegpkg.LockToken, N)
	errs := make([]error, N)

	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(f.Context(), 300*time.Millisecond)
			defer cancel()
			tokens[idx], errs[idx] = repo.AcquireLock(ctx, id.ID)
		}(i)
	}
	wg.Wait()

	winners := 0
	for i := range N {
		if errs[i] == nil {
			winners++
			require.NotEmpty(t, tokens[i])
		}
	}
	require.Equal(t, 1, winners, "exactly one goroutine should acquire the lock")
}

// TestCrossLock_DoesNotBlockWithNodeLock verifies that holding a cross-process
// lock does not prevent WithNodeLock (process-scoped) from succeeding.
func TestCrossLock_DoesNotBlockWithNodeLock(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Lock Independence"})
	require.NoError(t, err)

	// Acquire cross-process lock.
	token, err := repo.AcquireLock(f.Context(), id.ID)
	require.NoError(t, err)

	// WithNodeLock should still succeed — they're independent.
	err = repo.WithNodeLock(f.Context(), id.ID, func(ctx context.Context) error {
		return k.SetContent(ctx, id.ID, []byte("# Written under process lock\n"))
	})
	require.NoError(t, err)

	// Cross-process lock still held.
	info, err := repo.LockStatus(f.Context(), id.ID)
	require.NoError(t, err)
	require.Equal(t, token, info.Token)

	// Release cross-process lock.
	require.NoError(t, repo.ReleaseLock(f.Context(), id.ID, token))
}

// TestConcurrentRemoveThenSetContent_RaceCondition runs Remove and
// SetContent concurrently to verify the node lock serializes them and
// prevents resurrection.
func TestConcurrentRemoveThenSetContent_RaceCondition(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Race Node"})
	require.NoError(t, err)

	var wg sync.WaitGroup
	var removeErr, setErr error

	wg.Add(2)
	go func() {
		defer wg.Done()
		_, removeErr = k.Remove(f.Context(), removeOptions(t, f.Context(), k, id.ID))
	}()
	go func() {
		defer wg.Done()
		setErr = k.SetContent(f.Context(), id.ID, []byte("# Updated\n"))
	}()
	wg.Wait()

	// One of two outcomes is valid:
	// 1) Remove runs first: SetContent gets ErrNotExist
	// 2) SetContent runs first: Remove succeeds, SetContent succeeded
	//
	// In neither case should the node be resurrected after Remove completes.
	if removeErr == nil {
		// Remove succeeded. SetContent either succeeded (ran first) or
		// failed because the node disappeared or its precondition became stale.
		if setErr != nil {
			require.True(t, errors.Is(setErr, kegpkg.ErrNotExist) || errors.Is(setErr, kegpkg.ErrConflict), setErr)
		}
	} else {
		// A concurrent write may make the remove precondition stale.
		require.True(t, errors.Is(removeErr, kegpkg.ErrNotExist) || errors.Is(removeErr, kegpkg.ErrConflict), removeErr)
	}

	// After everything settles, if the node exists it should have valid content.
	exists, err := repo.HasNode(f.Context(), id.ID)
	require.NoError(t, err)
	if !exists {
		// Node was removed — verify it has no residual directory.
		return
	}

	// If node still exists, it should have valid content (SetContent won the race).
	content, err := repo.ReadContent(f.Context(), id.ID)
	require.NoError(t, err)
	require.NotNil(t, content, "surviving node should have content")
}

// TestConcurrentRemoveDuringSetContent_MemoryRepository verifies the same
// anti-resurrection behavior on the filesystem-backed repository.
func TestConcurrentRemoveDuringSetContent_MemoryRepository(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repo"), f.Runtime())
	require.NoError(t, err)
	initNonStrictTestKeg(t, k, f.Context())

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "FsDoomed"})
	require.NoError(t, err)

	// Remove the node.
	require.NoError(t, errOnly(k.Remove(f.Context(), removeOptions(t, f.Context(), k, id.ID))))

	// SetContent after removal should fail with ErrNotExist.
	err = k.SetContent(f.Context(), id.ID, []byte("# FsResurrected\n"))
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)

	// WithNodeLock creates a bare directory as a lock artifact, but it
	// should be cleaned up after the lock callback returns with no content
	// file present. Verify no orphaned directory remains.
	exists, err := k.(*kegpkg.LocalKeg).Repo.HasNode(f.Context(), id.ID)
	require.NoError(t, err)
	require.False(t, exists, "bare directory should be cleaned up after failed write")
}

// TestConcurrentRemoveDuringSetMeta_MemoryRepository verifies anti-resurrection for
// SetMeta on the filesystem-backed repository.
func TestConcurrentRemoveDuringSetMeta_MemoryRepository(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo_meta"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repo_meta"), f.Runtime())
	require.NoError(t, err)
	initNonStrictTestKeg(t, k, f.Context())

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
		Title: "FsMetaDoomed",
		Tags:  []string{"victim"},
	})
	require.NoError(t, err)

	meta, err := k.GetMeta(f.Context(), id.ID)
	require.NoError(t, err)

	require.NoError(t, errOnly(k.Remove(f.Context(), removeOptions(t, f.Context(), k, id.ID))))

	meta.SetTags([]string{"ghost"})
	err = k.SetMeta(f.Context(), id.ID, meta)
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)

	// Verify no orphaned directory was left behind on disk.
	exists, err := k.(*kegpkg.LocalKeg).Repo.HasNode(f.Context(), id.ID)
	require.NoError(t, err)
	require.False(t, exists, "bare directory should be cleaned up after failed SetMeta")
}

func TestConcurrentRemoveDuringTouch_MemoryRepository(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repo"), f.Runtime())
	require.NoError(t, err)
	initNonStrictTestKeg(t, k, f.Context())

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "FsTouchDoomed"})
	require.NoError(t, err)

	require.NoError(t, errOnly(k.Remove(f.Context(), removeOptions(t, f.Context(), k, id.ID))))

	err = k.Touch(f.Context(), id.ID)
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)

	exists, err := k.(*kegpkg.LocalKeg).Repo.HasNode(f.Context(), id.ID)
	require.NoError(t, err)
	require.False(t, exists, "bare directory should be cleaned up after failed Touch")
}

// TestConcurrentRemoveDuringUpdateMeta_MemoryRepository verifies that UpdateMeta on a
// removed node returns ErrNotExist on the filesystem-backed repository.
func TestConcurrentRemoveDuringUpdateMeta_MemoryRepository(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repo"), f.Runtime())
	require.NoError(t, err)
	initNonStrictTestKeg(t, k, f.Context())

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "FsUpdateDoomed"})
	require.NoError(t, err)

	require.NoError(t, errOnly(k.Remove(f.Context(), removeOptions(t, f.Context(), k, id.ID))))

	err = k.(*kegpkg.LocalKeg).UpdateMeta(f.Context(), id.ID, func(m *kegpkg.NodeMeta) {
		m.SetTags([]string{"ghost"})
	})
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)

	exists, err := k.(*kegpkg.LocalKeg).Repo.HasNode(f.Context(), id.ID)
	require.NoError(t, err)
	require.False(t, exists, "bare directory should be cleaned up after failed UpdateMeta")
}
