package tapper

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// fakeInferenceHub serves Hub's /inference/openai/v1 surface for token.
// Completions echo the bearer they saw, so a test can tell which Hub token a
// forwarded request carried.
func fakeInferenceHub(t *testing.T, token string, models string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !strings.HasPrefix(bearer, token) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/inference/openai/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, models)
		case "/inference/openai/v1/chat/completions":
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("X-Internal", "leak")
			fmt.Fprintf(w, "data: {\"bearer\":%q,\"body\":%s}\n\n", bearer, body)
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const twoModels = `{"object":"list","data":[
  {"id":"laptop/ollama/qwen3:8b","object":"model","owned_by":"relay:laptop","context_window":32768},
  {"id":"laptop/ollama/llama3","object":"model","owned_by":"relay:laptop"}]}`

func newHubLaunchTap(t *testing.T, hubURL, extra string) *Tap {
	t.Helper()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	cfg := fmt.Sprintf("hub: atlas\nhubs:\n  atlas: {kind: remote, url: %s, token: hub-token}\n%s", hubURL, extra)
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(cfg), 0o644))
	tap, err := NewTap(TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	return tap
}

func TestResolveLaunch_HubModeWiresOpencodeToTheCatalog(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	tap := newHubLaunchTap(t, hub.URL, "")

	got, err := tap.ResolveLaunch(LaunchOptions{Harness: "opencode", Model: "laptop/ollama/qwen3:8b", Args: []string{"--print-logs"}})
	require.NoError(t, err)
	require.Equal(t, LaunchSourceHub, got.Source)
	require.Equal(t, "atlas", got.Hub)
	require.Equal(t, []string{"opencode", "--model", "foldwise/laptop/ollama/qwen3:8b", "--print-logs"}, got.Argv)
	require.Equal(t, "atlas", got.Env["TAP_HUB"])
	require.NotContains(t, got.Env, "TAP_AGENT")

	var cfg struct {
		Provider map[string]struct {
			NPM     string            `json:"npm"`
			Options map[string]string `json:"options"`
			Models  map[string]struct {
				Limit *struct{ Context, Output int } `json:"limit"`
			} `json:"models"`
		} `json:"provider"`
	}
	require.NoError(t, json.Unmarshal([]byte(got.Env["OPENCODE_CONFIG_CONTENT"]), &cfg))
	p := cfg.Provider["foldwise"]
	require.Equal(t, "@ai-sdk/openai-compatible", p.NPM)
	require.Equal(t, launchForwarderPlaceholder, p.Options["baseURL"])
	require.Equal(t, launchKeyPlaceholder, p.Options["apiKey"], "a dry run shows placeholders, never a key")
	require.Len(t, p.Models, 2, "every catalog model is offered so opencode can switch")
	require.Equal(t, 32768, p.Models["laptop/ollama/qwen3:8b"].Limit.Context)
	require.Nil(t, p.Models["laptop/ollama/llama3"].Limit, "no limit is invented for a model without one")

	for k, v := range got.Env {
		require.NotContains(t, v, "hub-token", "the Hub token must not reach the child (%s)", k)
	}

	// The live render swaps in the forwarder's address and key.
	argv, env := got.hub.render("http://127.0.0.1:9/v1", "secret")
	require.Equal(t, got.Argv, argv)
	require.Contains(t, env["OPENCODE_CONFIG_CONTENT"], `"baseURL":"http://127.0.0.1:9/v1"`)
	require.Contains(t, env["OPENCODE_CONFIG_CONTENT"], `"apiKey":"secret"`)
}

