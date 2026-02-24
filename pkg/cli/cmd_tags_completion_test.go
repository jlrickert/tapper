package cli_test

import (
	"strings"
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

func parseCompletionSuggestions(raw string) []string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") {
			break
		}
		if strings.Contains(line, "\t") {
			line = strings.SplitN(line, "\t", 2)[0]
		}
		out = append(out, line)
	}
	return out
}
