package tapper_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// setupSiteKeg creates a Tap with an initialized keg at ~/kegs/test
// and creates several test nodes for site generation testing.
func setupSiteKeg(t *testing.T) (*sandbox.Sandbox, *tapper.Tap) {
	t.Helper()
	sb := NewSandbox(t)
	rt := sb.Runtime()

	root := "/home/testuser/work"
	require.NoError(t, rt.Mkdir(root, 0o755, true))
	require.NoError(t, sb.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: rt,
	})
	require.NoError(t, err)

	// Write user config with an explicit local keg.
	userCfg := `fallbackKeg: test
kegs:
  test: { path: /home/testuser/kegs/test }
`
	require.NoError(t, rt.Mkdir(tap.PathService.ConfigRoot, 0o755, true))
	require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))

	// Create and initialize the keg.
	kegDir := "/home/testuser/kegs/test"
	require.NoError(t, rt.Mkdir(kegDir, 0o755, true))

	k, err := keg.NewKegFromTarget(sb.Context(), keg.NewFile(kegDir), rt)
	require.NoError(t, err)
	require.NoError(t, k.Init(sb.Context()))

	ctx := sb.Context()

	// Create test nodes.
	_, err = tap.Create(ctx, tapper.CreateOptions{
		Title: "First Node",
		Lead:  "This is the first node.",
		Tags:  []string{"alpha", "beta"},
	})
	require.NoError(t, err)

	_, err = tap.Create(ctx, tapper.CreateOptions{
		Title: "Second Node",
		Lead:  "This is the second node.",
		Tags:  []string{"beta", "gamma"},
	})
	require.NoError(t, err)

	// Create a node with a link to node 1.
	body := []byte("# Third Node\n\nSee [first](../1) for details.\n")
	_, err = tap.Create(ctx, tapper.CreateOptions{
		Stream: &toolkit.Stream{IsPiped: true, In: bytes.NewReader(body)},
		Tags:   []string{"alpha"},
	})
	require.NoError(t, err)

	return sb, tap
}

