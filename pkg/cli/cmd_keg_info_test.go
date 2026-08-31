package cli_test

import (
	"encoding/json"
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
	require.Contains(t, stdout, "hub: home")
	require.Contains(t, stdout, "namespace: local")
	require.Contains(t, stdout, "keg: personal")
	require.Contains(t, stdout, "ref: keg:@local/personal")
	require.Contains(t, stdout, "flight:")
	require.Contains(t, stdout, "summary:")
	require.Contains(t, stdout, "node_count:")
	require.Contains(t, stdout, "files:")
	require.Contains(t, stdout, "images:")
	require.NotContains(t, stdout, "working_directory:")
	require.NotContains(t, stdout, "resolution_source:")
	require.NotContains(t, stdout, "scope:")
}

func TestInfoCommand_DebugYAMLAddsBackendDiagnostics(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	res := NewProcess(t, false, "info", "--keg", "personal", "--debug").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	stdout := string(res.Stdout)
	require.Contains(t, stdout, "debug:")
	require.Contains(t, stdout, "working_directory:")
	require.Contains(t, stdout, "backend:")
	require.Contains(t, stdout, "target:")
	require.Contains(t, stdout, "scheme:")
	require.Contains(t, stdout, "keg_directory:")
	require.NotContains(t, stdout, "resolution_source:")
	require.NotContains(t, stdout, "scope:")
}

func TestInfoCommand_ConciseAndDebugJSON(t *testing.T) {
	t.Parallel()
	for _, debug := range []bool{false, true} {
		sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
		args := []string{"info", "--keg", "personal", "--json"}
		if debug {
			args = append(args, "--debug")
		}
		res := NewProcess(t, false, args...).Run(sb.Context(), sb.Runtime())
		require.NoError(t, res.Err)
		var got map[string]any
		require.NoError(t, json.Unmarshal(res.Stdout, &got))
		require.Equal(t, "home", got["hub"])
		require.Equal(t, "local", got["namespace"])
		require.Equal(t, "personal", got["keg"])
		require.Equal(t, "keg:@local/personal", got["ref"])
		_, hasDebug := got["debug"]
		require.Equal(t, debug, hasDebug)
		require.NotContains(t, got, "resolution_source")
		require.NotContains(t, got, "scope")
	}
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

func TestInfoCommand_WithInvalidKegSettingsErrors(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	sb.MustWriteFile("~/kegs/@local/example/keg", []byte("kegv: [\n"), 0o644)

	res := NewProcess(t, false, "info", "--keg", "example").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "unable to read keg settings")
}
