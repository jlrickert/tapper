package cli_test

import (
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// TestKegFlagCompletion_HappyPath verifies that completing --keg "" returns no
// suggestions: with the alias map removed, kegs are no longer enumerable from
// config, so the completer offers nothing rather than stray file paths.
func TestKegFlagCompletion_HappyPath(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
}

// TestKegFlagCompletion_PrefixFilter verifies that completing --keg "per"
// returns no suggestions now that the alias map has been removed.
func TestKegFlagCompletion_PrefixFilter(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "per").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
}

// TestKegFlagCompletion_EmptyConfig verifies that completing --keg "" against
// a config with no aliases returns an empty suggestion list (not an error).
func TestKegFlagCompletion_EmptyConfig(t *testing.T) {
	t.Parallel()
	// testuser fixture has only "example" configured; use a subcommand that
	// needs --keg but a config with a minimal alias set.
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "zzz").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
}

// TestKegProfile_NoKegFlagCompletion verifies that the keg binary (which
// sets AllowKegAliasFlags=false) returns no suggestions for --keg. The tap
// profile registers the flag but, with the alias map removed, also returns no
// suggestions without erroring.
func TestKegProfile_NoKegFlagCompletion(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	// tap registers the --keg flag completer; it no longer enumerates aliases.
	tapSuggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, tapSuggestions)

	// keg has no --keg flag; __complete should return no matches for it.
	kegComp := NewKegProcess(t, false, "__complete", "--keg", "").Run(sb.Context(), sb.Runtime())
	kegSuggestions := parseCompletionSuggestions(string(kegComp.Stdout))
	require.Empty(t, kegSuggestions)
}

// TestKegFlagCompletion_IndexSubcommand verifies that the global --keg flag
// completion is wired on index subcommands and (with the alias map removed)
// returns no suggestions without erroring.
func TestKegFlagCompletion_IndexSubcommand(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "index", "rebuild", "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
}
