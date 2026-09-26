package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestAgentCLI_CRUDAndCompletion(t *testing.T) {
	var mu sync.Mutex
	var saved *tapper.HubAgent
	hub := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, "Bearer hub-token", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v1/agents":
			rows := []tapper.HubAgent{}
			if saved != nil {
				rows = append(rows, *saved)
			}
			_ = json.NewEncoder(w).Encode(rows)
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/@me/agents/"):
			if saved == nil {
				http.NotFound(w, r)
				return
			}
			_ = json.NewEncoder(w).Encode(saved)
		case r.Method == "POST" || r.Method == "PUT":
			var in tapper.HubAgentWrite
			require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
			saved = &tapper.HubAgent{Ref: "@me/reader", Namespace: "me", Name: in.Name, Title: in.Title, Model: in.Model, Tools: in.Tools, Flight: in.Flight}
			_ = json.NewEncoder(w).Encode(saved)
		case r.Method == "DELETE":
			saved = nil
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer hub.Close()
	sb := NewSandbox(t)
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(fmt.Sprintf("hub: test\nhubs:\n  test: {url: %s, token: hub-token}\n", hub.URL)), 0600))
	run := func(args ...string) string {
		t.Helper()
		res := NewProcess(t, false, args...).Run(sb.Context(), sb.Runtime())
		require.NoError(t, res.Err)
		return string(res.Stdout)
	}
	require.Contains(t, run("agent", "create", "@me/reader", "--model", "qwen3", "--tool", "node_read", "--flight", "@me/+notes"), "@me/reader")
	require.Contains(t, run("agent", "list"), "@me/reader")
	require.Contains(t, run("agent", "read", "@me/reader"), "qwen3")
	for _, words := range [][]string{{"agent", "read", "@me/r"}, {"agent", "edit", "@me/r"}, {"agent", "delete", "@me/r"}, {"launch", "claude", "--agent", "@me/r"}} {
		res := NewCompletionProcess(t, false, 0, words...).Run(sb.Context(), sb.Runtime())
		require.NoError(t, res.Err)
		require.Contains(t, parseCompletionSuggestions(string(res.Stdout)), "@me/reader")
	}
	run("agent", "edit", "@me/reader", "--title", "Reader")
	mu.Lock()
	require.Equal(t, "@me/+notes", saved.Flight)
	require.Equal(t, []string{"node_read"}, saved.Tools)
	mu.Unlock()
	run("agent", "delete", "@me/reader")
	require.Empty(t, run("agent", "list"))
}
