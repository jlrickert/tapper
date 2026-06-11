package keg_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// TestConcurrentCreate_UniqueIDs verifies that 20 goroutines creating nodes
// concurrently via MemoryRepo all get unique IDs.
func TestConcurrentCreate_UniqueIDs(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

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
			ids[idx] = id
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

// TestConcurrentCreate_FsRepo verifies that 10 goroutines creating nodes
// concurrently via FsRepo sandbox all get unique IDs.
func TestConcurrentCreate_FsRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repo"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

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
			ids[idx] = id
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

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	const N = 10
	ids := make([]kegpkg.NodeId, N)
	for i := range N {
		id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
			Title: fmt.Sprintf("Node %d", i),
		})
		require.NoError(t, err)
		ids[i] = id
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

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

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
			errs[idx] = k.SetContent(f.Context(), id, []byte(content))
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d failed SetContent", i)
	}

	// One of the writers should have won — content should match one of them.
	data, err := k.GetContent(f.Context(), id)
	require.NoError(t, err)
	require.Contains(t, string(data), "# Writer")
}

// TestConcurrentSetMeta_SameNode verifies that concurrent SetMeta calls on
// the same node are serialized.
func TestConcurrentSetMeta_SameNode(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Shared Meta"})
	require.NoError(t, err)

	const N = 5
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = k.UpdateMeta(f.Context(), id, func(m *kegpkg.NodeMeta) {
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

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	// Pre-create some nodes for editing.
	const preCreated = 5
	preIDs := make([]kegpkg.NodeId, preCreated)
	for i := range preCreated {
		id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
			Title: fmt.Sprintf("Pre %d", i),
		})
		require.NoError(t, err)
		preIDs[i] = id
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
// sharing the same MemoryRepo do not overwrite each other's dex entries.
// Reproduction test for bug 327/328 (stale dex cache in MCP server).
func TestTwoKegInstances_DexNotOverwritten(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())

	// Create two Keg instances sharing the same repo (simulates MCP server + CLI)
	k1 := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k1.Init(f.Context()))

	k2 := kegpkg.NewLocalKeg(repo, f.Runtime())

	// k1 creates node 1
	id1, err := k1.Create(f.Context(), &kegpkg.CreateOptions{Title: "From K1"})
	require.NoError(t, err)
	require.Equal(t, 1, id1.ID)

	// k2 creates node 2 -- its dex should include node 1 from k1
	id2, err := k2.Create(f.Context(), &kegpkg.CreateOptions{Title: "From K2"})
	require.NoError(t, err)
	require.Equal(t, 2, id2.ID)

	// Now load the dex fresh from the repo to verify both nodes are present
	dex, err := kegpkg.NewDexFromRepo(f.Context(), repo)
	require.NoError(t, err)

	ref1 := dex.GetRef(f.Context(), id1)
	require.NotNil(t, ref1, "node 1 (created by k1) should be in the dex")

	ref2 := dex.GetRef(f.Context(), id2)
	require.NotNil(t, ref2, "node 2 (created by k2) should be in the dex")
}

// TestWithNodeLock_StaleLockRecovery writes a fake lock file with a dead PID
// and verifies that the lock is acquired after stale detection removes it.
func TestWithNodeLock_StaleLockRecovery(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repo"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Locked Node"})
	require.NoError(t, err)

	// Simulate a stale lock: create the lock directory with owner.json
	// containing a PID that doesn't exist (use a very high PID).
	nodeDir := filepath.Join("repo", id.Path())
	lockDir := filepath.Join(nodeDir, ".keg-lock")
	require.NoError(t, f.Runtime().Mkdir(lockDir, 0o700, false))

	staleLock := struct {
		PID       int    `json:"pid"`
		Hostname  string `json:"hostname"`
		StartedAt string `json:"started_at"`
		UID       string `json:"uid"`
	}{
		PID:       999999999, // Very unlikely to be alive.
		Hostname:  "testhost",
		StartedAt: "2025-01-01T00:00:00Z",
		UID:       "stale-uid",
	}
	data, err := json.Marshal(staleLock)
	require.NoError(t, err)
	ownerPath := filepath.Join(lockDir, "owner.json")
	require.NoError(t, f.Runtime().WriteFile(ownerPath, data, 0o644))

	// Now attempt a lock operation — it should detect the stale lock and succeed.
	err = k.SetContent(f.Context(), id, []byte("# Updated after stale lock\n"))
	require.NoError(t, err, "SetContent should succeed after stale lock recovery")

	// Verify content was updated.
	content, err := k.GetContent(f.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "# Updated after stale lock\n", string(content))
}

