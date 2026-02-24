package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestDirCommand_ExpandsUserHomePath(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	h := NewProcess(t, false, "dir")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "dir command should succeed")

	got := strings.TrimSpace(string(res.Stdout))
	require.NotEmpty(t, got)
	require.NotContains(t, got, "~", "dir output should be a shell-usable absolute path")
	require.Equal(t, "/home/testuser/kegs/personal", got)
}

func TestDirCommand_ExpandsExplicitAliasPath(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	h := NewProcess(t, false, "dir", "--keg", "example")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "dir command should succeed for explicit alias")

	got := strings.TrimSpace(string(res.Stdout))
	require.NotEmpty(t, got)
	require.NotContains(t, got, "~", "dir output should be a shell-usable absolute path")
	require.Equal(t, "/home/testuser/kegs/example", got)
}

func TestDirCommand_NodeDirectory(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	createRes := NewProcess(t, false, "create", "--title", "ForDir").Run(sb.Context(), sb.Runtime())
	require.NoError(t, createRes.Err)

	h := NewProcess(t, false, "dir", "1", "--keg", "example")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "dir NODE_ID should succeed for existing node")

	got := strings.TrimSpace(string(res.Stdout))
	require.Equal(t, "/home/testuser/kegs/example/1", got)
}

func TestDirCommand_NodeDirectoryErrors(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "dir", "bad-id", "--keg", "example").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "invalid node ID")

	res = NewProcess(t, false, "dir", "4242", "--keg", "example").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "node 4242 not found")
}
