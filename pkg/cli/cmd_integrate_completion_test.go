package cli_test

import (
	"fmt"
	"testing"

	"github.com/spf13/cobra"
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

func TestOrientCompletion_TierFlagListsValidTiers(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "orient", "--tier", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "0")
	require.Contains(t, suggestions, "1")
	require.Contains(t, suggestions, "2")
}

func TestOrientCompletion_TierFlagFiltersByPrefix(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "orient", "--tier", "1").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Equal(t, []string{"1"}, suggestions)
}

func TestRootCompletion_FlightFlagSuppressesFileCompletion(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "--flight", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	// --flight is a free-form identifier; the completion hook must
	// return ShellCompDirectiveNoFileComp so the shell does not
	// propose arbitrary filesystem paths.
	out := string(comp.Stdout)
	expected := fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp)
	require.Contains(t, out, expected)
}

func TestIntegrateCompletion_TargetFlagRequestsDirectoryCompletion(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "integrate", "claude", "--target", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	// The cobra completion protocol encodes the directive bitmask as
	// ":<int>" at the end of the output. ShellCompDirectiveFilterDirs
	// asks the shell to only offer directories; asserting on the
	// rendered value via the cobra constant keeps the test insulated
	// from future directive renumbering.
	out := string(comp.Stdout)
	expected := fmt.Sprintf(":%d", cobra.ShellCompDirectiveFilterDirs)
	require.Contains(t, out, expected)
}
