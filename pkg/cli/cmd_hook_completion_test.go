package cli_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The hook group is hidden because hosts invoke it, not people — but cobra
// drops a hidden command from completion as well as from help, and hiding the
// subcommands too left `tap hook <TAB>` suggesting nothing at all. Only the
// parent is hidden now, and this is the regression that proves it.
func TestHookCompletion_SuggestsBothSubcommands(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "hook", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "pre-tool-use")
	require.Contains(t, suggestions, "session-start")
}
