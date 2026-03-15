package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestServeCommand_NoKegErrors(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)
	res := NewProcess(t, false, "serve").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
}

func TestServeCommand_HelpOutput(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)
	res := NewProcess(t, false, "serve", "--help").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	stdout := string(res.Stdout)
	require.Contains(t, stdout, "serve")
	require.Contains(t, stdout, "--port")
	require.Contains(t, stdout, "--host")
	require.Contains(t, stdout, "--title")
	require.Contains(t, stdout, "--base-url")
}

func TestServeCommand_Completions(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewCompletionProcess(t, false, 2, "serve", "--host", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	stdout := string(res.Stdout)
	suggestions := parseCompletionSuggestions(stdout)
	require.True(t, len(suggestions) > 0, "expected host completions, got: %s", stdout)

	found := false
	for _, s := range suggestions {
		if strings.Contains(s, "127.0.0.1") || strings.Contains(s, "0.0.0.0") || strings.Contains(s, "localhost") {
			found = true
			break
		}
	}
	require.True(t, found, "expected host suggestion in completions, got: %v", suggestions)
}
