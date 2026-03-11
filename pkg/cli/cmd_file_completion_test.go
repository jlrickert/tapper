package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestFileCompletion_LsCompletesNodeIDs(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "file", "ls", "--keg", "personal", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.ElementsMatch(t, []string{"0", "1", "2", "3"}, suggestions)
}

func TestFileCompletion_LsStopsAfterOneArg(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "file", "ls", "--keg", "personal", "0", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
}

func TestFileCompletion_UploadCompletesNodeIDs(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "file", "upload", "--keg", "personal", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.ElementsMatch(t, []string{"0", "1", "2", "3"}, suggestions)
}

func TestFileCompletion_DownloadCompletesNodeIDs(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "file", "download", "--keg", "personal", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.ElementsMatch(t, []string{"0", "1", "2", "3"}, suggestions)
}

func TestFileCompletion_DownloadCompletesFileNames(t *testing.T) {
	t.Parallel()
	sb := fileFixture(t)

	// Upload a file first so there's something to complete.
	NewProcess(t, false, "file", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())

	comp := NewCompletionProcess(t, false, 0, "file", "download", "0", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "default.png")
}

func TestFileCompletion_DownloadStopsAfterTwoArgs(t *testing.T) {
	t.Parallel()
	sb := fileFixture(t)

	comp := NewCompletionProcess(t, false, 0, "file", "download", "0", "default.png", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
}

func TestFileCompletion_DownloadFiltersPrefix(t *testing.T) {
	t.Parallel()
	sb := fileFixture(t)

	// Upload two files.
	NewProcess(t, false, "file", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())
	sb.MustWriteFile("~/test-images/other.txt", []byte("other"), 0o644)
	NewProcess(t, false, "file", "upload", "0", "~/test-images/other.txt").
		Run(sb.Context(), sb.Runtime())

	comp := NewCompletionProcess(t, false, 0, "file", "download", "0", "d").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "default.png")
	require.NotContains(t, suggestions, "other.txt")
}

func TestFileCompletion_RmCompletesFileNames(t *testing.T) {
	t.Parallel()
	sb := fileFixture(t)

	NewProcess(t, false, "file", "upload", "0", "~/test-images/default.png").
		Run(sb.Context(), sb.Runtime())

	comp := NewCompletionProcess(t, false, 0, "file", "rm", "0", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "default.png")
}

func TestFileCompletion_EmptyNodeReturnsNoFiles(t *testing.T) {
	t.Parallel()
	sb := fileFixture(t)

	// Node 0 has no files uploaded.
	comp := NewCompletionProcess(t, false, 0, "file", "download", "0", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
}

func TestFileCompletion_UploadStopsAfterTwoArgs(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "file", "upload", "--keg", "personal", "0", "somefile.txt", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	// After 2 args, no completions should be offered.
	out := string(comp.Stdout)
	suggestions := parseCompletionSuggestions(out)
	require.Empty(t, suggestions)

	// Check that the directive line does NOT allow file completion.
	require.True(t, strings.Contains(out, ":4") || strings.Contains(out, ":0"),
		"expected ShellCompDirectiveNoFileComp directive")
}