func TestResolveLaunch_HubModeSelection(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)

	// Neither --model nor any agent: hub mode on the first catalog model.
	got, err := newHubLaunchTap(t, hub.URL, "").ResolveLaunch(LaunchOptions{Harness: "opencode"})
	require.NoError(t, err)
	require.Equal(t, "laptop/ollama/qwen3:8b", got.Model)

	// A configured default agent keeps the agent path.
	withAgent := newHubLaunchTap(t, hub.URL, "agent: local\nagents:\n  local: {model: ollama/qwen3:8b}\n")
	got, err = withAgent.ResolveLaunch(LaunchOptions{Harness: "opencode"})
	require.NoError(t, err)
	require.Equal(t, LaunchSourceAgent, got.Source)

	// --model overrides the configured agent.
	got, err = withAgent.ResolveLaunch(LaunchOptions{Harness: "opencode", Model: "laptop/ollama/llama3"})
	require.NoError(t, err)
	require.Equal(t, LaunchSourceHub, got.Source)

	_, err = withAgent.ResolveLaunch(LaunchOptions{Harness: "opencode", Model: "x", Agent: "local"})
	require.ErrorContains(t, err, "mutually exclusive")
}

func TestResolveLaunch_HubModeErrors(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	tap := newHubLaunchTap(t, hub.URL, "")

	_, err := tap.ResolveLaunch(LaunchOptions{Harness: "opencode", Model: "desktop/ollama/qwen3:8b"})
	require.ErrorContains(t, err, "is its relay connected")
	require.ErrorContains(t, err, "laptop/ollama/llama3", "the error lists what is available")

	_, err = tap.ResolveLaunch(LaunchOptions{Harness: "claude", Model: "laptop/ollama/llama3"})
	require.ErrorContains(t, err, "cannot use Hub models yet")

	empty := fakeInferenceHub(t, "hub-token", `{"object":"list","data":[]}`)
	_, err = newHubLaunchTap(t, empty.URL, "").ResolveLaunch(LaunchOptions{Harness: "opencode"})
	require.ErrorContains(t, err, "tap relay")

	wrongToken := fakeInferenceHub(t, "other-token", twoModels)
	_, err = newHubLaunchTap(t, wrongToken.URL, "").ResolveLaunch(LaunchOptions{Harness: "opencode"})
	require.ErrorContains(t, err, "tap auth login")
}

func TestLaunchForwarder(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	var calls atomic.Int32
	fw, err := startLaunchForwarder(hub.URL, func() string {
		// A fresh token per request, as a refreshing resolver would give.
		return fmt.Sprintf("hub-token-%d", calls.Add(1))
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = fw.Close() })
	require.True(t, strings.HasPrefix(fw.BaseURL(), "http://127.0.0.1:"))

	do := func(method, path, key, body string) *http.Response {
		req, err := http.NewRequest(method, strings.TrimSuffix(fw.BaseURL(), "/v1")+path, strings.NewReader(body))
		require.NoError(t, err)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}

	require.Equal(t, http.StatusUnauthorized, do(http.MethodGet, "/v1/models", "", "").StatusCode)
	require.Equal(t, http.StatusUnauthorized, do(http.MethodGet, "/v1/models", "wrong", "").StatusCode)
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, "/api/v1/whoami", fw.Secret(), "").StatusCode,
		"only the inference routes are forwarded")
	require.Equal(t, http.StatusNotFound, do(http.MethodPost, "/v1/models", fw.Secret(), "").StatusCode)
	require.Equal(t, int32(0), calls.Load(), "rejected requests never resolve a Hub token")

	models := do(http.MethodGet, "/v1/models", fw.Secret(), "")
	require.Equal(t, http.StatusOK, models.StatusCode)

	resp := do(http.MethodPost, "/v1/chat/completions", fw.Secret(), `{"model":"m","stream":true}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	require.Empty(t, resp.Header.Get("X-Internal"), "upstream headers beyond content negotiation stay behind")
	events := bufio.NewScanner(resp.Body)
	require.True(t, events.Scan())
	require.Equal(t, `data: {"bearer":"hub-token-2","body":{"model":"m","stream":true}}`, events.Text(),
		"each request carries a freshly resolved token and the body verbatim")

	empty, err := startLaunchForwarder(hub.URL, func() string { return "" })
	require.NoError(t, err)
	t.Cleanup(func() { _ = empty.Close() })
	req, _ := http.NewRequest(http.MethodGet, empty.BaseURL()+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+empty.Secret())
	r, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	require.Equal(t, http.StatusUnauthorized, r.StatusCode)
	require.Contains(t, string(body), "tap auth login")
}
