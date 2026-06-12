package cli_test

import (
	"fmt"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestKegFlagCompletion_HappyPath verifies that completing --keg "" returns
// logical keg references from the configured hubs, not filesystem paths.
func TestKegFlagCompletion_HappyPath(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "@local/example")
	require.Contains(t, suggestions, "@local/personal")
	require.Contains(t, suggestions, "@local/work")
	require.Contains(t, suggestions, "example")
	require.Contains(t, suggestions, "personal")
	require.Contains(t, suggestions, "work")
	require.Contains(t, string(comp.Stdout), fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp))
}

func TestKegFlagCompletion_ShortFlag(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "-k", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "@local/personal")
	require.Contains(t, suggestions, "personal")
}

// TestKegFlagCompletion_PrefixFilter verifies that prefix filtering works for
// bare logical names in the active namespace.
func TestKegFlagCompletion_PrefixFilter(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "per").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Equal(t, []string{"personal"}, suggestions)
}

func TestKegFlagCompletion_CanonicalPrefixFilter(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "@local/p").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Equal(t, []string{"@local/personal"}, suggestions)
}

// TestKegFlagCompletion_NoMatches verifies that completing --keg with an
// unmatched prefix returns an empty suggestion list (not an error).
func TestKegFlagCompletion_EmptyConfig(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "zzz").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
}

func TestKegFlagCompletion_RemoteFailureIsBestEffort(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	sb.MustWriteFile("~/.config/tapper/config.yaml", []byte("hubs:\n  atlas:\n    kind: remote\n    url: https://atlas.foldwise.ai\n"), 0o644)

	comp := NewCompletionProcess(t, false, 0, "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
	require.Contains(t, string(comp.Stdout), fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp))
}

// TestKegProfile_NoKegFlagCompletion verifies that the keg binary (which
// sets AllowKegAliasFlags=false) returns no suggestions for --keg.
func TestKegProfile_NoKegFlagCompletion(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	// tap registers the --keg flag completer and enumerates logical kegs.
	tapSuggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, tapSuggestions, "@local/personal")

	// keg has no --keg flag; __complete should return no matches for it.
	kegComp := NewKegProcess(t, false, "__complete", "--keg", "").Run(sb.Context(), sb.Runtime())
	kegSuggestions := parseCompletionSuggestions(string(kegComp.Stdout))
	require.Empty(t, kegSuggestions)
}

// TestKegFlagCompletion_IndexSubcommand verifies that the global --keg flag
// completion is wired on index subcommands.
func TestKegFlagCompletion_IndexSubcommand(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "index", "rebuild", "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "@local/personal")
	require.Contains(t, suggestions, "personal")
}
