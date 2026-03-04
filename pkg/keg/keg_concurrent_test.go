package keg_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/stretchr/testify/require"
)

// TestConcurrentCreate_UniqueIDs verifies that 20 goroutines creating nodes
// concurrently via MemoryRepo all get unique IDs.
func TestConcurrentCreate_UniqueIDs(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewKeg(repo, f.Runtime())
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

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegurl.NewFile("repo"), f.Runtime())
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
	k := kegpkg.NewKeg(repo, f.Runtime())
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
	k := kegpkg.NewKeg(repo, f.Runtime())
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
	k := kegpkg.NewKeg(repo, f.Runtime())
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
	k := kegpkg.NewKeg(repo, f.Runtime())
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

// TestWithNodeLock_StaleLockRecovery writes a fake lock file with a dead PID
// and verifies that the lock is acquired after stale detection removes it.
func TestWithNodeLock_StaleLockRecovery(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegurl.NewFile("repo"), f.Runtime())
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
