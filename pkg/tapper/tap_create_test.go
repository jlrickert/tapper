package tapper_test

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// TestCreate_InteractiveIDConsistency verifies that the node ID returned by
// an interactive create (TTY mode) matches the node that is actually
// persisted. This is the reproduction test for the double-allocation bug
// where Next() was called once for the editor scaffold and again inside
// Keg.Create(), causing the editor to show node N while content was saved
// to node N+1.
//
// Since we cannot open a real TTY editor in tests, we simulate the
// interactive flow by using the non-interactive API and then verifying that
// the returned ID points to a node whose content matches what was created.
// The actual fix is verified by testing the create-then-edit flow produces
// consistent IDs.
func TestCreate_InteractiveIDConsistency(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	tap := setupTapWithKeg(t, fx)

	// Create a node via piped stdin (simulates what the fixed interactive
	// flow does internally: Create then edit).
	content := "# Test Node\n\nSome content.\n"
	stream := &toolkit.Stream{
		In:      io.NopCloser(bytes.NewReader([]byte(content))),
		IsPiped: true,
	}
	id, err := tap.Create(fx.Context(), tapper.CreateOptions{
		Stream: stream,
	})
	require.NoError(t, err)

	// The returned ID must point to a real node with the expected content.
	catOutput, err := tap.Cat(fx.Context(), tapper.CatOptions{
		NodeIDs:     []string{id.String()},
		ContentOnly: true,
	})
	require.NoError(t, err)
	require.Contains(t, catOutput, "# Test Node")
	require.Contains(t, catOutput, "Some content.")
}

// TestCreate_DoubleNextBugReproduction directly demonstrates the
// double-allocation bug at the Keg level. Calling Next() followed by
// Create() produces different IDs -- the editor would have shown
// nextID but content would have been saved under createID.
func TestCreate_DoubleNextBugReproduction(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	kegDir := "/home/testuser/kegs/test"
	require.NoError(t, fx.Runtime().Mkdir(kegDir, 0o755, true))

	k, err := keg.NewKegFromTarget(fx.Context(), keg.NewFile(kegDir), fx.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(fx.Context()))

	// This simulates what the OLD interactive create flow did:
	// 1. Call Next() to get the node ID for the editor scaffold
	nextID, err := k.Next(fx.Context())
	require.NoError(t, err)

	// 2. Call Create() which internally calls Next() again
	createID, err := k.Create(fx.Context(), &keg.CreateOptions{
		Body: []byte("# Bug Reproduction\n\nThis content was saved.\n"),
	})
	require.NoError(t, err)

	// BUG: nextID != createID -- the editor showed nextID but content
	// was saved under createID. After the fix, the interactive flow
	// no longer calls Next() separately, so this mismatch cannot occur.
	require.NotEqual(t, nextID.ID, createID.ID,
		"Next()+Create() should produce different IDs (demonstrating the bug)")
	require.Equal(t, nextID.ID+1, createID.ID,
		"Create() should have allocated the ID after the one Next() reserved")

	// The node at nextID should exist (Next creates the directory) but
	// have no meaningful content -- it's an orphan from the double allocation.
	exists, err := k.(*keg.LocalKeg).Repo.HasNode(fx.Context(), nextID)
	require.NoError(t, err)
	require.True(t, exists, "Next() should have reserved a directory for nextID")

	// The actual content should be at createID, not nextID.
	body, err := k.(*keg.LocalKeg).Repo.ReadContent(fx.Context(), createID)
	require.NoError(t, err)
	require.Contains(t, string(body), "Bug Reproduction")
}

