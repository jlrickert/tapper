package cli_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	// Register the default integration adapters so IntegrateHosts()
	// produces a non-empty completion list under test.
	_ "github.com/jlrickert/tapper/pkg/integrations/adapters"
)

func TestIntegrateCompletion_HostPositionalLists(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "integrate", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "claude")
	require.Contains(t, suggestions, "codex")
}

func TestIntegrateCompletion_StopsAfterOneArg(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "integrate", "codex", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
}

func TestOrientCompletion_HostFlagLists(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "orient", "--host", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "claude")
	require.Contains(t, suggestions, "codex")
}
