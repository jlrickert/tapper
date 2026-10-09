package parity_test

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/tapapi"
)

// searchHubReply is what the fake Hub answers every ranked search with.
const searchHubReply = `{"hits":[{"ref":"keg:@acme/notes/12","keg":"@acme/notes","node_id":"12","title":"Festival trip","score":4.25,` +
	`"fields":["title"],"snippet":"festival in Los Angeles","highlights":[{"start":0,"end":8}]}],"mode":"keyword"}`

// newSearchParityEnv points the parity environment's Hub at a fake that
// records each ranked-search request body verbatim.
func newSearchParityEnv(t *testing.T) (*parityEnv, func() []string) {
	t.Helper()
	e := newParityEnv(t)
	var mu sync.Mutex
	var bodies []string
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1"+tapapi.PathSearch {
			http.NotFound(w, r)
			return
		}
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(searchHubReply))
	}))
	t.Cleanup(srv.Close)
	e.sb.MustWriteFile("~/.config/tapper/config.yaml", []byte(fmt.Sprintf("hub: home\nhubs:\n  home:\n    url: %s\n    token: test-token\n", srv.URL)), 0644)
	e.tap.ConfigService.Reload()
	return e, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := append([]string(nil), bodies...)
		bodies = nil
		return out
	}
}

// TestParity_NodeSearchRequest checks, parameter by parameter, that `tap node
// search` and node_search send the Hub the same SearchRequest and render the
// same hit lines.
func TestParity_NodeSearchRequest(t *testing.T) {
	e, drain := newSearchParityEnv(t)

	cases := []struct {
		name string
		cli  []string
		mcp  map[string]any
		want string
	}{
		{
			name: "query only omits limit, cursor, and kegs",
			cli:  []string{"node", "search", "festival"},
			mcp:  map[string]any{"query": "festival"},
			want: `{"query":"festival"}`,
		},
		{
			name: "multi-word query",
			cli:  []string{"node", "search", "--", "los", `"angeles trip"`, "-draft"},
			mcp:  map[string]any{"query": `los "angeles trip" -draft`},
			want: `{"query":"los \"angeles trip\" -draft"}`,
		},
		{
			name: "kegs",
			cli:  []string{"node", "search", "festival", "--kegs", "@acme/notes,@acme/eng"},
			mcp:  map[string]any{"query": "festival", "kegs": []string{"@acme/notes", "@acme/eng"}},
			want: `{"query":"festival","kegs":["@acme/notes","@acme/eng"]}`,
		},
		{
			name: "one keg",
			cli:  []string{"node", "search", "festival", "--keg", "@acme/notes"},
			mcp:  map[string]any{"query": "festival", "keg": "@acme/notes"},
			want: `{"query":"festival","kegs":["@acme/notes"]}`,
		},
		{
			name: "cursor passes through unchanged",
			cli:  []string{"node", "search", "festival", "--cursor", "opaque/c+2=="},
			mcp:  map[string]any{"query": "festival", "cursor": "opaque/c+2=="},
			want: `{"query":"festival","cursor":"opaque/c+2=="}`,
		},
		{
			name: "limit",
			cli:  []string{"node", "search", "festival", "--limit", "5"},
			mcp:  map[string]any{"query": "festival", "limit": 5},
			want: `{"query":"festival","limit":5}`,
		},
	}
	for _, tc := range cases {
		cliOut, err := e.runCLI(tc.cli...)
		require.NoError(t, err, tc.name)
		mcpOut, err := e.runMCP("node_search", tc.mcp)
		require.NoError(t, err, tc.name)

		bodies := drain()
		require.Len(t, bodies, 2, tc.name)
		require.JSONEq(t, tc.want, bodies[0], "%s: CLI request", tc.name)
		require.Equal(t, bodies[0], bodies[1], "%s: CLI and MCP must send the same SearchRequest", tc.name)

		require.Equal(t, "keg:@acme/notes/12\t4.250\tFestival trip\t**festival** in Los Angeles", cliOut, tc.name)
		require.Equal(t, cliOut, mcpOut, "%s: CLI and MCP must render hits the same way", tc.name)
	}
}

// TestParity_NodeSearchRejectsTheSameInput checks that both surfaces refuse
// the same invalid requests without contacting the Hub.
func TestParity_NodeSearchRejectsTheSameInput(t *testing.T) {
	e, drain := newSearchParityEnv(t)

	cases := []struct {
		name string
		cli  []string
		mcp  map[string]any
	}{
		{"negative limit", []string{"node", "search", "x", "--limit", "-1"}, map[string]any{"query": "x", "limit": -1}},
		{"bare keg ref", []string{"node", "search", "x", "--kegs", "notes"}, map[string]any{"query": "x", "kegs": []string{"notes"}}},
		{"keg and kegs", []string{"node", "search", "x", "--keg", "@acme/a", "--kegs", "@acme/b"}, map[string]any{"query": "x", "keg": "@acme/a", "kegs": []string{"@acme/b"}}},
	}
	for _, tc := range cases {
		_, cliErr := e.runCLI(tc.cli...)
		_, mcpErr := e.runMCP("node_search", tc.mcp)
		require.Error(t, cliErr, tc.name)
		require.Error(t, mcpErr, tc.name)
	}
	require.Empty(t, drain(), "invalid searches must not reach the Hub")
}
