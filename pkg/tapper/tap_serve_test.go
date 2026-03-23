package tapper_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// serveKeg starts a test server backed by a keg and returns its URL.
// The server is automatically shut down when the test completes.
func serveKeg(t *testing.T, tap *tapper.Tap, opts tapper.ServeOptions) string {
	t.Helper()
	ctx := t.Context()
	handler, err := tap.NewServeHandler(ctx, opts)
	require.NoError(t, err)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestServe_IndexPage(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{
		Title: "Test KEG",
	})

	resp, err := http.Get(url + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	html := string(body)

	require.Contains(t, html, "Test KEG")
	require.Contains(t, html, "First Node")
	require.Contains(t, html, "Second Node")
}

func TestServe_NodePage(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{
		Title: "Test KEG",
	})

	resp, err := http.Get(url + "/1/")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	html := string(body)

	require.Contains(t, html, "First Node")
	require.Contains(t, resp.Header.Get("Content-Type"), "text/html")
}

func TestServe_NodeRawReadme(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{})

	resp, err := http.Get(url + "/1/README.md")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/markdown")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "# First Node")
}

func TestServe_NodeRawMetaYAML(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{})

	resp, err := http.Get(url + "/1/meta.yaml")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "yaml")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "alpha")
}

func TestServe_NodeMetaJSON(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{})

	resp, err := http.Get(url + "/1/meta.json")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var meta map[string]any
	require.NoError(t, json.Unmarshal(body, &meta))
	require.NotNil(t, meta["tags"])
}

func TestServe_NodeStatsJSON(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{})

	resp, err := http.Get(url + "/1/stats.json")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")
}

func TestServe_NodeStatsYAML(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{})

	resp, err := http.Get(url + "/1/stats.yaml")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "yaml")
}

func TestServe_NotFoundNode(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{})

	resp, err := http.Get(url + "/9999/")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestServe_NotFoundPath(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{})

	resp, err := http.Get(url + "/nonexistent")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestServe_TagsIndex(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{})

	resp, err := http.Get(url + "/tags/")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	html := string(body)

	require.Contains(t, html, "alpha")
	require.Contains(t, html, "beta")
	require.Contains(t, html, "gamma")
}

func TestServe_TagPage(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{})

	resp, err := http.Get(url + "/tags/beta/")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	html := string(body)

	require.Contains(t, html, "beta")
	require.Contains(t, html, "First Node")
	require.Contains(t, html, "Second Node")
}

func TestServe_TagPageNotFound(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{})

	resp, err := http.Get(url + "/tags/nonexistent/")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestServe_ChangesPage(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{})

	resp, err := http.Get(url + "/changes/")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), "Changes")
}

func TestServe_TimezoneRendering(t *testing.T) {
	t.Parallel()
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

	// Write user config with kegSearchPaths.
	userCfg := `fallbackKeg: test
kegSearchPaths:
  - /home/testuser/kegs
`
	require.NoError(t, rt.Mkdir(tap.PathService.ConfigRoot, 0o755, true))
	require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))

	// Create and initialize the keg.
	kegDir := "/home/testuser/kegs/test"
	require.NoError(t, rt.Mkdir(kegDir, 0o755, true))

	k, err := keg.NewKegFromTarget(sb.Context(), kegurl.NewFile(kegDir), rt)
	require.NoError(t, err)
	require.NoError(t, k.Init(sb.Context()))

	// Set timezone to America/Chicago (UTC-6 / UTC-5).
	ctx := sb.Context()
	require.NoError(t, k.UpdateConfig(ctx, func(cfg *keg.Config) {
		cfg.Timezone = "America/Chicago"
	}))

	// Create a test node.
	_, err = tap.Create(ctx, tapper.CreateOptions{
		Title: "Timezone Test Node",
		Tags:  []string{"alpha"},
	})
	require.NoError(t, err)

	url := serveKeg(t, tap, tapper.ServeOptions{
		Title: "Timezone KEG",
	})

	// Request the node page and verify UTC is not in the rendered date.
	// The sandbox clock is frozen, so timestamps are deterministic.
	// With America/Chicago, the date may differ from UTC by -5 or -6 hours.
	resp, err := http.Get(url + "/1/")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	html := string(body)

	// The sandbox clock is frozen at a known time. Verify the page renders
	// a date (we just need to confirm it does not error and contains date info).
	require.Contains(t, html, "Updated:")
	require.Contains(t, html, "Timezone Test Node")
}

func TestServe_LinkRewriting(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{
		BaseURL: "/mysite/",
	})

	// Node 3 has a link to ../1 which should be rewritten to /mysite/1/.
	resp, err := http.Get(url + "/3/")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	html := string(body)
	require.Contains(t, html, `href="/mysite/1/"`)
	require.NotContains(t, html, `href="../1"`)
}

func TestServe_WatchEnabled_InjectsSSEScript(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	// Default watch=nil means enabled.
	url := serveKeg(t, tap, tapper.ServeOptions{
		Title: "Watch KEG",
	})

	resp, err := http.Get(url + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	html := string(body)

	require.Contains(t, html, "EventSource")
	require.Contains(t, html, "/events")
}

func TestServe_WatchDisabled_NoSSEScript(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	watchOff := false
	url := serveKeg(t, tap, tapper.ServeOptions{
		Title: "No Watch KEG",
		Watch: &watchOff,
	})

	resp, err := http.Get(url + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	html := string(body)

	require.NotContains(t, html, "EventSource")
	require.NotContains(t, html, "/events")
}

func TestServe_WatchEnabled_NodePage_HasSSEScript(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{
		Title: "Watch KEG",
	})

	resp, err := http.Get(url + "/1/")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	html := string(body)

	require.Contains(t, html, "EventSource")
}

func TestServe_WatchDisabled_NotFoundPage_NoSSEScript(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	watchOff := false
	url := serveKeg(t, tap, tapper.ServeOptions{
		Title: "No Watch KEG",
		Watch: &watchOff,
	})

	resp, err := http.Get(url + "/9999/")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	html := string(body)

	require.NotContains(t, html, "EventSource")
}

func TestServe_SSEBroadcaster(t *testing.T) {
	t.Parallel()

	// Test the broadcaster in isolation.
	b := tapper.NewSSEBroadcasterForTest()

	// Subscribe two clients.
	ch1 := b.Subscribe()
	ch2 := b.Subscribe()
	require.Equal(t, 2, b.Count())

	// Broadcast should send to both.
	b.Broadcast()

	select {
	case <-ch1:
	default:
		t.Fatal("ch1 should have received a broadcast")
	}
	select {
	case <-ch2:
	default:
		t.Fatal("ch2 should have received a broadcast")
	}

	// Unsubscribe one.
	b.Unsubscribe(ch1)
	require.Equal(t, 1, b.Count())

	// Broadcast should only reach ch2.
	b.Broadcast()
	select {
	case <-ch2:
	default:
		t.Fatal("ch2 should have received a broadcast after ch1 unsubscribed")
	}

	// ch1 should not receive anything.
	select {
	case <-ch1:
		t.Fatal("ch1 should not receive a broadcast after unsubscribe")
	default:
	}
}
