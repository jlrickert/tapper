package tapper

import (
	"encoding/json"
	"fmt"
	"github.com/jlrickert/tapper/pkg/apicontract"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAgentHub is fakeInferenceHub that also answers REST API discovery and
// serves routes (path → JSON body) for the Hub agent lookups a launch makes.
func fakeAgentHub(t *testing.T, token string, models string, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/version" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(apicontract.Discovery{ServerVersion: "hub-test", APIVersions: []string{apicontract.Revision}})
			return
		}
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
			if body, ok := routes[r.URL.EscapedPath()]; ok {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

const researcherAgent = `{"ref":"@me/researcher","namespace":"me","name":"researcher","title":"Researcher",
  "description":"Finds and cites sources.","instructions":"Cite your sources.",
  "model":"@me/llama3","tools":["node_read","node_search"],"flight":"@me/+work"}`

// The agent is the unit of authority: it brings its model, its instructions
// (as an opencode primary agent), TAP_AGENT so the child's tap mcp serves only
// its tools, and its memory flight as the launch root.
func TestResolveLaunch_HubAgentBringsItsFlight(t *testing.T) {
	t.Parallel()
	hub := fakeAgentHub(t, "hub-token", twoModels, map[string]string{"/api/v1/@me/agents/researcher": researcherAgent})
	tap := newLaunchTap(t, hub.URL, "")

	got, err := tap.ResolveLaunch(LaunchOptions{Harness: "opencode", Agent: "@me/researcher"})
	require.NoError(t, err)
	require.Equal(t, "@me/researcher", got.HubAgent)
	require.Equal(t, "@me/llama3", got.Model)
	require.Equal(t, "@me/researcher", got.Env["TAP_AGENT"])
	require.Equal(t, "@me/+work", got.Env["TAP_FLIGHT"])
	require.Equal(t, "@me/+work", got.Flight)
	require.Equal(t, []string{"opencode", "--model", "foldwise/@me/llama3", "--agent", "me-researcher"}, got.Argv)

	var cfg struct {
		Agent map[string]struct {
			Mode, Model, Prompt, Description string
		} `json:"agent"`
	}
	require.NoError(t, json.Unmarshal([]byte(got.Env["OPENCODE_CONFIG_CONTENT"]), &cfg))
	agent := cfg.Agent["me-researcher"]
	require.Equal(t, "primary", agent.Mode)
	require.Equal(t, "foldwise/@me/llama3", agent.Model)
	require.Equal(t, "Cite your sources.", agent.Prompt)
}

func TestResolveLaunch_HubAgentSelection(t *testing.T) {
	t.Parallel()
	hub := fakeAgentHub(t, "hub-token", twoModels, map[string]string{
		"/api/v1/@me/agents/researcher": researcherAgent,
		"/api/v1/@me/agents/admin":      `{"ref":"@me/admin","namespace":"me","name":"admin","model":"","tools":[]}`,
	})
	tap := newLaunchTap(t, hub.URL, "")

	// An explicit flight overrides the agent's own.
	got, err := tap.ResolveLaunch(LaunchOptions{Harness: "opencode", Flight: "@me/+other", Agent: "@me/researcher"})
	require.NoError(t, err)
	require.Equal(t, "@me/researcher", got.HubAgent)
	require.Equal(t, "@me/+other", got.Env["TAP_FLIGHT"])

	// An agent with no model and no flight runs the first catalog model with
	// no launch root.
	got, err = tap.ResolveLaunch(LaunchOptions{Harness: "opencode", Agent: "@me/admin"})
	require.NoError(t, err)
	require.Equal(t, "@me/admin", got.HubAgent)
	require.Equal(t, "@me/qwen3:8b", got.Model)
	require.Empty(t, got.Flight)
	require.Contains(t, strings.Join(got.Warnings, " "), "KEG tools remain locked")
	require.NotContains(t, strings.Join(got.Warnings, " "), "full access")

	// An unknown agent is refused.
	_, err = tap.ResolveLaunch(LaunchOptions{Harness: "opencode", Agent: "@me/stranger"})
	require.ErrorContains(t, err, "agent @me/stranger")

	// A flight alone launches a bare catalog model, with no agent.
	got, err = tap.ResolveLaunch(LaunchOptions{Harness: "opencode", Flight: "@me/+work"})
	require.NoError(t, err)
	require.Empty(t, got.HubAgent)
	require.NotContains(t, got.Env, "TAP_AGENT")

	// --model still picks a bare catalog model on a flight.
	got, err = tap.ResolveLaunch(LaunchOptions{Harness: "opencode", Flight: "@me/+work", Model: "@me/qwen3:8b"})
	require.NoError(t, err)
	require.Empty(t, got.HubAgent)
}

func TestResolveLaunch_HubAgentEveryHarness(t *testing.T) {
	hub := fakeAgentHub(t, "hub-token", twoModels, map[string]string{"/api/v1/@me/agents/researcher": researcherAgent})
	for _, harness := range LaunchHarnesses() {
		t.Run(harness, func(t *testing.T) {
			got, err := newLaunchTap(t, hub.URL, "").ResolveLaunch(LaunchOptions{Harness: harness, Agent: "@me/researcher"})
			require.NoError(t, err)
			require.Equal(t, "@me/researcher", got.Env["TAP_AGENT"])
			require.Equal(t, "@me/+work", got.Flight)
			require.Equal(t, "@me/llama3", got.Model)
			require.Contains(t, strings.Join(got.Argv, " ")+got.Env["OPENCODE_CONFIG_CONTENT"], "Cite your sources.")
			noHubToken(t, got)
		})
	}
}
