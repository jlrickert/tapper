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
	require.Contains(t, text, "next cursor: c2", "text-only clients need the cursor to page")

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

	res := callNodeSearch(t, session, ctx, map[string]any{"query": "festival", "kegs": []string{"@acme/notes"}})
	require.False(t, res.IsError, "node_search failed: %v", extractText(t, res))
	calls := provider.recorded()
	require.Len(t, calls, 1)
	require.Zero(t, calls[0].Limit, "the Hub owns the default page size")

	payload := res.StructuredContent.(map[string]any)
	require.Equal(t, []any{}, payload["hits"], "a nil hit list renders as an empty array")
	require.Contains(t, extractText(t, res), "No matching nodes.")
	require.NotContains(t, extractText(t, res), "next cursor", "the last page names no cursor")
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
		require.Equal(t, keg.RemoteCodeInvalid, errorPayload(t, res)["code"], name)
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
	payload := errorPayload(t, res)
	require.Equal(t, keg.RemoteCodeNotSupported, payload["code"], "a missing capability is not an unknown failure")
	require.Equal(t, false, payload["operationPerformed"], "a search never writes")
	require.Contains(t, payload["action"], "supported tool")
}

func TestMCP_NodeGrepKeepsRegexBehavior(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "node_grep", Arguments: map[string]any{"query": "Hel+o", "format": "%i"}})
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
		ExploreProvider: backend, IdentityProvider: backend, NodeSearchProvider: provider,
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

// node_search scopes to the call's flight: kegs only narrows it. One kegs
// list replaced the old keg/kegs pair, so the schema names no keg.
func TestMCP_NodeSearchSearchesTheFlightCoverAndNarrowsWithinIt(t *testing.T) {
	t.Parallel()
	provider := &recordingNodeSearch{resp: &tapapi.SearchResponse{}}
	session, ctx := newFlightSearchSession(t, "active", provider)

	listed, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	for _, tool := range listed.Tools {
		if tool.Name != "node_search" {
			continue
		}
		props := schemaProperties(t, tool.InputSchema)
		require.Contains(t, props, "kegs")
		require.NotContains(t, props, "keg", "kegs is the one way to name KEGs")
	}

	// Empty or omitted kegs searches the whole cover of the call's flight.
	for _, args := range []map[string]any{
		{"query": "release"},
		{"query": "release", "kegs": []string{}},
	} {
		res := callNodeSearch(t, session, ctx, args)
		require.False(t, res.IsError, "node_search failed: %v", extractText(t, res))
	}
	// An explicit flight selects that flight's cover instead.
	res := callNodeSearch(t, session, ctx, map[string]any{"query": "release", "flight": "@team/+child"})
	require.False(t, res.IsError, "node_search failed: %v", extractText(t, res))
	calls := provider.recorded()
	require.Len(t, calls, 3)
	for i, call := range calls {
		require.Emptyf(t, call.Kegs, "call %d named KEGs; the whole cover is searched", i)
		require.Emptyf(t, call.Keg, "call %d named a single KEG", i)
	}
	require.Equal(t, "@team/+root", calls[0].FlightContext.Name)
	require.Equal(t, "@team/+child", calls[2].FlightContext.Name)

	// Naming KEGs inside the cover narrows to them.
	res = callNodeSearch(t, session, ctx, map[string]any{"query": "release", "kegs": []string{"@team/root-keg"}})
	require.False(t, res.IsError, "node_search failed: %v", extractText(t, res))
	require.Equal(t, []string{"@team/root-keg"}, provider.recorded()[3].Kegs)

	// Naming any outside it is refused, naming each, and searches nothing.
	res = callNodeSearch(t, session, ctx, map[string]any{"query": "release", "kegs": []string{"@team/root-keg", "@team/child-keg", "@other/notes"}})
	require.True(t, res.IsError)
	require.Equal(t, keg.RemoteCodeInvalid, errorPayload(t, res)["code"])
	text := extractText(t, res)
	require.Contains(t, text, "@team/child-keg, @other/notes")
	require.NotContains(t, text, "root-keg")
	// The same KEG is in cover under the flight that covers it.
	res = callNodeSearch(t, session, ctx, map[string]any{"query": "release", "flight": "@team/+child", "kegs": []string{"@team/child-keg"}})
	require.False(t, res.IsError, "node_search failed: %v", extractText(t, res))
	require.Len(t, provider.recorded(), 5)
}

// A client written before kegs replaced keg still sends keg. It is searched
// as the one KEG named, bare names included, rather than refused by the
// schema.
func TestMCP_NodeSearchAcceptsLegacyKeg(t *testing.T) {
	t.Parallel()
	provider := &recordingNodeSearch{resp: &tapapi.SearchResponse{}}
	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{NodeSearchProvider: provider})

	for _, ref := range []string{"@acme/notes", "notes"} {
		res := callNodeSearch(t, session, ctx, map[string]any{"query": "festival", "keg": ref})
		require.False(t, res.IsError, "legacy keg %q: %v", ref, extractText(t, res))
	}
	calls := provider.recorded()
	require.Len(t, calls, 2)
	require.Equal(t, "@acme/notes", calls[0].Keg)
	require.Equal(t, "notes", calls[1].Keg)
	require.Empty(t, calls[0].Kegs)

	// An empty legacy keg asks for nothing narrower than the whole scope.
	res := callNodeSearch(t, session, ctx, map[string]any{"query": "festival", "keg": ""})
	require.False(t, res.IsError, extractText(t, res))
	require.Empty(t, provider.recorded()[2].Keg)
}
