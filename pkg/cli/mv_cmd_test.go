package cli_test

import (
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
