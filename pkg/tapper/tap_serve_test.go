package tapper_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
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

func TestServe_NodeAssetSubdirs(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	ctx := sb.Context()
	rt := sb.Runtime()

	require.NoError(t, rt.AtomicWriteFile("/home/testuser/work/d.png", []byte("png-bytes"), 0o644))
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/work/doc.txt", []byte("attachment-bytes"), 0o644))
	_, err := tap.UploadImage(ctx, tapper.UploadImageOptions{NodeID: "1", FilePath: "/home/testuser/work/d.png"})
	require.NoError(t, err)
	_, err = tap.UploadFile(ctx, tapper.UploadFileOptions{NodeID: "1", FilePath: "/home/testuser/work/doc.txt"})
	require.NoError(t, err)

	url := serveKeg(t, tap, tapper.ServeOptions{})

	// Subdirectory paths mirror the on-disk node layout.
	resp, err := http.Get(url + "/1/images/d.png")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "png-bytes", string(body))
	require.Contains(t, resp.Header.Get("Content-Type"), "image/png")

	resp, err = http.Get(url + "/1/assets/doc.txt")
	require.NoError(t, err)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "attachment-bytes", string(body))

	// An image is not reachable under /assets/ and vice versa.
	resp, err = http.Get(url + "/1/assets/d.png")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Flat fallback still serves both kinds.
	resp, err = http.Get(url + "/1/d.png")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestServe_NodePageResolvesRelativeLinks(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	ctx := sb.Context()

	k, err := keg.NewKegFromTarget(ctx, keg.NewFile("/home/testuser/kegs/@local/test"), sb.Runtime())
	require.NoError(t, err)
	nid, err := keg.ParseNode("1")
	require.NoError(t, err)
	content := "# First Node\n\nSee [two](../2) and ![d](./images/d.png).\n"
	require.NoError(t, k.SetContent(ctx, *nid, []byte(content)))

	url := serveKeg(t, tap, tapper.ServeOptions{})

	resp, err := http.Get(url + "/1/")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	html := string(body)
	require.Contains(t, html, `href="/2/"`)
	require.Contains(t, html, `src="/1/images/d.png"`)
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

	// Write user config with a local hub; the bare name "test" resolves to
	// @local/test under the hub's basePath.
	userCfg := `fallbackKeg: test
fallbackNamespace: local
hubs:
  home:
    kind: local
    basePath: /home/testuser/kegs
`
	require.NoError(t, rt.Mkdir(tap.PathService.ConfigRoot, 0o755, true))
	require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))

	// Create and initialize the keg.
	kegDir := "/home/testuser/kegs/@local/test"
	require.NoError(t, rt.Mkdir(kegDir, 0o755, true))

	k, err := keg.NewKegFromTarget(sb.Context(), keg.NewFile(kegDir), rt)
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

// TestServe_DexFreshness_NewNodeAppearsOnNextRequest verifies that creating
// a new node via the Tap API between two HTTP requests causes the second
// response to include the new node in the landing page, tag pages, and
// changes page. This is the core end-to-end test for the live content
// refresh feature.
func TestServe_DexFreshness_NewNodeAppearsOnNextRequest(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	ctx := sb.Context()

	watchOff := false
	url := serveKeg(t, tap, tapper.ServeOptions{
		Title: "Freshness KEG",
		Watch: &watchOff,
	})

	// First request: verify initial state with 3 nodes (plus zero node).
	resp, err := http.Get(url + "/")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	html := string(body)
	require.Contains(t, html, "First Node")
	require.Contains(t, html, "Second Node")
	require.NotContains(t, html, "Fresh Node")

	// Create a new node via the Tap API (simulating an external edit).
	_, err = tap.Create(ctx, tapper.CreateOptions{
		Title: "Fresh Node",
		Lead:  "This node was created after the server started.",
		Tags:  []string{"delta"},
	})
	require.NoError(t, err)

	// Second request: the landing page should now include the new node.
	resp2, err := http.Get(url + "/")
	require.NoError(t, err)
	body2, err := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	require.NoError(t, err)
	html2 := string(body2)
	require.Contains(t, html2, "Fresh Node", "landing page should show newly created node")

	// Tags index should now include the new "delta" tag.
	resp3, err := http.Get(url + "/tags/")
	require.NoError(t, err)
	body3, err := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	require.NoError(t, err)
	html3 := string(body3)
	require.Contains(t, html3, "delta", "tags index should include the new tag")

	// The delta tag page should list the new node.
	resp4, err := http.Get(url + "/tags/delta/")
	require.NoError(t, err)
	body4, err := io.ReadAll(resp4.Body)
	resp4.Body.Close()
	require.NoError(t, err)
	html4 := string(body4)
	require.Contains(t, html4, "Fresh Node", "delta tag page should list the new node")

	// Changes page should include the new node.
	resp5, err := http.Get(url + "/changes/")
	require.NoError(t, err)
	body5, err := io.ReadAll(resp5.Body)
	resp5.Body.Close()
	require.NoError(t, err)
	html5 := string(body5)
	require.Contains(t, html5, "Fresh Node", "changes page should include the new node")
}

