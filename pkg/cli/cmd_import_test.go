package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// TestImportCmd_BasicCopyWithLinkRewrite imports two named nodes from the
// "personal" keg into the "work" keg and verifies:
//   - correct output lines and summary
//   - link to an imported node stays relative (../NEW_ID)
//   - link to a non-imported node is rewritten to keg:personal/N
func TestImportCmd_BasicCopyWithLinkRewrite(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// personal node 1 = Personal Overview (links to ../2 and ../3)
	// personal node 2 = Project Alpha      (links to ../1 and ../3)
	// work keg has only node 0, so next IDs are 1, 2.
	res := NewProcess(t, false, "import", "--from", "personal", "1", "2", "--keg", "work").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	// Each imported node emits "SRC -> DST"
	require.Contains(t, out, "1 -> 1")
	require.Contains(t, out, "2 -> 2")
	require.Contains(t, out, "imported 2 node(s)")

	// Work node 1 (was personal/1): ../2 is imported → stays ../2; ../3 not imported → keg:personal/3
	node1 := string(sb.MustReadFile("~/kegs/work/1/README.md"))
	require.Contains(t, node1, "# Personal Overview")
	require.Contains(t, node1, "../2", "link to imported node 2 should remain relative")
	require.Contains(t, node1, "keg:personal/3", "link to non-imported node 3 should be cross-keg")
	require.NotContains(t, node1, "../3", "bare ../3 must not remain")

	// Work node 2 (was personal/2): ../1 imported → ../1; ../3 not imported → keg:personal/3
	node2 := string(sb.MustReadFile("~/kegs/work/2/README.md"))
	require.Contains(t, node2, "# Project Alpha")
	require.Contains(t, node2, "keg:personal/3")
}

// TestImportCmd_KegRefArgFormat verifies that keg:ALIAS/NODE_ID positional
// arguments are accepted when --from is absent; the alias is extracted from
// the first argument.
func TestImportCmd_KegRefArgFormat(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "import", "keg:personal/1", "keg:personal/2", "--keg", "work").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "imported 2 node(s)")
}

// TestImportCmd_AllNodesSkipsZero imports all nodes from "personal" to "work"
// without specifying node IDs. Node 0 must be skipped by default.
func TestImportCmd_AllNodesSkipsZero(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// personal has nodes 0, 1, 2, 3 — so 3 non-zero nodes should be imported.
	res := NewProcess(t, false, "import", "--from", "personal", "--keg", "work").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	require.Contains(t, out, "imported 3 node(s)")

	// Node 0 from personal must NOT be present in work (work already has its own 0).
	// Work's node 0 content should be unchanged.
	node0 := string(sb.MustReadFile("~/kegs/work/0/README.md"))
	require.Contains(t, node0, "Sorry, planned but not yet available")
}

// TestImportCmd_SkipZeroFalseIncludesZero verifies that --skip-zero=false
// causes node 0 to be imported.
func TestImportCmd_SkipZeroFalseIncludesZero(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "import", "--from", "personal", "--keg", "work", "--skip-zero=false").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	// 4 nodes: 0, 1, 2, 3
	require.Contains(t, out, "imported 4 node(s)")
}

// TestImportCmd_LeaveStubs verifies that --leave-stubs replaces each source
// node's README with a forwarding stub pointing to the new location.
func TestImportCmd_LeaveStubs(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "import", "--from", "personal", "1", "--keg", "work", "--leave-stubs").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// personal/1/README.md should now be a stub.
	stub := string(sb.MustReadFile("~/kegs/personal/1/README.md"))
	require.Contains(t, stub, "Personal Overview")
	require.Contains(t, stub, "keg:work/")
	require.Contains(t, stub, "Moved to")
	// The stub should not contain the original body.
	require.NotContains(t, stub, "An index of personal notes")
}

// TestImportCmd_SelfImportError verifies that importing into the same keg as
// the source produces an error.
func TestImportCmd_SelfImportError(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "import", "--from", "personal", "1", "--keg", "personal").
		Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, strings.ToLower(string(res.Stderr)), "same")
}

// TestImportCmd_MissingFromError verifies that an error is returned when no
// --from flag and no keg:ALIAS/NODE_ID arguments are provided.
func TestImportCmd_MissingFromError(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "import", "1", "2", "--keg", "work").
		Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "--from")
}

// TestImportCmd_ConflictingAliasInArgsError verifies that using keg:ALIAS/N
// args with different aliases produces an error.
func TestImportCmd_ConflictingAliasInArgsError(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "import", "keg:personal/1", "keg:work/1", "--keg", "example").
		Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
}

// TestImportCmd_NodesNotInTarget verifies that after import the dex in the
// target keg lists the newly imported nodes.
func TestImportCmd_DexUpdatedAfterImport(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "import", "--from", "personal", "1", "2", "3", "--keg", "work").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// Use tap list to verify the work keg now lists the imported nodes.
	res = NewProcess(t, false, "list", "--keg", "work").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	listOut := string(res.Stdout)
	require.Contains(t, listOut, "Personal Overview")
	require.Contains(t, listOut, "Project Alpha")
	require.Contains(t, listOut, "Meeting Notes")
}
