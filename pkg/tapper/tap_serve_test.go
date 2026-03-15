package tapper_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

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