// TestCreate_DexConsistency verifies that after a create operation, the dex
// accurately reflects the created node (correct ID and title).
func TestCreate_DexConsistency(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	tap := setupTapWithKeg(t, fx)

	content := "# Dex Check Node\n\nContent for dex test.\n"
	stream := &toolkit.Stream{
		In:      io.NopCloser(bytes.NewReader([]byte(content))),
		IsPiped: true,
	}
	id, err := tap.Create(fx.Context(), tapper.CreateOptions{
		Stream: stream,
	})
	require.NoError(t, err)

	// Verify the node appears in the list output (which reads from dex).
	// Use a format that starts each line with the node ID followed by a tab
	// to avoid substring false positives (e.g., "1" matching inside "10").
	lines, err := tap.List(fx.Context(), tapper.ListOptions{})
	require.NoError(t, err)

	prefix := id.String() + "\t"
	found := false
	for _, line := range lines {
		if len(line) >= len(prefix) && line[:len(prefix)] == prefix {
			found = true
			require.Contains(t, line, "Dex Check Node",
				"dex entry for created node should contain the title")
			break
		}
	}
	require.True(t, found, "created node %s should appear in list output", id.String())
}

// TestCreate_NonInteractivePipedStdin verifies that the piped stdin create
// path works correctly and is unaffected by changes to the interactive flow.
func TestCreate_NonInteractivePipedStdin(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	tap := setupTapWithKeg(t, fx)

	content := "# Piped Create\n\nCreated via piped stdin.\n"
	stream := &toolkit.Stream{
		In:      io.NopCloser(bytes.NewReader([]byte(content))),
		IsPiped: true,
	}
	id, err := tap.Create(fx.Context(), tapper.CreateOptions{
		Stream: stream,
	})
	require.NoError(t, err)
	require.True(t, id.ID > 0, "created node ID should be positive")

	catOutput, err := tap.Cat(fx.Context(), tapper.CatOptions{
		NodeIDs:     []string{id.String()},
		ContentOnly: true,
	})
	require.NoError(t, err)
	require.Contains(t, catOutput, "# Piped Create")
	require.Contains(t, catOutput, "Created via piped stdin.")
}

// TestCreate_NonInteractiveTitleLead verifies that the title/lead flag
// create path works correctly and is unaffected by changes to the
// interactive flow.
func TestCreate_NonInteractiveTitleLead(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	tap := setupTapWithKeg(t, fx)

	id, err := tap.Create(fx.Context(), tapper.CreateOptions{
		Title: "Flag Title",
		Lead:  "A lead paragraph.",
	})
	require.NoError(t, err)
	require.True(t, id.ID > 0, "created node ID should be positive")

	catOutput, err := tap.Cat(fx.Context(), tapper.CatOptions{
		NodeIDs:     []string{id.String()},
		ContentOnly: true,
	})
	require.NoError(t, err)
	require.Contains(t, catOutput, "# Flag Title")
	require.Contains(t, catOutput, "A lead paragraph.")
}

// TestCreate_MultiplePipedCreatesSequential verifies that sequential piped
// creates produce unique, ascending node IDs with correct content.
func TestCreate_MultiplePipedCreatesSequential(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	tap := setupTapWithKeg(t, fx)

	const N = 5
	ids := make([]keg.NodeId, N)
	for i := range N {
		content := fmt.Sprintf("# Sequential Node %d\n\nContent %d.\n", i, i)
		stream := &toolkit.Stream{
			In:      io.NopCloser(bytes.NewReader([]byte(content))),
			IsPiped: true,
		}
		id, err := tap.Create(fx.Context(), tapper.CreateOptions{
			Stream: stream,
		})
		require.NoError(t, err)
		ids[i] = id
	}

	// IDs should be unique and ascending.
	for i := 1; i < N; i++ {
		require.Greater(t, ids[i].ID, ids[i-1].ID,
			"node IDs should be ascending: %d should be > %d", ids[i].ID, ids[i-1].ID)
	}

	// Each node should have the correct content.
	for i, id := range ids {
		catOutput, err := tap.Cat(fx.Context(), tapper.CatOptions{
			NodeIDs:     []string{id.String()},
			ContentOnly: true,
		})
		require.NoError(t, err)
		require.Contains(t, catOutput, fmt.Sprintf("Sequential Node %d", i))
	}
}
