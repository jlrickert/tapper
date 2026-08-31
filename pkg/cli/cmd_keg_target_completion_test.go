package cli_test

import (
	"fmt"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestKegFlagCompletion_HappyPath verifies that completing --keg "" returns
// logical keg references from the configured hubs, not filesystem paths.
func TestKegFlagCompletion_HappyPath(t *testing.T) {
	t.Parallel()
	sb := NewRemoteKegListSandbox(t, remoteCompletionKegs())

	comp := NewCompletionProcess(t, false, 0, "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "@team/example")
	require.Contains(t, suggestions, "@team/personal")
	require.Contains(t, suggestions, "@team/work")
	require.Contains(t, suggestions, "example")
	require.Contains(t, suggestions, "personal")
	require.Contains(t, suggestions, "work")
	require.Contains(t, string(comp.Stdout), fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp))
}

func TestKegFlagCompletion_ShortFlag(t *testing.T) {
	t.Parallel()
	sb := NewRemoteKegListSandbox(t, remoteCompletionKegs())

	comp := NewCompletionProcess(t, false, 0, "-k", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "@team/personal")
	require.Contains(t, suggestions, "personal")
}

// TestKegFlagCompletion_PrefixFilter verifies that prefix filtering works for
// bare logical names in the active namespace.
func TestKegFlagCompletion_PrefixFilter(t *testing.T) {
	t.Parallel()
	sb := NewRemoteKegListSandbox(t, remoteCompletionKegs())

	comp := NewCompletionProcess(t, false, 0, "--keg", "per").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Equal(t, []string{"personal"}, suggestions)
}

func TestKegFlagCompletion_CanonicalPrefixFilter(t *testing.T) {
	t.Parallel()
	sb := NewRemoteKegListSandbox(t, remoteCompletionKegs())

	comp := NewCompletionProcess(t, false, 0, "--keg", "@team/p").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Equal(t, []string{"@team/personal"}, suggestions)
}

// TestKegFlagCompletion_NoMatches verifies that completing --keg with an
// unmatched prefix returns an empty suggestion list (not an error).
func TestKegFlagCompletion_EmptyConfig(t *testing.T) {
	t.Parallel()
	sb := NewRemoteKegListSandbox(t, nil)

	comp := NewCompletionProcess(t, false, 0, "--keg", "zzz").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
}

func TestKegFlagCompletion_RemoteFailureIsBestEffort(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	sb.MustWriteFile("~/.config/tapper/config.yaml", []byte("hubs:\n  atlas:\n    kind: remote\n    url: https://atlas.foldwise.ai\n"), 0o644)

	comp := NewCompletionProcess(t, false, 0, "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
	require.Contains(t, string(comp.Stdout), fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp))
}

func TestKegFlagCompletion_IndexSubcommand(t *testing.T) {
	t.Parallel()
	sb := NewRemoteKegListSandbox(t, remoteCompletionKegs())

	comp := NewCompletionProcess(t, false, 0, "index", "rebuild", "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "@team/personal")
	require.Contains(t, suggestions, "personal")
}

func remoteCompletionKegs() []tapper.HubKeg {
	return []tapper.HubKeg{
		{Namespace: "team", Alias: "example", Visibility: "private", Role: "admin"},
		{Namespace: "team", Alias: "personal", Visibility: "private", Role: "admin"},
		{Namespace: "team", Alias: "work", Visibility: "private", Role: "editor"},
	}
}
