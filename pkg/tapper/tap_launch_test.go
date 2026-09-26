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

// fakeInferenceHub serves Hub's inference surfaces for token. Every POST
// echoes the path and bearer it saw plus its body, so a test can tell where a
// forwarded request went and which Hub token it carried.
func fakeInferenceHub(t *testing.T, token string, models string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !strings.HasPrefix(bearer, token) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/inference/openai/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, models)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/inference/"):
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("X-Internal", "leak")
			fmt.Fprintf(w, "data: {\"path\":%q,\"bearer\":%q,\"version\":%q,\"body\":%s}\n\n",
				r.URL.Path, bearer, r.Header.Get("Anthropic-Version"), body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const twoModels = `{"object":"list","data":[
  {"id":"laptop/ollama/qwen3:8b","object":"model","owned_by":"relay:laptop","context_window":32768},
  {"id":"laptop/ollama/whisper","object":"model","owned_by":"relay:laptop","capabilities":["transcription"]},
  {"id":"laptop/ollama/llama3","object":"model","owned_by":"relay:laptop"}]}`

// newLaunchTap builds a Tap whose selected hub is hubURL, with extra appended
// to the user config.
func newLaunchTap(t *testing.T, hubURL, extra string) *Tap {
	t.Helper()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	cfg := fmt.Sprintf("hub: atlas\nhubs:\n  atlas: {kind: remote, url: %s, token: hub-token}\n%s", hubURL, extra)
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(cfg), 0o644))
	tap, err := NewTap(TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	return tap
}

// noHubToken checks that no rendered part of a launch carries the Hub token.
func noHubToken(t *testing.T, got *LaunchResult) {
	t.Helper()
	for _, part := range got.Argv {
		require.NotContains(t, part, "hub-token")
	}
	for k, v := range got.Env {
		require.NotContains(t, v, "hub-token", "the Hub token must not reach the child (%s)", k)
	}
	for name, content := range got.Files {
		require.NotContains(t, content, "hub-token", "the Hub token must not reach the child (%s)", name)
	}
}

func TestLaunchHarnesses(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"claude", "codex", "opencode", "pi"}, LaunchHarnesses())
}

