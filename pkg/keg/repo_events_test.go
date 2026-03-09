package keg_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// ---------- MemoryRepo event tests ----------

func TestMemoryRepoEvents_WatchReceivesEmittedEvents(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	repo := keg.NewMemoryRepo(fx.Runtime())
	w := repo.WatchEvents()
	defer w.Close()

	ctx, cancel := context.WithTimeout(fx.Context(), 2*time.Second)
	defer cancel()

	ch, err := w.Watch(ctx, keg.NodeId{ID: 5})
	require.NoError(t, err)

	expected := keg.NodeEvent{
		Kind:   keg.NodeEventModified,
		NodeID: keg.NodeId{ID: 5},
		Field:  "content",
	}
	w.Emit(expected)

	select {
	case got := <-ch:
		require.Equal(t, expected.Kind, got.Kind)
		require.Equal(t, expected.NodeID, got.NodeID)
		require.Equal(t, expected.Field, got.Field)
	case <-ctx.Done():
		t.Fatal("timed out waiting for event")
	}
}

func TestMemoryRepoEvents_FilterByNodeID(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	repo := keg.NewMemoryRepo(fx.Runtime())
	w := repo.WatchEvents()
	defer w.Close()

	ctx, cancel := context.WithTimeout(fx.Context(), 2*time.Second)
	defer cancel()

	// Watch only node 3.
	ch, err := w.Watch(ctx, keg.NodeId{ID: 3})
	require.NoError(t, err)

	// Emit event for node 7 — should not be received.
	w.Emit(keg.NodeEvent{
		Kind:   keg.NodeEventCreated,
		NodeID: keg.NodeId{ID: 7},
		Field:  "content",
	})
	// Emit event for node 3 — should be received.
	w.Emit(keg.NodeEvent{
		Kind:   keg.NodeEventModified,
		NodeID: keg.NodeId{ID: 3},
		Field:  "meta",
	})

	select {
	case got := <-ch:
		require.Equal(t, keg.NodeId{ID: 3}, got.NodeID)
		require.Equal(t, "meta", got.Field)
	case <-ctx.Done():
		t.Fatal("timed out waiting for filtered event")
	}
}

func TestMemoryRepoEvents_WatchAllNodes(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	repo := keg.NewMemoryRepo(fx.Runtime())
	w := repo.WatchEvents()
	defer w.Close()

	ctx, cancel := context.WithTimeout(fx.Context(), 2*time.Second)
	defer cancel()

	// Watch all nodes (no IDs specified).
	ch, err := w.Watch(ctx)
	require.NoError(t, err)

	w.Emit(keg.NodeEvent{
		Kind:   keg.NodeEventCreated,
		NodeID: keg.NodeId{ID: 42},
		Field:  "content",
	})

	select {
	case got := <-ch:
		require.Equal(t, keg.NodeId{ID: 42}, got.NodeID)
	case <-ctx.Done():
		t.Fatal("timed out waiting for all-node event")
	}
}

func TestMemoryRepoEvents_CloseStopsDelivery(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	repo := keg.NewMemoryRepo(fx.Runtime())
	w := repo.WatchEvents()

	ctx, cancel := context.WithTimeout(fx.Context(), 2*time.Second)
	defer cancel()

	ch, err := w.Watch(ctx)
	require.NoError(t, err)

	require.NoError(t, w.Close())

	// Channel should be closed after Close.
	select {
	case _, ok := <-ch:
		require.False(t, ok, "channel should be closed after Close")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("channel was not closed after Close")
	}
}

func TestMemoryRepoEvents_ContextCancellation(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	repo := keg.NewMemoryRepo(fx.Runtime())
	w := repo.WatchEvents()
	defer w.Close()

	ctx, cancel := context.WithCancel(fx.Context())
	ch, err := w.Watch(ctx)
	require.NoError(t, err)

	cancel()

	// Channel should be closed after context cancellation.
	select {
	case _, ok := <-ch:
		require.False(t, ok, "channel should be closed after cancel")
	case <-time.After(500 * time.Millisecond):
		t.Fatal("channel was not closed after cancel")
	}
}