// TestConcurrentCrossLock_OnlyOneWins verifies that concurrent AcquireLock
// calls on the same node result in exactly one winner, with the rest timing out.
func TestConcurrentCrossLock_OnlyOneWins(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

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
			tokens[idx], errs[idx] = repo.AcquireLock(ctx, id)
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

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Lock Independence"})
	require.NoError(t, err)

	// Acquire cross-process lock.
	token, err := repo.AcquireLock(f.Context(), id)
	require.NoError(t, err)

	// WithNodeLock should still succeed — they're independent.
	err = repo.WithNodeLock(f.Context(), id, func(ctx context.Context) error {
		return k.SetContent(ctx, id, []byte("# Written under process lock\n"))
	})
	require.NoError(t, err)

	// Cross-process lock still held.
	info, err := repo.LockStatus(f.Context(), id)
	require.NoError(t, err)
	require.Equal(t, token, info.Token)

	// Release cross-process lock.
	require.NoError(t, repo.ReleaseLock(f.Context(), id, token))
}

// TestConcurrentRemoveDuringSetContent_MemoryRepo verifies that if a node is
// removed while SetContent is about to write, SetContent returns ErrNotExist
// and does not resurrect the node. This is a regression test for issue 325.
func TestConcurrentRemoveDuringSetContent_MemoryRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Doomed"})
	require.NoError(t, err)

	// Remove the node.
	require.NoError(t, errOnly(k.Remove(f.Context(), id)))

	// SetContent after removal should fail with ErrNotExist.
	err = k.SetContent(f.Context(), id, []byte("# Resurrected\n"))
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)

	// Verify the node was not resurrected.
	exists, err := repo.HasNode(f.Context(), id)
	require.NoError(t, err)
	require.False(t, exists, "node should not be resurrected after removal")
}

// TestConcurrentRemoveDuringSetMeta_MemoryRepo verifies that SetMeta on a
// removed node returns ErrNotExist and does not resurrect it.
func TestConcurrentRemoveDuringSetMeta_MemoryRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
		Title: "Meta Doomed",
		Tags:  []string{"victim"},
	})
	require.NoError(t, err)

	// Read meta before removal.
	meta, err := k.GetMeta(f.Context(), id)
	require.NoError(t, err)

	// Remove the node.
	require.NoError(t, errOnly(k.Remove(f.Context(), id)))

	// SetMeta after removal should fail with ErrNotExist.
	meta.SetTags([]string{"ghost"})
	err = k.SetMeta(f.Context(), id, meta)
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)

	// Verify the node was not resurrected.
	exists, err := repo.HasNode(f.Context(), id)
	require.NoError(t, err)
	require.False(t, exists, "node should not be resurrected by SetMeta")
}

// TestConcurrentRemoveDuringUpdateMeta_MemoryRepo verifies that UpdateMeta
// on a removed node returns ErrNotExist.
func TestConcurrentRemoveDuringUpdateMeta_MemoryRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Update Doomed"})
	require.NoError(t, err)

	// Remove the node.
	require.NoError(t, errOnly(k.Remove(f.Context(), id)))

	// UpdateMeta after removal should fail with ErrNotExist.
	err = k.UpdateMeta(f.Context(), id, func(m *kegpkg.NodeMeta) {
		m.SetTags([]string{"ghost"})
	})
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)

	// Verify the node was not resurrected.
	exists, err := repo.HasNode(f.Context(), id)
	require.NoError(t, err)
	require.False(t, exists, "node should not be resurrected by UpdateMeta")
}

// TestConcurrentRemoveThenSetContent_RaceCondition runs Remove and
// SetContent concurrently to verify the node lock serializes them and
// prevents resurrection.
func TestConcurrentRemoveThenSetContent_RaceCondition(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Race Node"})
	require.NoError(t, err)

	var wg sync.WaitGroup
	var removeErr, setErr error

	wg.Add(2)
	go func() {
		defer wg.Done()
		_, removeErr = k.Remove(f.Context(), id)
	}()
	go func() {
		defer wg.Done()
		setErr = k.SetContent(f.Context(), id, []byte("# Updated\n"))
	}()
	wg.Wait()

	// One of two outcomes is valid:
	// 1) Remove runs first: SetContent gets ErrNotExist
	// 2) SetContent runs first: Remove succeeds, SetContent succeeded
	//
	// In neither case should the node be resurrected after Remove completes.
	if removeErr == nil {
		// Remove succeeded. SetContent either succeeded (ran first) or
		// failed with ErrNotExist (ran second).
		if setErr != nil {
			require.ErrorIs(t, setErr, kegpkg.ErrNotExist)
		}
	} else {
		// Remove failed (e.g., SetContent removed the lock dir). Either
		// way the node should not be in a resurrected broken state.
		require.ErrorIs(t, removeErr, kegpkg.ErrNotExist)
	}

	// After everything settles, if the node exists it should have valid content.
	exists, err := repo.HasNode(f.Context(), id)
	require.NoError(t, err)
	if !exists {
		// Node was removed — verify it has no residual directory.
		return
	}

	// If node still exists, it should have valid content (SetContent won the race).
	content, err := repo.ReadContent(f.Context(), id)
	require.NoError(t, err)
	require.NotNil(t, content, "surviving node should have content")
}