func TestResolveLaunch_Claude(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	got, err := newLaunchTap(t, hub.URL, "").ResolveLaunch(LaunchOptions{Harness: "claude", Model: "laptop/ollama/llama3"})
	require.NoError(t, err)
	require.Equal(t, []string{"claude", "--model", "laptop/ollama/llama3"}, got.Argv)
	require.Equal(t, launchForwarderPlaceholder+"/anthropic", got.Env["ANTHROPIC_BASE_URL"])
	require.Equal(t, launchKeyPlaceholder, got.Env["ANTHROPIC_AUTH_TOKEN"])
	for _, slot := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
		require.Equal(t, "laptop/ollama/llama3", got.Env[slot], "every model slot stays on Hub (%s)", slot)
	}
	require.Equal(t, []string{"ANTHROPIC_API_KEY"}, got.StripEnv, "an inherited key would win over the launch key")
	require.NotContains(t, got.Env, "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "no window is invented for a model without one")
	require.Equal(t, "claude", got.Env["TAP_HARNESS"])
	require.Equal(t, "laptop/ollama/llama3", got.Env["TAP_MODEL"])
	noHubToken(t, got)

	got, err = newLaunchTap(t, hub.URL, "").ResolveLaunch(LaunchOptions{Harness: "claude", Model: "laptop/ollama/qwen3:8b"})
	require.NoError(t, err)
	require.Equal(t, "32768", got.Env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"], "Claude Code compacts against the advertised window")
}

func TestResolveLaunch_Codex(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	got, err := newLaunchTap(t, hub.URL, "").ResolveLaunch(LaunchOptions{
		Harness: "codex", Model: "laptop/ollama/qwen3:8b", Args: []string{"--sandbox", "read-only"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{
		"codex",
		"-c", `model_providers.foldwise={name="Foldwise (atlas)", base_url="` + launchForwarderPlaceholder + `/v1", env_key="TAP_LAUNCH_KEY", wire_api="responses"}`,
		"-c", `model_provider="foldwise"`,
		"-c", "model_context_window=32768",
		"--model", "laptop/ollama/qwen3:8b",
		"--sandbox", "read-only",
	}, got.Argv)
	require.Equal(t, launchKeyPlaceholder, got.Env[launchKeyEnv])
	require.NotContains(t, got.Env, "OPENAI_API_KEY", "an API key would put Codex in mixed-auth mode")
	noHubToken(t, got)

	// No context window advertised, no override invented.
	got, err = newLaunchTap(t, hub.URL, "").ResolveLaunch(LaunchOptions{Harness: "codex", Model: "laptop/ollama/llama3"})
	require.NoError(t, err)
	require.NotContains(t, strings.Join(got.Argv, " "), "model_context_window")
}

func TestResolveLaunch_Opencode(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	got, err := newLaunchTap(t, hub.URL, "").ResolveLaunch(LaunchOptions{Harness: "opencode", Model: "laptop/ollama/qwen3:8b"})
	require.NoError(t, err)
	require.Equal(t, []string{"opencode", "--model", "foldwise/laptop/ollama/qwen3:8b"}, got.Argv)

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
	require.Equal(t, launchForwarderPlaceholder+"/v1", p.Options["baseURL"])
	require.Equal(t, launchKeyPlaceholder, p.Options["apiKey"], "a dry run shows placeholders, never a key")
	require.Len(t, p.Models, 2, "every chat model is offered so opencode can switch; the speech model is not")
	require.Equal(t, 32768, p.Models["laptop/ollama/qwen3:8b"].Limit.Context)
	require.Nil(t, p.Models["laptop/ollama/llama3"].Limit, "no limit is invented for a model without one")
	noHubToken(t, got)
}

func TestResolveLaunch_Pi(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	got, err := newLaunchTap(t, hub.URL, "").ResolveLaunch(LaunchOptions{Harness: "pi", Model: "laptop/ollama/qwen3:8b"})
	require.NoError(t, err)
	require.Equal(t, []string{"pi", "-e", launchDirPlaceholder + "/foldwise-pi.ts", "--provider", "foldwise", "--model", "laptop/ollama/qwen3:8b"}, got.Argv)
	require.Equal(t, launchKeyPlaceholder, got.Env[launchKeyEnv])

	ext := got.Files["foldwise-pi.ts"]
	require.Contains(t, ext, `pi.registerProvider("foldwise", `)
	start, end := strings.Index(ext, "{"), strings.LastIndex(ext, ");")
	var provider struct {
		BaseURL string `json:"baseUrl"`
		APIKey  string `json:"apiKey"`
		API     string `json:"api"`
		Models  []struct {
			ID            string `json:"id"`
			ContextWindow int    `json:"contextWindow"`
			MaxTokens     int    `json:"maxTokens"`
		} `json:"models"`
	}
	require.NoError(t, json.Unmarshal([]byte(ext[strings.Index(ext[start+1:], "{")+start+1:end]), &provider))
	require.Equal(t, launchForwarderPlaceholder+"/v1", provider.BaseURL)
	require.Equal(t, "$TAP_LAUNCH_KEY", provider.APIKey, "the key is read from the environment, never written to the file")
	require.Equal(t, "openai-completions", provider.API)
	require.Len(t, provider.Models, 2)
	require.Equal(t, 32768, provider.Models[0].ContextWindow)
	require.Equal(t, 8192, provider.Models[0].MaxTokens)
	noHubToken(t, got)
}

func TestResolveLaunch_ModelSelection(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	tap := newLaunchTap(t, hub.URL, "")

	got, err := tap.ResolveLaunch(LaunchOptions{Harness: "opencode"})
	require.NoError(t, err)
	require.Equal(t, "laptop/ollama/qwen3:8b", got.Model, "without --model the first catalog model is used")

	_, err = tap.ResolveLaunch(LaunchOptions{Harness: "opencode", Model: "desktop/ollama/qwen3:8b"})
	require.ErrorContains(t, err, "is its relay connected")
	require.ErrorContains(t, err, "laptop/ollama/llama3", "the error lists what is available")

	_, err = tap.ResolveLaunch(LaunchOptions{Harness: "claude", Model: "laptop/ollama/whisper"})
	require.ErrorContains(t, err, "not in your catalog", "a speech model cannot drive a harness")

	_, err = tap.ResolveLaunch(LaunchOptions{Harness: "vim"})
	require.ErrorContains(t, err, "unknown harness")
}

func TestResolveLaunch_HubErrors(t *testing.T) {
	t.Parallel()
	empty := fakeInferenceHub(t, "hub-token", `{"object":"list","data":[]}`)
	_, err := newLaunchTap(t, empty.URL, "").ResolveLaunch(LaunchOptions{Harness: "opencode"})
	require.ErrorContains(t, err, "tap relay")

	wrongToken := fakeInferenceHub(t, "other-token", twoModels)
	_, err = newLaunchTap(t, wrongToken.URL, "").ResolveLaunch(LaunchOptions{Harness: "opencode"})
	require.ErrorContains(t, err, "tap auth login")
}

// Configured agents are gone: a leftover agents block changes nothing, and
// validation says it is unused.
func TestResolveLaunch_IgnoresRetiredAgents(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	tap := newLaunchTap(t, hub.URL, "agent: opus\nagents:\n  opus: {model: anthropic/claude-opus-4}\n")
	got, err := tap.ResolveLaunch(LaunchOptions{Harness: "claude"})
	require.NoError(t, err)
	require.Equal(t, "laptop/ollama/qwen3:8b", got.Model)

	var retired []string
	for _, issue := range tap.DoctorConfig() {
		if strings.Contains(issue.Message, "no longer used") {
			retired = append(retired, issue.Message)
		}
	}
	require.Len(t, retired, 2, "tap doctor names both retired keys")
	require.Contains(t, retired[0], "user config agent")
	require.Contains(t, retired[1], "user config agents")
}

func TestResolveLaunch_LaunchesWithoutFlight(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	got, err := newLaunchTap(t, hub.URL, "").ResolveLaunch(LaunchOptions{Harness: "claude"})
	require.NoError(t, err)
	require.Empty(t, got.Flight)
	require.NotContains(t, got.Env, "TAP_FLIGHT",
		"a no-flight launch must not pin a root, or the child reports itself launcher-bound")
	require.Len(t, got.Warnings, 1)
	require.Contains(t, got.Warnings[0], "full access")
}

func TestResolveLaunch_RequiresHubBackedRoot(t *testing.T) {
	t.Parallel()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(`flight: "@local/+dev"
hub: home
hubs:
  home: {kind: local, basePath: /home/testuser/kegs, defaultNamespace: local}
`), 0o644))
	tap, err := NewTap(TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	_, err = tap.ResolveLaunch(LaunchOptions{Harness: "claude"})
	require.ErrorContains(t, err, "hub URL")
}

// A configured root is pinned immutably for the child's lifetime, and an
// explicit flight wins over every other source.
func TestResolveLaunch_FlightPrecedence(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	require.NoError(t, sb.Setwd("/home/testuser/work/project"))
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml",
		[]byte(fmt.Sprintf("flight: '@user/+root'\nhub: atlas\nhubs:\n  atlas: {kind: remote, url: %s, token: hub-token}\n", hub.URL)), 0o644))
	tap, err := NewTap(TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)

	got, err := tap.ResolveLaunch(LaunchOptions{Harness: "claude"})
	require.NoError(t, err)
	require.Equal(t, "@user/+root", got.Flight)
	require.Equal(t, got.Flight, got.Env["TAP_FLIGHT"])
	require.Empty(t, got.Warnings)

	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/work/project/.tapper/config.yaml",
		[]byte("flight: '@project/+root'\n"), 0o644))
	require.NoError(t, sb.Runtime().Env().Set("TAP_FLIGHT", "@environment/+root"))
	tap.ConfigService.Reload()
	got, err = tap.ResolveLaunch(LaunchOptions{Harness: "claude", Flight: "@explicit/+root"})
	require.NoError(t, err)
	require.Equal(t, "@explicit/+root", got.Env["TAP_FLIGHT"])
}

