package mcp

import (
	"context"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapapi"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// --- node_search ---

type nodeSearchInput struct {
	Query  string   `json:"query" jsonschema:"required,what to find: plain terms (any may match; more matches and title or metadata matches rank higher), \"quoted phrases\" that must appear, and -exclusions"`
	Keg    string   `json:"keg,omitempty" jsonschema:"search only this KEG (@namespace/keg or alias). Omit to search every KEG this call may read; never combined with kegs"`
	Kegs   []string `json:"kegs,omitempty" jsonschema:"narrow the search to these @namespace/keg references; omit to search every KEG this call may read"`
	Cursor string   `json:"cursor,omitempty" jsonschema:"next_cursor from a previous node_search response for the same query, passed back unchanged"`
	Limit  int      `json:"limit,omitempty" jsonschema:"maximum hits to return; omit to let the Hub choose its default"`
}

type nodeSearchOutput struct {
	tapapi.SearchResponse
	Pagination string `json:"pagination"`
}

func registerNodeSearch(srv *sdkmcp.Server, defaults KegDefaults, provider NodeSearchProvider) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "node_search",
		Description: "Find the KEG nodes most relevant to a query, ranked across every KEG this call may read, one named keg, or the listed kegs. " +
			"Matches titles, metadata, tags, and bodies. Use node_grep for exact regex matching of identifiers or symbols. " +
			"Each hit gives the node reference, score, title, matched fields, and a snippet with matches in **bold**; " +
			"follow next_cursor for more and use node_read for complete bodies.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in nodeSearchInput) (*sdkmcp.CallToolResult, any, error) {
		target := resolveKegTarget(ctx, in.Keg, defaults)
		// An omitted keg means the whole readable scope, never the server's
		// default KEG.
		target.Keg = strings.TrimSpace(in.Keg)
		opts := tapper.SearchOptions{
			KegTargetOptions: target,
			Kegs:             in.Kegs,
			Query:            in.Query,
			Cursor:           in.Cursor,
			Limit:            in.Limit,
		}
		if _, err := opts.Validate(); err != nil {
			return errorResult(err), nil, nil
		}
		var resp *tapapi.SearchResponse
		if sessionReachesNothing(ctx) {
			// Failed-root recovery has no flight snapshot yet reaches no KEG; an
			// unscoped request would ask the Hub for everything.
			resp = &tapapi.SearchResponse{Hits: []tapapi.SearchHit{}}
		} else {
			var err error
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

// nodeSearchLines renders warnings, then one line per hit.
func nodeSearchLines(resp *tapapi.SearchResponse) []string {
	lines := make([]string, 0, len(resp.Warnings)+len(resp.Hits)+1)
	for _, warning := range resp.Warnings {
		lines = append(lines, "Warning: "+tsvField(warning))
	}
	lines = append(lines, tapper.SearchHitLines(resp.Hits, "**", "**")...)
	if len(resp.Hits) == 0 {
		lines = append(lines, "No matching nodes.")
	}
	return lines
}
