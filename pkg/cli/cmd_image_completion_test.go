package cli_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageCompletion_DownloadOffersPathsForThirdArg(t *testing.T) {
	t.Parallel()
	sb := imageFixture(t)

	comp := NewCompletionProcess(t, false, 0, "image", "download", "0", "default.png", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	out := string(comp.Stdout)
	require.True(t, strings.Contains(out, ":0"),
		"expected ShellCompDirectiveDefault directive for filesystem completion")
}

func TestImageCompletion_DownloadStopsAfterThreeArgs(t *testing.T) {
	t.Parallel()
	sb := imageFixture(t)

	comp := NewCompletionProcess(t, false, 0, "image", "download", "0", "default.png", "/tmp/out", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
}
