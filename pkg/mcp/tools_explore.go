package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// ExploreProvider searches the hub's public directory. It never sees the
// caller's private KEGs or flights: those are listed by keg_list and
// flight_list, never searched.
type ExploreProvider interface {
	// Explore returns a bounded page of public matches of one kind.
	Explore(ctx context.Context, kind tapper.ExploreKind, query string) (tapper.ExploreResult, error)
}

type localExploreProvider struct{ tap *tapper.Tap }

func (p localExploreProvider) Explore(ctx context.Context, kind tapper.ExploreKind, query string) (tapper.ExploreResult, error) {
	opts := tapper.ExploreOptions{Query: query}
	switch kind {
	case tapper.ExploreKegs:
		return p.tap.ExploreKegs(ctx, opts)
	case tapper.ExploreFlights:
		return p.tap.ExploreFlights(ctx, opts)
	case tapper.ExplorePeople:
		return p.tap.ExplorePeople(ctx, opts)
	}
	return tapper.ExploreResult{}, fmt.Errorf("%w: unknown explore kind %q", keg.ErrInvalid, kind)
}

type exploreInput struct {
	Query string `json:"query" jsonschema:"literal text to match against public names, references, and descriptions"`
}

// registerExploreTools exposes the public directory, at parity with
// `tap explore kegs|flights|people`. These are the only search tools over
// resources rather than note content; everything the caller already holds is
// listed instead.
func registerExploreTools(srv *sdkmcp.Server, explore ExploreProvider) {
	tools := []struct {
		name string
		kind tapper.ExploreKind
		desc string
	}{
		{
			name: "explore_kegs", kind: tapper.ExploreKegs,
			desc: "Explore: search the hub's public KEGs by reference, title, or description. " +
				"Discovery only: a result grants no access, and a KEG outside the flight still needs a flight that covers it. " +
				"Your own KEGs come from keg_list; notes come from node_search, never from here.",
		},
		{
			name: "explore_flights", kind: tapper.ExploreFlights,
			desc: "Explore: search the hub's public flights by reference, title, or description. " +
				"Discovery only: returns metadata, never instructions, and grants no access. Your own flights come from flight_list.",
		},
		{
			name: "explore_people", kind: tapper.ExplorePeople,
			desc: "Explore: search the people and org namespaces on the hub by name or display name. " +
				"Discovery only: role is set where you already belong. Your own namespaces come from namespace_list.",
		},
	}
	for _, tool := range tools {
		kind := tool.kind
		sdkmcp.AddTool(srv, &sdkmcp.Tool{
			Name:        tool.name,
			Description: tool.desc,
			Annotations: &sdkmcp.ToolAnnotations{
				ReadOnlyHint:  true,
				OpenWorldHint: boolPtr(true),
			},
		}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in exploreInput) (*sdkmcp.CallToolResult, any, error) {
			query := strings.TrimSpace(in.Query)
			if query == "" {
				return errorResult(fmt.Errorf("%w: query must not be empty; list your own resources instead", keg.ErrInvalid)), nil, nil
			}
			found, err := explore.Explore(ctx, kind, query)
			if err != nil {
				return errorResult(err), nil, nil
			}
			lines := make([]string, 0, len(found.Results)+1)
			if found.Truncated {
				lines = append(lines, "Results truncated; refine the query.")
			}
			for _, row := range found.Results {
				lines = append(lines, strings.Join([]string{
					row.Ref, tsvField(row.Name), tsvField(row.Description), row.Role,
				}, "\t"))
			}
			res := linesResult(lines)
			res.StructuredContent = found
			return res, nil, nil
		})
	}
}
