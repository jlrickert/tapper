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

func TestOrientCommandRejectsRemovedHostAndTierFlags(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	for _, args := range [][]string{
		{"orient", "--host", "codex"},
		{"orient", "--tier", "1"},
	} {
		res := NewProcess(t, false, args...).Run(sb.Context(), sb.Runtime())
		require.Error(t, res.Err)
		require.Contains(t, res.Err.Error(), "unknown flag")
	}
}

func TestOrientCommandRejectsKegTargetFlags(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	for _, args := range [][]string{
		{"orient", "--keg", "notes"},
		{"orient", "--namespace", "alice"},
		{"orient", "--hub", "home"},
	} {
		res := NewProcess(t, false, args...).Run(sb.Context(), sb.Runtime())
		require.Error(t, res.Err)
		require.Contains(t, res.Err.Error(), "tap orient is flight-scoped")
	}
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

func TestRootCompletion_FlightFlagSuggestsRemoteFlights(t *testing.T) {
	t.Parallel()
	sb := NewRemoteKegListSandbox(t, remoteCompletionKegs())

	comp := NewCompletionProcess(t, false, 0, "--flight", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "@team/+backend")
	require.Contains(t, string(comp.Stdout), fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp))
}

func TestIntegrateCompletion_PluginListsMarketplaceNames(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "integrate", "claude", "--plugin", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "tapper")
	require.Contains(t, suggestions, "tapper-dev")
	out := string(comp.Stdout)
	expected := fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp)
	require.Contains(t, out, expected)
}

func TestIntegrateCommandRejectsRemovedWithDevFlag(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	res := NewProcess(t, false, "integrate", "claude", "--with-dev", "--dry-run").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "unknown flag")
}