// The live render fills in what a dry run shows as placeholders.
func TestLaunchPlan_RenderFillsPlaceholders(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	tap := newLaunchTap(t, hub.URL, "")
	for _, harness := range LaunchHarnesses() {
		got, err := tap.ResolveLaunch(LaunchOptions{Harness: harness})
		require.NoError(t, err)
		inv := got.plan.render("http://127.0.0.1:9", "secret", "/tmp/launch")
		rendered := strings.Join(inv.argv, " ")
		for k, v := range inv.env {
			rendered += " " + k + "=" + v
		}
		for _, content := range inv.files {
			rendered += " " + content
		}
		require.Contains(t, rendered, "http://127.0.0.1:9", harness)
		for _, placeholder := range []string{launchForwarderPlaceholder, launchKeyPlaceholder, launchDirPlaceholder} {
			require.NotContains(t, rendered, placeholder, "%s leaves %s unfilled", harness, placeholder)
		}
	}
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
	require.True(t, strings.HasPrefix(fw.Origin(), "http://127.0.0.1:"))

	do := func(method, path string, headers map[string]string, body string) *http.Response {
		req, err := http.NewRequest(method, fw.Origin()+path, strings.NewReader(body))
		require.NoError(t, err)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	bearer := map[string]string{"Authorization": "Bearer " + fw.Secret()}
	firstEvent := func(resp *http.Response) map[string]any {
		t.Helper()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
		require.Empty(t, resp.Header.Get("X-Internal"), "upstream headers beyond content negotiation stay behind")
		scanner := bufio.NewScanner(resp.Body)
		require.True(t, scanner.Scan())
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "data: ")), &event))
		return event
	}

	require.Equal(t, http.StatusUnauthorized, do(http.MethodGet, "/v1/models", nil, "").StatusCode)
	require.Equal(t, http.StatusUnauthorized, do(http.MethodGet, "/v1/models", map[string]string{"Authorization": "Bearer wrong"}, "").StatusCode)
	require.Equal(t, http.StatusUnauthorized, do(http.MethodPost, "/anthropic/v1/messages", map[string]string{"X-Api-Key": "wrong"}, "{}").StatusCode)
	require.Equal(t, http.StatusNotFound, do(http.MethodGet, "/api/v1/whoami", bearer, "").StatusCode,
		"only the inference routes are forwarded")
	require.Equal(t, http.StatusNotFound, do(http.MethodPost, "/v1/models", bearer, "").StatusCode)
	require.Equal(t, int32(0), calls.Load(), "rejected requests never resolve a Hub token")

	require.Equal(t, http.StatusOK, do(http.MethodGet, "/v1/models", bearer, "").StatusCode)

	chat := firstEvent(do(http.MethodPost, "/v1/chat/completions", bearer, `{"model":"m","stream":true}`))
	require.Equal(t, "/inference/openai/v1/chat/completions", chat["path"])
	require.Equal(t, "hub-token-2", chat["bearer"], "each request carries a freshly resolved token")
	require.Equal(t, map[string]any{"model": "m", "stream": true}, chat["body"], "the body goes through verbatim")

	responses := firstEvent(do(http.MethodPost, "/v1/responses", bearer, `{"model":"m"}`))
	require.Equal(t, "/inference/openai/v1/responses", responses["path"])

	messages := firstEvent(do(http.MethodPost, "/anthropic/v1/messages",
		map[string]string{"X-Api-Key": fw.Secret(), "Anthropic-Version": "2023-06-01"}, `{"model":"m"}`))
	require.Equal(t, "/inference/anthropic/v1/messages", messages["path"], "Anthropic clients authenticate with x-api-key")
	require.Equal(t, "2023-06-01", messages["version"])
	require.NotEqual(t, fw.Secret(), messages["bearer"], "the launch key never reaches Hub")

	count := firstEvent(do(http.MethodPost, "/anthropic/v1/messages/count_tokens", bearer, `{"model":"m"}`))
	require.Equal(t, "/inference/anthropic/v1/messages/count_tokens", count["path"])

	empty, err := startLaunchForwarder(hub.URL, func() string { return "" })
	require.NoError(t, err)
	t.Cleanup(func() { _ = empty.Close() })
	req, _ := http.NewRequest(http.MethodGet, empty.Origin()+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+empty.Secret())
	r, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer r.Body.Close()
	body, _ := io.ReadAll(r.Body)
	require.Equal(t, http.StatusUnauthorized, r.StatusCode)
	require.Contains(t, string(body), "tap auth login")
}
