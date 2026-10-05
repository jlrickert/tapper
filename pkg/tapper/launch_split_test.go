package tapper

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Split mode is Claude Code's default: no Hub credential in the child, the key
// moves into the base URL's path, and the picker adds Hub models beside
// Claude's own instead of replacing them.
func TestResolveLaunch_ClaudeSplit(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	tap := newLaunchTap(t, hub.URL, "")

	got, err := tap.ResolveLaunch(LaunchOptions{Harness: "claude"})
	require.NoError(t, err)
	require.Equal(t, LaunchInferenceSplit, got.Inference)
	require.Empty(t, got.Model, "without --model Claude Code starts on its own default")
	require.Equal(t, "claude", got.Argv[0])
	require.NotContains(t, got.Argv, "--model")
	require.Equal(t, launchForwarderPlaceholder+"/t/"+launchKeyPlaceholder+"/anthropic", got.Env["ANTHROPIC_BASE_URL"])
	for _, name := range []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_SMALL_FAST_MODEL", "CLAUDE_CODE_MAX_CONTEXT_TOKENS", "TAP_MODEL"} {
		require.NotContains(t, got.Env, name, "split mode leaves %s to Claude Code", name)
	}
	require.Equal(t, []string{"TAP_AGENT"}, got.StripEnv, "the user's own Anthropic credential is not stripped")
	var settings struct {
		ModelPicker struct {
			Options []struct{ Model string } `json:"options"`
			Replace *bool                    `json:"replaceBuiltInOptions"`
		} `json:"modelPicker"`
	}
	require.Equal(t, "--settings", got.Argv[1])
	require.NoError(t, json.Unmarshal([]byte(got.Argv[2]), &settings))
	require.Len(t, settings.ModelPicker.Options, 2)
	require.NotNil(t, settings.ModelPicker.Replace)
	require.False(t, *settings.ModelPicker.Replace, "Claude's built-in models stay in the picker")
	noHubToken(t, got)

	got, err = tap.ResolveLaunch(LaunchOptions{Harness: "claude", Model: "sonnet"})
	require.NoError(t, err)
	require.Equal(t, []string{"claude", "--model", "sonnet"}, got.Argv[:3])
	require.NotContains(t, got.Env, "CLAUDE_CODE_MAX_CONTEXT_TOKENS")

	got, err = tap.ResolveLaunch(LaunchOptions{Harness: "claude", Model: "@me/qwen3:8b"})
	require.NoError(t, err)
	require.Equal(t, "32768", got.Env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"], "a Hub starting model still sets its window")

	_, err = tap.ResolveLaunch(LaunchOptions{Harness: "claude", Model: "@other/x"})
	require.ErrorContains(t, err, "neither a Claude model nor in your catalog")

	// A hub that cannot list models degrades split mode to Claude models.
	wrong := fakeInferenceHub(t, "other-token", twoModels)
	got, err = newLaunchTap(t, wrong.URL, "").ResolveLaunch(LaunchOptions{Harness: "claude"})
	require.NoError(t, err)
	require.Equal(t, []string{"claude"}, got.Argv)
	require.Contains(t, strings.Join(got.Warnings, "\n"), "Claude models only")
}

func TestResolveLaunch_ClaudeSubscription(t *testing.T) {
	t.Parallel()
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	got, err := newLaunchTap(t, hub.URL, "").ResolveLaunch(LaunchOptions{Harness: "claude", Inference: LaunchInferenceSubscription, Model: "@me/llama3"})
	require.NoError(t, err)
	require.Equal(t, []string{"claude"}, got.Argv)
	require.NotContains(t, got.Env, "ANTHROPIC_BASE_URL")
	require.Contains(t, strings.Join(got.Warnings, "\n"), "is a Hub model")

	_, err = newLaunchTap(t, hub.URL, "").ResolveLaunch(LaunchOptions{Harness: "codex", Inference: LaunchInferenceSubscription})
	require.ErrorContains(t, err, "Hub models only")
	_, err = newLaunchTap(t, hub.URL, "").ResolveLaunch(LaunchOptions{Harness: "claude", Inference: "both"})
	require.ErrorContains(t, err, "unknown inference mode")
}

