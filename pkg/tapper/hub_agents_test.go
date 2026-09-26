package tapper_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/tapper"
)

func TestHubAgents_ClientPaths(t *testing.T) {
	t.Parallel()

	var seen []string
	var wrote tapper.HubAgentWrite
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		agent := tapper.HubAgent{Ref: "@acme/writer", Namespace: "acme", Name: "writer", Tools: []string{"keg:read"}, EffectiveTools: []string{"node_read", "node_list"}}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/agents", "GET /api/v1/@acme/agents":
			_ = json.NewEncoder(w).Encode([]tapper.HubAgent{agent})
		case "GET /api/v1/@acme/agents/writer":
			_ = json.NewEncoder(w).Encode(agent)
		case "POST /api/v1/@acme/agents", "PUT /api/v1/@acme/agents/writer":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&wrote))
			_ = json.NewEncoder(w).Encode(agent)
		case "DELETE /api/v1/@acme/agents/writer":
			w.WriteHeader(http.StatusNoContent)
		case "POST /api/v1/namespaces/search":
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "ac", body["query"])
			_ = json.NewEncoder(w).Encode(tapper.NamespaceSearchResult{Namespaces: []tapper.NamespaceMatch{{Name: "acme", Kind: "org"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	all, err := tapper.ListHubAgents(ctx, srv.URL, "tok", "")
	require.NoError(t, err)
	require.Equal(t, []string{"node_read", "node_list"}, all[0].ToolNames(), "effective tools win over stored groups")
	_, err = tapper.ListHubAgents(ctx, srv.URL, "tok", "acme")
	require.NoError(t, err)

	_, err = tapper.CreateHubAgent(ctx, srv.URL, "tok", "acme", tapper.HubAgentWrite{Name: "writer", Tools: []string{"keg:read"}, Flight: "@acme/+docs"})
	require.NoError(t, err)
	require.Equal(t, "writer", wrote.Name)
	require.Equal(t, "@acme/+docs", wrote.Flight, "the agent's memory flight travels on writes")
	_, err = tapper.UpdateHubAgent(ctx, srv.URL, "tok", "acme", "writer", tapper.HubAgentWrite{Name: "writer", Title: "Scribe"})
	require.NoError(t, err)
	require.Equal(t, "Scribe", wrote.Title)
	require.NoError(t, tapper.DeleteHubAgent(ctx, srv.URL, "tok", "acme", "writer"))

	found, err := tapper.SearchHubNamespaces(ctx, srv.URL, "tok", "ac")
	require.NoError(t, err)
	require.Equal(t, "acme", found.Namespaces[0].Name)

	require.Equal(t, []string{
		"GET /api/v1/agents", "GET /api/v1/@acme/agents", "POST /api/v1/@acme/agents",
		"PUT /api/v1/@acme/agents/writer", "DELETE /api/v1/@acme/agents/writer", "POST /api/v1/namespaces/search",
	}, seen)

	// An older hub sends no effective tools; the stored names are the list.
	require.Equal(t, []string{"node_read"}, tapper.HubAgent{Tools: []string{"node_read"}}.ToolNames())
}
