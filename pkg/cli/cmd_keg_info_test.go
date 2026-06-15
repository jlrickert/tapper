package cli_test

import (
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestInfoCommand_DisplaysDiagnostics(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "info", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	stdout := string(res.Stdout)
	require.Contains(t, stdout, "working_directory:")
	require.Contains(t, stdout, "target:")
	require.Contains(t, stdout, "node_count:")
	require.Contains(t, stdout, "assets:")
	require.Contains(t, stdout, "images:")
	require.NotContains(t, stdout, "files:")
}

func TestInfoCommand_NoConfiguredKegErrors(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	res := NewProcess(t, false, "info").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "tap bootstrap")
}

func TestInfoCommand_WithNonexistentAliasErrors(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "info", "--keg", "does-not-exist").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "keg not initialized")
}

func TestInfoCommand_WithInvalidKegConfigErrors(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	sb.MustWriteFile("~/kegs/@local/example/keg", []byte("kegv: [\n"), 0o644)

	res := NewProcess(t, false, "info", "--keg", "example").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "unable to read keg config")
}
