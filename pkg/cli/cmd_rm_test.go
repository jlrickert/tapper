package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestRemoveCommand_DeletesNode(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "create", "--title", "Delete me").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	res = NewProcess(t, false, "rm", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	_, err := sb.Runtime().Stat("~/kegs/example/1", false)
	require.Error(t, err, "node directory should be removed")

	catRes := NewProcess(t, false, "cat", "1").Run(sb.Context(), sb.Runtime())
	require.Error(t, catRes.Err)
	require.Contains(t, string(catRes.Stderr), "node 1 not found")
}

func TestRemoveCommand_ErrorCases(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "rm", "999").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "node 999 not found")

	res = NewProcess(t, false, "rm", "0").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "node 0 cannot be removed")
}

// TestRemoveCommand_RedirectsLinksToZero verifies that when a node is removed,
// all in-content links pointing to it are rewritten to reference node 0 instead
// of being left as dangling references.
//
// Uses the joe fixture which has cross-linked nodes pre-populated:
//
//	node 1 (Personal Overview): [Project Alpha](../2)  [Meeting Notes](../3)
//	node 2 (Project Alpha):     [Personal Overview](../1) related to [Meeting Notes](../3)
//	node 3 (Meeting Notes):     [Project Alpha](../2)  ../2
func TestRemoveCommand_RedirectsLinksToZero(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Remove node 2 (Project Alpha).  Nodes 1 and 3 both link to it.
	res := NewProcess(t, false, "rm", "2", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	_, err := sb.Runtime().Stat("~/kegs/personal/2", false)
	require.Error(t, err, "node 2 directory should be deleted")

	// Node 1: [Project Alpha](../2) → [Project Alpha](../0)
	content1 := string(sb.MustReadFile("~/kegs/personal/1/README.md"))
	require.Contains(t, content1, "../0", "node 1 should redirect stale link to node 0")
	require.NotContains(t, content1, "../2", "node 1 must not keep stale ref to removed node 2")

	// Node 3: [Project Alpha](../2) and bare ../2 → both become ../0
	content3 := string(sb.MustReadFile("~/kegs/personal/3/README.md"))
	require.Contains(t, content3, "../0", "node 3 should redirect stale link to node 0")
	require.NotContains(t, content3, "../2", "node 3 must not keep stale ref to removed node 2")
}

// TestRemoveCommand_RedirectsLinksCreatedViaStdin creates nodes with
// cross-links via stdin (`tap c --keg personal`), removes one, and confirms
// that every back-reference in the remaining nodes is redirected to ../0.
func TestRemoveCommand_RedirectsLinksCreatedViaStdin(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Create node 4 that links to node 5 (to be created next).
	node4Content := "# Task A\n\nDepends on [Task B](../5).\nAlso see ../5.\n"
	res := NewProcess(t, false, "create", "--keg", "personal").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(node4Content))
	require.NoError(t, res.Err)
	require.Equal(t, "4", strings.TrimSpace(string(res.Stdout)))

	// Create node 5 that links back to node 4.
	node5Content := "# Task B\n\nBlocked by [Task A](../4).\n"
	res = NewProcess(t, false, "create", "--keg", "personal").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(node5Content))
	require.NoError(t, res.Err)
	require.Equal(t, "5", strings.TrimSpace(string(res.Stdout)))

	// Remove node 5.  Node 4's references to ../5 should become ../0.
	res = NewProcess(t, false, "rm", "5", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	_, err := sb.Runtime().Stat("~/kegs/personal/5", false)
	require.Error(t, err, "node 5 directory should be deleted")

	content4 := string(sb.MustReadFile("~/kegs/personal/4/README.md"))
	require.Contains(t, content4, "../0", "references to removed node should point to 0")
	require.NotContains(t, content4, "../5", "stale ref to removed node must be gone")
}

// TestRemoveCommand_MultipleNodes removes several nodes in a single invocation
// and verifies each is gone and its backlinks redirected to zero.
func TestRemoveCommand_MultipleNodes(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// node 1 links to 2 and 3; remove both 2 and 3 in one command.
	res := NewProcess(t, false, "rm", "2", "3", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	_, err := sb.Runtime().Stat("~/kegs/personal/2", false)
	require.Error(t, err)
	_, err = sb.Runtime().Stat("~/kegs/personal/3", false)
	require.Error(t, err)

	content1 := string(sb.MustReadFile("~/kegs/personal/1/README.md"))
	require.NotContains(t, content1, "../2")
	require.NotContains(t, content1, "../3")
	require.Contains(t, content1, "../0")
}
