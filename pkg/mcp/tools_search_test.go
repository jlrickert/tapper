package mcp_test

import (
	"context"
	"sync"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapapi"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// recordingNodeSearch is a hosted NodeSearchProvider that records each call.
type recordingNodeSearch struct {
	mu    sync.Mutex
	calls []tapper.SearchOptions
	resp  *tapapi.SearchResponse
	err   error
}

func (r *recordingNodeSearch) SearchNodes(_ context.Context, opts tapper.SearchOptions) (*tapapi.SearchResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, opts)
	return r.resp, r.err
}

func (r *recordingNodeSearch) recorded() []tapper.SearchOptions {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]tapper.SearchOptions(nil), r.calls...)
}

func callNodeSearch(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "node_search", Arguments: args})
	require.NoError(t, err)
	return res
}

func TestMCP_NodeSearchUsesTheInjectedProvider(t *testing.T) {
	t.Parallel()
	provider := &recordingNodeSearch{resp: &tapapi.SearchResponse{
		Hits: []tapapi.SearchHit{{
			Ref: "keg:@acme/notes/12", Keg: "@acme/notes", NodeID: "12", Title: "Festival trip", Score: 4.25,
			Fields: []string{tapapi.SearchFieldTitle}, Sources: []string{tapapi.SearchSourceSemantic},
			Snippet: "festival in Los Angeles", Highlights: []tapapi.Span{{Start: 0, End: 8}},
		}},
		NextCursor: "c2", Mode: tapapi.SearchModeHybrid, Warnings: []string{"partial"},
	}}
	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{NodeSearchProvider: provider})

	res := callNodeSearch(t, session, ctx, map[string]any{
		"query": "festival", "kegs": []string{"@acme/notes"}, "cursor": " c1 ", "limit": 7,
	})
	require.False(t, res.IsError, "node_search failed: %v", extractText(t, res))

	calls := provider.recorded()
	require.Len(t, calls, 1)
	require.Equal(t, "festival", calls[0].Query)
	require.Equal(t, []string{"@acme/notes"}, calls[0].Kegs)
	require.Equal(t, " c1 ", calls[0].Cursor, "the cursor is opaque and passed through unchanged")
	require.Equal(t, 7, calls[0].Limit)
	require.Empty(t, calls[0].Keg, "an omitted keg searches the readable scope, not the default KEG")
	require.False(t, calls[0].BypassFlightRestrictions, "MCP searches stay inside the flight's cover")

	text := extractText(t, res)
	require.Contains(t, text, "Warning: partial")
	require.Contains(t, text, "keg:@acme/notes/12\t4.250\tFestival trip\t**festival** in Los Angeles")

	payload, ok := res.StructuredContent.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "c2", payload["next_cursor"])
	require.Equal(t, tapapi.SearchModeHybrid, payload["mode"])
	require.Contains(t, payload["pagination"], "next_cursor")
	hits, ok := payload["hits"].([]any)
	require.True(t, ok)
	require.Len(t, hits, 1)
	hit := hits[0].(map[string]any)
	require.Equal(t, []any{tapapi.SearchSourceSemantic}, hit["sources"])
}

func TestMCP_NodeSearchOmittedLimitStaysZero(t *testing.T) {
	t.Parallel()
	provider := &recordingNodeSearch{resp: &tapapi.SearchResponse{}}
	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{NodeSearchProvider: provider})

	res := callNodeSearch(t, session, ctx, map[string]any{"query": "festival", "keg": "@acme/notes"})
	require.False(t, res.IsError, "node_search failed: %v", extractText(t, res))
	calls := provider.recorded()
	require.Len(t, calls, 1)
	require.Zero(t, calls[0].Limit, "the Hub owns the default page size")
	require.Equal(t, "@acme/notes", calls[0].Keg)

	payload := res.StructuredContent.(map[string]any)
	require.Equal(t, []any{}, payload["hits"], "a nil hit list renders as an empty array")
	require.Contains(t, extractText(t, res), "No matching nodes.")
	require.Equal(t, "This is the last page.", payload["pagination"])
}