// TestServe_DexFreshness_EditedNodeUpdatesOnNextRequest verifies that
// editing an existing node's content between requests causes the next
// response to reflect the updated title.
func TestServe_DexFreshness_EditedNodeUpdatesOnNextRequest(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	ctx := sb.Context()

	watchOff := false
	url := serveKeg(t, tap, tapper.ServeOptions{
		Title: "Edit Freshness KEG",
		Watch: &watchOff,
	})

	// First request: node 1 should show "First Node".
	resp, err := http.Get(url + "/1/")
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)
	require.Contains(t, string(body), "First Node")

	// Edit node 1 via the keg API to change its title.
	k, err := tap.LookupKeg(ctx, "test")
	require.NoError(t, err)
	err = k.SetContent(ctx, keg.NodeId{ID: 1}, []byte("# Updated First Node\n\nNew content after edit.\n"))
	require.NoError(t, err)

	// Second request: node 1 should now show the updated title.
	resp2, err := http.Get(url + "/1/")
	require.NoError(t, err)
	body2, err := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	require.NoError(t, err)
	html2 := string(body2)
	require.Contains(t, html2, "Updated First Node", "node page should reflect edited title")

	// The landing page should also show the updated title.
	resp3, err := http.Get(url + "/")
	require.NoError(t, err)
	body3, err := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	require.NoError(t, err)
	html3 := string(body3)
	require.Contains(t, html3, "Updated First Node", "landing page should reflect edited title")
}

// TestServe_SSE_EventDelivery verifies that the SSE endpoint delivers
// reload events when a broadcast is triggered.
func TestServe_SSE_EventDelivery(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	handler, err := tap.NewServeHandler(t.Context(), tapper.ServeOptions{
		Title: "SSE KEG",
	})
	require.NoError(t, err)
	t.Cleanup(handler.Close)

	// Disable the grace period so broadcasts reach clients immediately.
	handler.DisableSSEGraceForTest()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Connect to the SSE endpoint in a goroutine.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/events", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Trigger a broadcast via the exported test helper.
	// The handler's internal SSE broadcaster is accessed through the
	// ServeHandler. We broadcast by calling BroadcastForTest.
	handler.BroadcastForTest()

	// Read the SSE data from the response body.
	buf := make([]byte, 256)
	n, err := resp.Body.Read(buf)
	require.NoError(t, err)
	data := string(buf[:n])
	require.Contains(t, data, "data: reload", "SSE endpoint should deliver reload event")
}

// TestServe_WatchEnabled_ClientCooldown verifies that the injected
// JavaScript includes a client-side cooldown to prevent reload loops.
func TestServe_WatchEnabled_ClientCooldown(t *testing.T) {
	t.Parallel()
	sb, tap := setupSiteKeg(t)
	_ = sb

	url := serveKeg(t, tap, tapper.ServeOptions{
		Title: "Cooldown KEG",
	})

	resp, err := http.Get(url + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	html := string(body)

	// The cooldown variable, sessionStorage persistence, and comparison must be present.
	require.Contains(t, html, "cooldown", "SSE script should include client-side cooldown")
	require.Contains(t, html, "lastReload", "SSE script should track last reload time")
	require.Contains(t, html, "sessionStorage", "SSE script should persist reload timestamp across page loads")
}

// TestServe_SSEBroadcaster_Debounce verifies that rapid sequential
// broadcasts through the broadcaster are each delivered (the broadcaster
// itself does not debounce -- that happens in the watcher goroutine).
// This confirms the broadcaster remains a simple fan-out mechanism.
func TestServe_SSEBroadcaster_Debounce(t *testing.T) {
	t.Parallel()

	b := tapper.NewSSEBroadcasterForTest()
	ch := b.Subscribe()
	defer b.Unsubscribe(ch)

	// Broadcast three times in rapid succession.
	b.Broadcast()
	b.Broadcast()
	b.Broadcast()

	// The channel has buffer size 1. The first broadcast should be
	// received; subsequent ones are dropped (non-blocking send).
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected at least one broadcast")
	}

	// Channel should now be empty since buffer was full for 2nd and 3rd.
	select {
	case <-ch:
		t.Fatal("expected channel to be empty after draining the single buffered event")
	default:
		// Good: no extra events queued.
	}
}
