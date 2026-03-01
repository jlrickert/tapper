package cli_test

import (
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestTagsCommand_CompletionSuggestsTags(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "create", "--title", "One", "--tags", "zeta").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewProcess(t, false, "create", "--title", "Two", "--tags", "alpha").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewProcess(t, false, "create", "--title", "Three", "--tags", "beta").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	comp := NewCompletionProcess(t, false, 0, "tags", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.ElementsMatch(t, []string{"alpha", "beta", "zeta"}, suggestions)
}

func TestTagsCommand_CompletionFiltersByPrefix(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "create", "--title", "One", "--tags", "alpha").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewProcess(t, false, "create", "--title", "Two", "--tags", "alpine").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewProcess(t, false, "create", "--title", "Three", "--tags", "beta").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	comp := NewCompletionProcess(t, false, 0, "tags", "al").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.ElementsMatch(t, []string{"alpha", "alpine"}, suggestions)
}

