package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestMoveCommand_RewritesLinks(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "create", "--title", "One").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewProcess(t, false, "create", "--title", "Two").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	sb.MustWriteFile("~/kegs/example/1/README.md", []byte("# One\n\nSee [two](../2).\nAlso ../2.\n"), 0o644)

	res = NewProcess(t, false, "mv", "2", "3").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	content := string(sb.MustReadFile("~/kegs/example/1/README.md"))
	require.Contains(t, content, "[two](../3)")
	require.Contains(t, content, "../3.")
	require.NotContains(t, content, "../2")

	_, err := sb.Runtime().Stat("~/kegs/example/2", false)
	require.Error(t, err, "source node directory should be moved")
	_, err = sb.Runtime().Stat("~/kegs/example/3", false)
	require.NoError(t, err, "destination node directory should exist")
}

func TestMoveCommand_ErrorCases(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "mv", "999", "1000").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "node 999 not found")

	res = NewProcess(t, false, "create", "--title", "One").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewProcess(t, false, "create", "--title", "Two").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewProcess(t, false, "create", "--title", "Three").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	res = NewProcess(t, false, "mv", "2", "3").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "destination node 3 already exists")
}

// TestMoveCommand_UpdatesAllBacklinksInFixture verifies that moving a node
// rewrites every in-content reference to that node across the whole keg.
// Uses the joe fixture which has nodes with cross-links pre-populated.
func TestMoveCommand_UpdatesAllBacklinksInFixture(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Joe personal keg fixture:
	//   node 1 (Personal Overview): links to ../2 and ../3
	//   node 2 (Project Alpha):     links to ../1 and ../3
	//   node 3 (Meeting Notes):     links to ../2 (two occurrences)
	//
	// Move node 2 → 5.  Both node 1 and node 3 must update their references.

	res := NewProcess(t, false, "mv", "2", "5", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	content1 := string(sb.MustReadFile("~/kegs/personal/1/README.md"))
	require.Contains(t, content1, "../5", "node 1 should reference the new id 5")
	require.NotContains(t, content1, "../2", "node 1 must not keep stale ref to 2")

	content3 := string(sb.MustReadFile("~/kegs/personal/3/README.md"))
	require.Contains(t, content3, "../5", "node 3 should reference the new id 5")
	require.NotContains(t, content3, "../2", "node 3 must not keep stale ref to 2")

	// Directory checks
	_, err := sb.Runtime().Stat("~/kegs/personal/2", false)
	require.Error(t, err, "old node 2 directory should be gone")
	_, err = sb.Runtime().Stat("~/kegs/personal/5", false)
	require.NoError(t, err, "new node 5 directory should exist")
}

// TestMoveCommand_CreatesNodesViaStdinThenMoves creates nodes by piping content
// via stdin (simulating `tap c --keg personal`), writes cross-links, then
// exercises `tap mv` to confirm link rewriting works end-to-end.
func TestMoveCommand_CreatesNodesViaStdinThenMoves(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Create node 4: references future node 5.
	node4Content := "# Alpha Task\n\nSee [Beta Task](../5) for follow-up.\n"
	res := NewProcess(t, false, "create", "--keg", "personal").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(node4Content))
	require.NoError(t, res.Err)
	require.Equal(t, "4", strings.TrimSpace(string(res.Stdout)))

	// Create node 5 via stdin.
	node5Content := "# Beta Task\n\nRelated to [Alpha Task](../4).\n"
	res = NewProcess(t, false, "create", "--keg", "personal").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(node5Content))
	require.NoError(t, res.Err)
	require.Equal(t, "5", strings.TrimSpace(string(res.Stdout)))

	// Move node 5 → 6.  Node 4 must be updated automatically.
	res = NewProcess(t, false, "mv", "5", "6", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	content4 := string(sb.MustReadFile("~/kegs/personal/4/README.md"))
	require.Contains(t, content4, "../6")
	require.NotContains(t, content4, "../5")
}
