package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestSiteBuildCommand_GeneratesSite(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Create a node first.
	res := NewProcess(t, true, "create", "--keg", "personal", "--title", "Test Node").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader("# Test Node\n\nSome content.\n"))
	require.NoError(t, res.Err)

	// Rebuild index.
	res = NewProcess(t, false, "index", "rebuild", "--keg", "personal").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// Generate site.
	outputDir := "~/site-test-output"
	res = NewProcess(t, false,
		"site", "build", "--keg", "personal", "--output", outputDir, "--title", "Test Site", "--no-search",
	).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	stdout := string(res.Stdout)
	require.Contains(t, stdout, "site generated:")
	require.Contains(t, stdout, "nodes")
}

func TestSiteBuildCommand_NoKegErrors(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)
	res := NewProcess(t, false, "site", "build", "--no-search").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
}

func TestSiteBuildCommand_OutputFlag(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false,
		"site", "build", "--keg", "personal", "--output", "~/custom-out", "--no-search",
	).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	stdout := string(res.Stdout)
	require.Contains(t, stdout, "custom-out")
}

func TestSiteBuildCommand_TitleAndBaseURLFlags(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false,
		"site", "build", "--keg", "personal",
		"--output", "~/titled-site",
		"--title", "My Custom Title",
		"--base-url", "/keg/personal/",
		"--no-search",
	).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// Verify the generated index has the custom title.
	data := sb.MustReadFile("~/titled-site/index.html")
	require.Contains(t, string(data), "My Custom Title")
}

func TestSiteServeCommand_NoKegErrors(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)
	res := NewProcess(t, false, "site", "serve").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
}

func TestSiteServeCommand_HelpOutput(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)
	res := NewProcess(t, false, "site", "serve", "--help").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	stdout := string(res.Stdout)
	require.Contains(t, stdout, "serve")
	require.Contains(t, stdout, "--port")
	require.Contains(t, stdout, "--host")
	require.Contains(t, stdout, "--title")
	require.Contains(t, stdout, "--base-url")
}

func TestSiteCommand_CompletesSubcommands(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "site", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "build")
	require.Contains(t, suggestions, "serve")
}

func TestSiteBuildCommand_NoPositionalCompletions(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "site", "build", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	// The directive line should contain :4 (ShellCompDirectiveNoFileComp)
	// and there should be no file suggestions.
	stdout := string(comp.Stdout)
	suggestions := parseCompletionSuggestions(stdout)
	require.Empty(t, suggestions, "site build should not suggest positional args, got: %v", suggestions)
}

func TestSiteServeCommand_NoPositionalCompletions(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "site", "serve", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	stdout := string(comp.Stdout)
	suggestions := parseCompletionSuggestions(stdout)
	require.Empty(t, suggestions, "site serve should not suggest positional args, got: %v", suggestions)
}

func TestSiteServeCommand_HostCompletions(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewCompletionProcess(t, false, 3, "site", "serve", "--host", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	stdout := string(res.Stdout)
	suggestions := parseCompletionSuggestions(stdout)
	require.True(t, len(suggestions) > 0, "expected host completions, got: %s", stdout)

	found := false
	for _, s := range suggestions {
		if strings.Contains(s, "127.0.0.1") || strings.Contains(s, "0.0.0.0") || strings.Contains(s, "localhost") {
			found = true
			break
		}
	}
	require.True(t, found, "expected host suggestion in completions, got: %v", suggestions)
}

func TestSiteServeCommand_PortCompletions(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 3, "site", "serve", "--port", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	stdout := string(comp.Stdout)
	suggestions := parseCompletionSuggestions(stdout)
	require.True(t, len(suggestions) > 0, "expected port completions, got: %s", stdout)

	found := false
	for _, s := range suggestions {
		if s == "8080" || s == "3000" || s == "9090" {
			found = true
			break
		}
	}
	require.True(t, found, "expected port suggestion in completions, got: %v", suggestions)
}
