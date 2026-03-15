package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestSiteCommand_GeneratesSite(t *testing.T) {
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
		"site", "--keg", "personal", "--output", outputDir, "--title", "Test Site", "--no-search",
	).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	stdout := string(res.Stdout)
	require.Contains(t, stdout, "site generated:")
	require.Contains(t, stdout, "nodes")
}

func TestSiteCommand_NoKegErrors(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)
	res := NewProcess(t, false, "site", "--no-search").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
}

func TestSiteCommand_OutputFlag(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false,
		"site", "--keg", "personal", "--output", "~/custom-out", "--no-search",
	).Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	stdout := string(res.Stdout)
	require.Contains(t, stdout, "custom-out")
}

func TestSiteCommand_TitleAndBaseURLFlags(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false,
		"site", "--keg", "personal",
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
