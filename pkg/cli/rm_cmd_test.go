package cli_test

import (
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
