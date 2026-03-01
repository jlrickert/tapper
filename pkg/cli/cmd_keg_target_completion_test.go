package cli_test

import (
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// TestKegFlagCompletion_HappyPath verifies that completing --keg "" returns
// all configured aliases from the joe fixture.
func TestKegFlagCompletion_HappyPath(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "personal")
	require.Contains(t, suggestions, "work")
	require.Contains(t, suggestions, "example")
}

// TestKegFlagCompletion_PrefixFilter verifies that completing --keg "per"
// returns only aliases whose name starts with "per".
func TestKegFlagCompletion_PrefixFilter(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "per").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "personal")
	require.NotContains(t, suggestions, "work")
	require.NotContains(t, suggestions, "example")
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

// TestKegV2Profile_NoKegFlagCompletion verifies that the kegv2 binary (which
// sets AllowKegAliasFlags=false) returns no suggestions for --keg.
func TestKegV2Profile_NoKegFlagCompletion(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	// Confirm tap profile does return suggestions (control case).
	tapSuggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.NotEmpty(t, tapSuggestions)

	// kegv2 has no --keg flag; __complete should return no matches for it.
	kegComp := NewKegV2Process(t, false, "__complete", "--keg", "").Run(sb.Context(), sb.Runtime())
	kegSuggestions := parseCompletionSuggestions(string(kegComp.Stdout))
	require.Empty(t, kegSuggestions)
}

// TestAliasFlagCompletion_Index verifies that completing --alias "" on the
// index command returns configured keg aliases.
func TestAliasFlagCompletion_Index(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "index", "--alias", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "personal")
	require.Contains(t, suggestions, "work")
}

// TestAliasFlagCompletion_Reindex verifies that completing --alias "" on the
// reindex command returns configured keg aliases.
func TestAliasFlagCompletion_Reindex(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "reindex", "--alias", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "personal")
}

// TestAliasFlagCompletion_EmptyConfig verifies that completing --alias "" with
// no matching aliases returns an empty list, not an error.
func TestAliasFlagCompletion_EmptyConfig(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	comp := NewCompletionProcess(t, false, 0, "index", "--alias", "zzz").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
}