func TestMemoryRepoEvents_ReadContentEmitsAccessed(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	repo := keg.NewMemoryRepo(fx.Runtime())

	ctx := fx.Context()
	id := keg.NodeId{ID: 1}
	require.NoError(t, repo.WriteContent(ctx, id, []byte("# Hello\n")))

	w := repo.WatchEvents()
	defer w.Close()

	watchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	ch, err := w.Watch(watchCtx, id)
	require.NoError(t, err)

	// Reading content should emit an accessed event.
	_, readErr := repo.ReadContent(ctx, id)
	require.NoError(t, readErr)

	select {
	case got := <-ch:
		require.Equal(t, keg.NodeEventAccessed, got.Kind)
		require.Equal(t, id, got.NodeID)
		require.Equal(t, "content", got.Field)
	case <-watchCtx.Done():
		t.Fatal("timed out waiting for accessed event")
	}
}

// ---------- FsRepo event tests ----------

func TestFsRepoEvents_WatchDetectsContentChange(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, sandbox.WithFixture("example", "~/testrepo"))
	ctx, cancel := context.WithTimeout(fx.Context(), 5*time.Second)
	defer cancel()

	repo := keg.NewFsRepo("~/testrepo", fx.Runtime())
	w, err := repo.WatchEvents()
	require.NoError(t, err)
	defer w.Close()

	id := keg.NodeId{ID: 0}
	ch, watchErr := w.Watch(ctx, id)
	require.NoError(t, watchErr)

	// Modify the content file on disk using the real filesystem path.
	// ResolvePath(false) returns the virtual path; apply jail prefix to get
	// the real OS path that fsnotify watches.
	virtualRoot, err2 := fx.Runtime().ResolvePath("~/testrepo", false)
	require.NoError(t, err2)
	jail := fx.Runtime().GetJail()
	realRoot := filepath.Join(jail, strings.TrimPrefix(virtualRoot, string(filepath.Separator)))
	contentPath := filepath.Join(realRoot, id.Path(), "README.md")
	require.NoError(t, os.WriteFile(contentPath, []byte("# updated content\n"), 0o644))

	// Wait for a debounced event.
	select {
	case ev := <-ch:
		require.Equal(t, id, ev.NodeID)
		require.Equal(t, "content", ev.Field)
	case <-ctx.Done():
		t.Fatal("timed out waiting for fs content change event")
	}
}

func TestFsRepoEvents_CloseStopsWatcher(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, sandbox.WithFixture("example", "~/testrepo"))
	ctx, cancel := context.WithTimeout(fx.Context(), 2*time.Second)
	defer cancel()

	repo := keg.NewFsRepo("~/testrepo", fx.Runtime())
	w, err := repo.WatchEvents()
	require.NoError(t, err)

	ch, watchErr := w.Watch(ctx, keg.NodeId{ID: 0})
	require.NoError(t, watchErr)

	require.NoError(t, w.Close())

	// Channel should eventually close.
	select {
	case _, ok := <-ch:
		require.False(t, ok, "channel should be closed after Close")
	case <-time.After(1 * time.Second):
		t.Fatal("channel was not closed after Close")
	}
}

// ---------- NodeEventKind.String test ----------

func TestNodeEventKind_String(t *testing.T) {
	t.Parallel()
	require.Equal(t, "created", keg.NodeEventCreated.String())
	require.Equal(t, "modified", keg.NodeEventModified.String())
	require.Equal(t, "deleted", keg.NodeEventDeleted.String())
	require.Equal(t, "accessed", keg.NodeEventAccessed.String())
	require.Equal(t, "unknown", keg.NodeEventKind(0).String())
}