func TestMCP_NodeSearchRefusesInvalidInputBeforeTheProvider(t *testing.T) {
	t.Parallel()
	provider := &recordingNodeSearch{resp: &tapapi.SearchResponse{}}
	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{NodeSearchProvider: provider})

	for name, args := range map[string]map[string]any{
		"empty query":    {"query": "  "},
		"negative limit": {"query": "x", "limit": -1},
		"bare kegs ref":  {"query": "x", "kegs": []string{"notes"}},
		"keg and kegs":   {"query": "x", "keg": "@acme/notes", "kegs": []string{"@acme/eng"}},
	} {
		res := callNodeSearch(t, session, ctx, args)
		require.True(t, res.IsError, name)
		require.Equal(t, keg.RemoteCodeInvalid, res.StructuredContent.(map[string]any)["code"], name)
	}
	require.Empty(t, provider.recorded())
}

func TestMCP_NodeSearchUnavailableNamesNodeGrep(t *testing.T) {
	t.Parallel()
	provider := &recordingNodeSearch{err: tapper.ErrRankedSearchUnavailable}
	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{NodeSearchProvider: provider})

	res := callNodeSearch(t, session, ctx, map[string]any{"query": "x"})
	require.True(t, res.IsError)
	require.Contains(t, extractText(t, res), "ranked search unavailable")
}

func TestMCP_NodeGrepKeepsRegexBehavior(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "node_grep", Arguments: map[string]any{"query": "Hel+o", "id_only": true}})
	require.NoError(t, err)
	require.False(t, res.IsError, "node_grep failed: %v", extractText(t, res))
	require.NotEmpty(t, extractText(t, res))
}

func newFlightSearchSession(t *testing.T, mode string, provider mcp.NodeSearchProvider) (*sdkmcp.ClientSession, context.Context) {
	t.Helper()
	backend := newRefreshFlightBackend(mode)
	ctx := context.Background()
	sandbox := newTestSandbox(t)
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sandbox.Runtime()})
	require.NoError(t, err)
	server := mcp.NewServer(tap, "test", mcp.KegDefaults{}, mcp.ServerOptions{
		OrientationProvider: backend, FlightProvider: backend, KegProvider: backend,
		KegSearchProvider: backend, IdentityProvider: backend, NodeSearchProvider: provider,
	})
	return connectFlightSession(t, ctx, server, nil), ctx
}

func TestMCP_NodeSearchCarriesTheCallFlight(t *testing.T) {
	t.Parallel()
	provider := &recordingNodeSearch{resp: &tapapi.SearchResponse{}}
	session, ctx := newFlightSearchSession(t, "active", provider)

	res := callNodeSearch(t, session, ctx, map[string]any{"query": "release"})
	require.False(t, res.IsError, "node_search failed: %v", extractText(t, res))
	calls := provider.recorded()
	require.Len(t, calls, 1)
	require.NotNil(t, calls[0].FlightContext, "the provider must see the flight it searches within")
	require.Equal(t, "@team/+root", calls[0].FlightContext.Name)
}

func TestMCP_NodeSearchReachesNothingWithoutAuthority(t *testing.T) {
	t.Parallel()
	provider := &recordingNodeSearch{resp: &tapapi.SearchResponse{Hits: []tapapi.SearchHit{{Ref: "keg:@team/x/1"}}}}
	session, ctx := newFlightSearchSession(t, "failed", provider)

	res := callNodeSearch(t, session, ctx, map[string]any{"query": "release"})
	if !res.IsError {
		require.Equal(t, []any{}, res.StructuredContent.(map[string]any)["hits"])
	}
	require.Empty(t, provider.recorded(), "a session that reaches no KEG must not search the Hub")
}