// TestConcurrentRemoveDuringSetContent_FsRepo verifies the same
// anti-resurrection behavior on the filesystem-backed repository.
func TestConcurrentRemoveDuringSetContent_FsRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repo"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "FsDoomed"})
	require.NoError(t, err)

	// Remove the node.
	require.NoError(t, errOnly(k.Remove(f.Context(), id)))

	// SetContent after removal should fail with ErrNotExist.
	err = k.SetContent(f.Context(), id, []byte("# FsResurrected\n"))
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)

	// WithNodeLock creates a bare directory as a lock artifact, but it
	// should be cleaned up after the lock callback returns with no content
	// file present. Verify no orphaned directory remains.
	exists, err := k.(*kegpkg.LocalKeg).Repo.HasNode(f.Context(), id)
	require.NoError(t, err)
	require.False(t, exists, "bare directory should be cleaned up after failed write")
}

// TestConcurrentRemoveDuringSetMeta_FsRepo verifies anti-resurrection for
// SetMeta on the filesystem-backed repository.
func TestConcurrentRemoveDuringSetMeta_FsRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo_meta"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repo_meta"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
		Title: "FsMetaDoomed",
		Tags:  []string{"victim"},
	})
	require.NoError(t, err)

	meta, err := k.GetMeta(f.Context(), id)
	require.NoError(t, err)

	require.NoError(t, errOnly(k.Remove(f.Context(), id)))

	meta.SetTags([]string{"ghost"})
	err = k.SetMeta(f.Context(), id, meta)
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)

	// Verify no orphaned directory was left behind on disk.
	exists, err := k.(*kegpkg.LocalKeg).Repo.HasNode(f.Context(), id)
	require.NoError(t, err)
	require.False(t, exists, "bare directory should be cleaned up after failed SetMeta")
}

// TestSetContent_NoOrphanedDirectoryOnRemovedNode verifies that after
// SetContent returns ErrNotExist for a removed node, no empty node directory
// is left behind on disk. This is a defense-in-depth check ensuring the
// WithNodeLock cleanup and WriteContent existence check cooperate to prevent
// orphaned artifacts.
func TestSetContent_NoOrphanedDirectoryOnRemovedNode(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repo"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Ephemeral"})
	require.NoError(t, err)

	// Verify node directory exists before removal.
	nodeDir := filepath.Join("repo", id.Path())
	_, statErr := f.Runtime().Stat(nodeDir, false)
	require.NoError(t, statErr, "node directory should exist after Create")

	// Remove the node.
	require.NoError(t, errOnly(k.Remove(f.Context(), id)))

	// Verify directory was removed.
	_, statErr = f.Runtime().Stat(nodeDir, false)
	require.Error(t, statErr, "node directory should not exist after Remove")

	// Attempt SetContent — should fail with ErrNotExist.
	err = k.SetContent(f.Context(), id, []byte("# Ghost content\n"))
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)

	// Verify no orphaned empty directory was left behind. WithNodeLock
	// creates the directory as a lock artifact and should clean it up
	// when the lock callback returns without creating a content file.
	_, statErr = f.Runtime().Stat(nodeDir, false)
	require.Error(t, statErr, "no orphaned directory should remain after failed SetContent")

	// Also verify via HasNode for consistency.
	exists, err := k.(*kegpkg.LocalKeg).Repo.HasNode(f.Context(), id)
	require.NoError(t, err)
	require.False(t, exists, "HasNode should return false — no resurrection")
}

// TestConcurrentRemoveDuringTouch_MemoryRepo verifies that Touch on a removed
// node returns ErrNotExist and does not resurrect the node.
func TestConcurrentRemoveDuringTouch_MemoryRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Touch Doomed"})
	require.NoError(t, err)

	require.NoError(t, errOnly(k.Remove(f.Context(), id)))

	err = k.Touch(f.Context(), id)
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)

	exists, err := repo.HasNode(f.Context(), id)
	require.NoError(t, err)
	require.False(t, exists, "node should not be resurrected by Touch")
}

// TestConcurrentRemoveDuringTouch_FsRepo verifies that Touch on a removed
// node returns ErrNotExist on the filesystem-backed repository.
func TestConcurrentRemoveDuringTouch_FsRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repo"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "FsTouchDoomed"})
	require.NoError(t, err)

	require.NoError(t, errOnly(k.Remove(f.Context(), id)))

	err = k.Touch(f.Context(), id)
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)

	exists, err := k.(*kegpkg.LocalKeg).Repo.HasNode(f.Context(), id)
	require.NoError(t, err)
	require.False(t, exists, "bare directory should be cleaned up after failed Touch")
}

// TestConcurrentRemoveDuringUpdateMeta_FsRepo verifies that UpdateMeta on a
// removed node returns ErrNotExist on the filesystem-backed repository.
func TestConcurrentRemoveDuringUpdateMeta_FsRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repo"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "FsUpdateDoomed"})
	require.NoError(t, err)

	require.NoError(t, errOnly(k.Remove(f.Context(), id)))

	err = k.(*kegpkg.LocalKeg).UpdateMeta(f.Context(), id, func(m *kegpkg.NodeMeta) {
		m.SetTags([]string{"ghost"})
	})
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)

	exists, err := k.(*kegpkg.LocalKeg).Repo.HasNode(f.Context(), id)
	require.NoError(t, err)
	require.False(t, exists, "bare directory should be cleaned up after failed UpdateMeta")
}