func TestSite_GeneratesNodePages(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	ctx := sb.Context()
	rt := sb.Runtime()

	outputDir := "/home/testuser/site-output"
	result, err := tap.Site(ctx, tapper.SiteOptions{
		Output:   outputDir,
		Title:    "Test KEG",
		BaseURL:  "/",
		NoSearch: true,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 4, result.NodeCount) // 0 + 3 created
	require.False(t, result.HasSearch)

	// Verify node 0 directory.
	data, err := rt.ReadFile(filepath.Join(outputDir, "0", "index.html"))
	require.NoError(t, err)
	require.Contains(t, string(data), "planned but not yet available")

	// Verify node 1 directory.
	data, err = rt.ReadFile(filepath.Join(outputDir, "1", "index.html"))
	require.NoError(t, err)
	require.Contains(t, string(data), "First Node")

	// Verify raw README.md copied.
	data, err = rt.ReadFile(filepath.Join(outputDir, "1", "README.md"))
	require.NoError(t, err)
	require.Contains(t, string(data), "# First Node")

	// Verify meta.yaml exists.
	_, err = rt.ReadFile(filepath.Join(outputDir, "1", "meta.yaml"))
	require.NoError(t, err)

	// Verify meta.json exists.
	_, err = rt.ReadFile(filepath.Join(outputDir, "1", "meta.json"))
	require.NoError(t, err)

	// Verify stats.json exists.
	_, err = rt.ReadFile(filepath.Join(outputDir, "1", "stats.json"))
	require.NoError(t, err)

	// Verify stats.yaml exists.
	_, err = rt.ReadFile(filepath.Join(outputDir, "1", "stats.yaml"))
	require.NoError(t, err)
}

func TestSite_GeneratesIndexPage(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	ctx := sb.Context()
	rt := sb.Runtime()

	outputDir := "/home/testuser/site-out-idx"
	_, err := tap.Site(ctx, tapper.SiteOptions{
		Output:   outputDir,
		Title:    "My KEG",
		NoSearch: true,
	})
	require.NoError(t, err)

	data, err := rt.ReadFile(filepath.Join(outputDir, "index.html"))
	require.NoError(t, err)
	html := string(data)
	require.Contains(t, html, "My KEG")
	require.Contains(t, html, "First Node")
	require.Contains(t, html, "Second Node")
}

func TestSite_GeneratesTagPages(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	ctx := sb.Context()
	rt := sb.Runtime()

	outputDir := "/home/testuser/site-out-tags"
	result, err := tap.Site(ctx, tapper.SiteOptions{
		Output:   outputDir,
		Title:    "Tags Test",
		NoSearch: true,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, result.TagCount, 3) // alpha, beta, gamma

	// Tags index page.
	data, err := rt.ReadFile(filepath.Join(outputDir, "tags", "index.html"))
	require.NoError(t, err)
	html := string(data)
	require.Contains(t, html, "alpha")
	require.Contains(t, html, "beta")
	require.Contains(t, html, "gamma")

	// Individual tag page.
	data, err = rt.ReadFile(filepath.Join(outputDir, "tags", "beta", "index.html"))
	require.NoError(t, err)
	html = string(data)
	require.Contains(t, html, "beta")
	require.Contains(t, html, "First Node")
	require.Contains(t, html, "Second Node")
}

func TestSite_GeneratesChangesPage(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	ctx := sb.Context()
	rt := sb.Runtime()

	outputDir := "/home/testuser/site-out-changes"
	_, err := tap.Site(ctx, tapper.SiteOptions{
		Output:   outputDir,
		NoSearch: true,
	})
	require.NoError(t, err)

	data, err := rt.ReadFile(filepath.Join(outputDir, "changes", "index.html"))
	require.NoError(t, err)
	require.Contains(t, string(data), "Changes")
}

func TestSite_NodeLinkRewriting(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	ctx := sb.Context()
	rt := sb.Runtime()

	outputDir := "/home/testuser/site-out-links"
	_, err := tap.Site(ctx, tapper.SiteOptions{
		Output:   outputDir,
		BaseURL:  "/mysite/",
		NoSearch: true,
	})
	require.NoError(t, err)

	// Node 3 has a link to ../1 which should be rewritten to /mysite/1/.
	data, err := rt.ReadFile(filepath.Join(outputDir, "3", "index.html"))
	require.NoError(t, err)
	html := string(data)
	require.Contains(t, html, `href="/mysite/1/"`)
	require.NotContains(t, html, `href="../1"`)
}

func TestSite_ConfigDefaults(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	ctx := sb.Context()

	// Set up site config on the keg.
	k, err := tap.LookupKeg(ctx, "test")
	require.NoError(t, err)
	searchFalse := false
	err = k.UpdateConfig(ctx, func(cfg *keg.Config) {
		cfg.Site = &keg.SiteConfig{
			Output:  "/home/testuser/configured-output",
			Title:   "Configured Title",
			BaseURL: "/configured/",
			Search:  &searchFalse,
		}
	})
	require.NoError(t, err)

	result, err := tap.Site(ctx, tapper.SiteOptions{})
	require.NoError(t, err)
	require.Contains(t, result.OutputDir, "configured-output")
	require.False(t, result.HasSearch)

	// Verify the configured title appears in the generated HTML.
	rt := sb.Runtime()
	data, err := rt.ReadFile(filepath.Join(result.OutputDir, "index.html"))
	require.NoError(t, err)
	require.Contains(t, string(data), "Configured Title")
}

func TestSite_CLIFlagsOverrideConfig(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	ctx := sb.Context()

	// Set up site config.
	k, err := tap.LookupKeg(ctx, "test")
	require.NoError(t, err)
	err = k.UpdateConfig(ctx, func(cfg *keg.Config) {
		cfg.Site = &keg.SiteConfig{
			Title: "Config Title",
		}
	})
	require.NoError(t, err)

	// CLI flag should override config.
	outputDir := "/home/testuser/cli-output"
	result, err := tap.Site(ctx, tapper.SiteOptions{
		Output:   outputDir,
		Title:    "CLI Title",
		NoSearch: true,
	})
	require.NoError(t, err)
	require.Contains(t, result.OutputDir, "cli-output")

	rt := sb.Runtime()
	data, err := rt.ReadFile(filepath.Join(result.OutputDir, "index.html"))
	require.NoError(t, err)
	require.Contains(t, string(data), "CLI Title")
	require.NotContains(t, string(data), "Config Title")
}
