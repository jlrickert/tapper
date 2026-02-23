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
