package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapapi"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// --- node_search ---

type nodeSearchInput struct {
	Query  string   `json:"query" jsonschema:"What to find: plain terms (any may match; more matches, and matches in titles or metadata, rank higher), \"quoted phrases\" that must appear, and -exclusions."`
	Kegs   []string `json:"kegs,omitempty" jsonschema:"KEGs to search, as @namespace/keg references (at most 50), each inside the call's flight cover. Omit it or pass [] to search every KEG the flight covers."`
	Cursor string   `json:"cursor,omitempty" jsonschema:"The next_cursor of a previous node_search response for the same query, passed back unchanged."`
	Limit  int      `json:"limit,omitempty" jsonschema:"Maximum hits to return; 0 or omitted lets the Hub choose. Unlike listing tools, -1 is not accepted."`
}

type nodeSearchOutput struct {
	tapapi.SearchResponse
	Pagination string `json:"pagination" jsonschema:"How to page this search."`
}

func registerNodeSearch(srv *sdkmcp.Server, defaults KegDefaults, provider NodeSearchProvider) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "node_search",
		Description: "Memory: Find the notes about a topic: KEG nodes ranked by relevance to a query, across every KEG the call's flight covers or only the kegs you name. " +
			"It matches titles, metadata, tags, and bodies, and is the only search over note content: explore_kegs finds public KEGs, and keg_list lists your own. " +
			"Use node_grep for exact regex matches of identifiers or symbols. " +
			"Each hit gives the node reference, score, title, matched fields, and a snippet with matches in **bold**; " +
			"follow next_cursor for more, and use node_read for complete bodies.",
		OutputSchema: outputSchema[nodeSearchOutput](),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in nodeSearchInput) (*sdkmcp.CallToolResult, any, error) {
		// The scope is the call's flight cover, narrowed by kegs; never the
		// server's default KEG. Only a pre-kegs caller's keg argument names
		// one KEG through the keg target (see legacySearchKeg).
		target := resolveKegTarget(ctx, "", defaults)
		target.Keg = legacySearchKeg(ctx)
		opts := tapper.SearchOptions{
			KegTargetOptions: target,
			Kegs:             in.Kegs,
			Query:            in.Query,
			Cursor:           in.Cursor,
			Limit:            in.Limit,
		}
		kegs, err := opts.Validate()
		if err != nil {
			return errorResult(err), nil, nil
		}
		// Naming KEGs narrows the flight's search and never reaches past its
		// cover. Refusing here names every KEG outside it, the same way on
		// every Hub, instead of the Hub reporting the first as not found.
		if err := tapper.CheckSearchCover(tapper.ComposedFlight(target.FlightContext), kegs); err != nil {
			return errorResult(err), nil, nil
		}
		var resp *tapapi.SearchResponse
		if sessionReachesNothing(ctx) {
			// Failed-root recovery has no flight snapshot yet reaches no KEG; an
			// unscoped request would ask the Hub for everything.
			resp = &tapapi.SearchResponse{Hits: []tapapi.SearchHit{}}
		} else {
			resp, err = provider.SearchNodes(ctx, opts)
			if err != nil {
				return errorResult(err), nil, nil
			}
		}
		if resp.Hits == nil {
			resp.Hits = []tapapi.SearchHit{}
		}
		res := linesResult(nodeSearchLines(resp))
		pagination := "This is the last page."
		if resp.NextCursor != "" {
			pagination = "Repeat the same query with next_cursor as cursor for the next page."
		}
		res.StructuredContent = nodeSearchOutput{SearchResponse: *resp, Pagination: pagination}
		return res, nil, nil
	})
}

// sessionReachesNothing reports the governed state with no flight snapshot
// and no full access: failed-root recovery, which may reach no KEG.
func sessionReachesNothing(ctx context.Context) bool {
	return HasSessionOrientation(ctx) && SessionFlight(ctx) == nil && !SessionFullAccess(ctx)
}

// nodeSearchLines renders warnings, one line per hit, then the next page's
// cursor when there is one.
func nodeSearchLines(resp *tapapi.SearchResponse) []string {
	lines := make([]string, 0, len(resp.Warnings)+len(resp.Hits)+1)
	for _, warning := range resp.Warnings {
		lines = append(lines, "Warning: "+tsvField(warning))
	}
	lines = append(lines, tapper.SearchHitLines(resp.Hits, "**", "**")...)
	if len(resp.Hits) == 0 {
		lines = append(lines, "No matching nodes.")
	}
	if resp.NextCursor != "" {
		// Text-only clients page from this line; structured clients read
		// next_cursor.
		lines = append(lines, tapper.SearchNextCursorLine(resp.NextCursor))
	}
	return lines
}