func TestIsClaudeModel(t *testing.T) {
	t.Parallel()
	for _, m := range []string{"claude-sonnet-5-5", "claude-opus-5-5[1m]", "sonnet", "Opus", "haiku", "default"} {
		require.True(t, IsClaudeModel(m), m)
	}
	for _, m := range []string{"", "@me/qwen3:8b", "@me/claude-distill", "relay/ollama/llama3", "gpt-5"} {
		require.False(t, IsClaudeModel(m), m)
	}
}

// Under the pinned prefix the forwarder routes by model: Claude models reach
// Anthropic with the harness's own credential, Hub models reach Hub with the
// Hub credential, and neither credential crosses to the other side.
func TestLaunchForwarder_Split(t *testing.T) {
	t.Parallel()
	type seen struct{ path, auth, apiKey, beta, body string }
	anthropicSaw := make(chan seen, 4)
	anthropic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		anthropicSaw <- seen{r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"), r.Header.Get("Anthropic-Beta"), string(body)}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Anthropic-Ratelimit-Unified-Status", "allowed")
		_, _ = io.WriteString(w, "data: {}\n\n")
	}))
	t.Cleanup(anthropic.Close)
	hub := fakeInferenceHub(t, "hub-token", twoModels)
	fw, err := startLaunchForwarderWith(launchForwarderConfig{hubURL: hub.URL, harness: "claude", token: func() string { return "hub-token" }, anthropic: anthropic.URL})
	require.NoError(t, err)
	t.Cleanup(func() { _ = fw.Close() })

	oauth := map[string]string{"Authorization": "Bearer sk-ant-oat-user", "Anthropic-Beta": "oauth-2025-04-20"}
	post := func(path, body string, headers map[string]string) *http.Response {
		req, err := http.NewRequest(http.MethodPost, fw.Origin()+path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	pinned := fw.PinnedPrefix()

	resp := post(pinned+"/anthropic/v1/messages", `{"model":"claude-sonnet-5-5","stream":true}`, oauth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "allowed", resp.Header.Get("Anthropic-Ratelimit-Unified-Status"), "Anthropic's headers reach Claude Code")
	got := <-anthropicSaw
	require.Equal(t, "/v1/messages", got.path)
	require.Equal(t, "Bearer sk-ant-oat-user", got.auth, "the user's own credential goes to Anthropic unchanged")
	require.Equal(t, "oauth-2025-04-20", got.beta)
	require.JSONEq(t, `{"model":"claude-sonnet-5-5","stream":true}`, got.body)

	resp = post(pinned+"/anthropic/v1/messages/count_tokens", `{"model":"sonnet"}`, map[string]string{"X-Api-Key": "sk-ant-api-user"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	got = <-anthropicSaw
	require.Equal(t, "/v1/messages/count_tokens", got.path)
	require.Equal(t, "sk-ant-api-user", got.apiKey)

	resp = post(pinned+"/anthropic/v1/messages", `{"model":"@me/qwen3:8b"}`, oauth)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	require.Contains(t, string(body), `"path":"/inference/anthropic/v1/messages"`)
	require.Contains(t, string(body), `"bearer":"hub-token"`, "Hub models carry the Hub credential, not the user's Claude login")
	require.NotContains(t, string(body), "sk-ant-oat-user")
	require.Empty(t, anthropicSaw, "a Hub model never reaches Anthropic")

	require.Equal(t, http.StatusNotFound, post("/t/wrong/anthropic/v1/messages", `{"model":"claude-sonnet-5-5"}`, oauth).StatusCode)
	require.Equal(t, http.StatusUnauthorized, post("/anthropic/v1/messages", `{"model":"claude-sonnet-5-5"}`, oauth).StatusCode,
		"outside the prefix the launch key is still required")
	require.Equal(t, http.StatusNotFound, post(pinned+"/v1/other", `{}`, oauth).StatusCode, "the prefix opens no new Hub routes")
	require.Empty(t, anthropicSaw)

	// Without split routing the prefix still authorizes Hub routes only.
	plain, err := startLaunchForwarder(hub.URL, "claude", func() string { return "hub-token" })
	require.NoError(t, err)
	t.Cleanup(func() { _ = plain.Close() })
	req, _ := http.NewRequest(http.MethodPost, plain.Origin()+plain.PinnedPrefix()+"/anthropic/v1/messages", strings.NewReader(`{"model":"claude-sonnet-5-5"}`))
	r, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = r.Body.Close()
	require.Equal(t, http.StatusOK, r.StatusCode)
	require.Empty(t, anthropicSaw, "no anthropic upstream, nothing goes there")
}

// Relayed tools come from the plugin's `tap mcp`, so the forwarder no longer
// serves Hub's /mcp/relay, even under the pinned prefix.
func TestLaunchForwarder_NoRelayMCP(t *testing.T) {
	t.Parallel()
	var hubSaw []string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hubSaw = append(hubSaw, r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(hub.Close)
	fw, err := startLaunchForwarderWith(launchForwarderConfig{hubURL: hub.URL, harness: "claude", token: func() string { return "hub-token" }})
	require.NoError(t, err)
	t.Cleanup(func() { _ = fw.Close() })

	resp, err := http.Post(fw.Origin()+fw.PinnedPrefix()+"/mcp/relay", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Empty(t, hubSaw, "nothing reaches Hub's relay endpoint through the forwarder")
}

// An agent's subagents become Claude Code subagents limited to their own
// tools; relayed tools carry the plugin's prefix, since `tap mcp` serves them.
func TestResolveLaunch_ClaudeAgentExtras(t *testing.T) {
	t.Parallel()
	hub := fakeAgentHub(t, "hub-token", twoModels, map[string]string{
		"/api/v1/tools":       `{"tools":["keg_search","node_read"],"always_available":["orient"]}`,
		"/api/v1/relay/tools": `{"data":[{"name":"mcp__me__everything__echo"}]}`,
		"/api/v1/@me/agents/lead": `{"ref":"@me/lead","namespace":"me","name":"lead","instructions":"Lead.","model":"@me/llama3",
			"tools":["relay:tools"],"runtime_tools":[],"relay_tools":true,"subagents":["@me/reader","@me/open","@me/gone"]}`,
		"/api/v1/@me/agents/reader": `{"ref":"@me/reader","namespace":"me","name":"reader","description":"Reads notes.","instructions":"Read.",
			"model":"@me/qwen3:8b","runtime_tools":["node_read","orient"],"relay_tools":true}`,
		"/api/v1/@me/agents/open": `{"ref":"@me/open","namespace":"me","name":"open","title":"Open","model":"@other/missing","runtime_tools":[]}`,
	})
	got, err := newLaunchTap(t, hub.URL, "").ResolveLaunch(LaunchOptions{Harness: "claude", Agent: "@me/lead"})
	require.NoError(t, err)
	require.Contains(t, strings.Join(got.Warnings, "\n"), "subagent @me/gone skipped")

	require.NotContains(t, got.Argv, "--mcp-config", "relayed tools come from the plugin's tap mcp")

	var agentsJSON string
	for j, a := range got.Argv {
		if a == "--agents" {
			agentsJSON = got.Argv[j+1]
		}
	}
	var subagents map[string]claudeSubagent
	require.NoError(t, json.Unmarshal([]byte(agentsJSON), &subagents))
	require.Len(t, subagents, 2)
	reader := subagents["me-reader"]
	require.Equal(t, "Reads notes.", reader.Description)
	require.Equal(t, "Read.", reader.Prompt)
	require.Equal(t, "@me/qwen3:8b", reader.Model, "a Hub model is usable in split mode")
	require.Equal(t, []string{"Read", "Grep", "Glob",
		"mcp__plugin_tapper_tapper__node_read", "mcp__plugin_tapper_tapper__orient",
		"mcp__plugin_tapper_tapper__mcp__me__everything__echo"}, reader.Tools)
	open := subagents["me-open"]
	require.Equal(t, "Open", open.Description)
	require.Empty(t, open.Model, "a model the launch cannot reach is left to inherit")
	require.Equal(t, []string{"Read", "Grep", "Glob",
		"mcp__plugin_tapper_tapper__keg_search", "mcp__plugin_tapper_tapper__node_read", "mcp__plugin_tapper_tapper__orient"}, open.Tools,
		"every Hub tool, and no relayed tool without relay_tools")

	got, err = newLaunchTap(t, hub.URL, "").ResolveLaunch(LaunchOptions{Harness: "claude", Agent: "@me/lead", Subagents: "none", SubagentBuiltins: []string{}})
	require.NoError(t, err)
	require.NotContains(t, got.Argv, "--agents")
	noHubToken(t, got)
}
