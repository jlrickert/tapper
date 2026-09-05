package cli_test

import (
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestTagsCommand_CompletionSuggestsTags(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewCreateProcess(t, false, "One", "tags:\n  - zeta\n").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewCreateProcess(t, false, "Two", "tags:\n  - alpha\n").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewCreateProcess(t, false, "Three", "tags:\n  - beta\n").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	comp := NewCompletionProcess(t, false, 0, "tags", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.ElementsMatch(t, []string{"alpha", "beta", "zeta"}, suggestions)
}

func TestTagsCommand_CompletionFiltersByPrefix(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewCreateProcess(t, false, "One", "tags:\n  - alpha\n").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewCreateProcess(t, false, "Two", "tags:\n  - alpine\n").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewCreateProcess(t, false, "Three", "tags:\n  - beta\n").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	comp := NewCompletionProcess(t, false, 0, "tags", "al").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.ElementsMatch(t, []string{"alpha", "alpine"}, suggestions)
}
