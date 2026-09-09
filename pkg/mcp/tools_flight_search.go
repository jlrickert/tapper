package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// FlightCatalogProvider supplies an identity-filtered catalog without per-resource reads.
type FlightCatalogProvider interface {
	// FlightCatalog returns readable flights from one catalog projection.
	FlightCatalog(context.Context) ([]*tapper.Flight, error)
}

// FlightSearchRow is descriptive metadata and confers no authority.
type FlightSearchRow struct {
	Ref         string `json:"ref"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// FlightSearchResult is a bounded deterministic metadata result.
type FlightSearchResult struct {
	Flights   []FlightSearchRow `json:"flights"`
	Truncated bool              `json:"truncated"`
}

// SearchIdentityFlights matches a literal query over readable metadata.
func SearchIdentityFlights(rows []*tapper.Flight, query string) FlightSearchResult {
	out := FlightSearchResult{Flights: []FlightSearchRow{}}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return out
	}
	seen := map[string]bool{}
	for _, f := range rows {
		if f == nil || seen[f.Name] {
			continue
		}
		seen[f.Name] = true
		if !strings.Contains(strings.ToLower(f.Name+"\n"+f.Title+"\n"+f.Description), query) {
			continue
		}
		title := f.Title
		if strings.TrimSpace(title) == "" {
			title = f.Name
		}
		out.Flights = append(out.Flights, FlightSearchRow{Ref: f.Name, Title: title, Description: f.Description})
	}
	sort.Slice(out.Flights, func(i, j int) bool { return out.Flights[i].Ref < out.Flights[j].Ref })
	if len(out.Flights) > 50 {
		out.Truncated = true
		out.Flights = out.Flights[:50]
	}
	return out
}
func registerFlightSearch(srv *sdkmcp.Server, flights FlightProvider) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{Name: "flight_search", Description: "Search readable flight reference, title, and description by literal query. Returns at most 50 deterministic metadata results, never instructions. Refine the query if truncated=true; there is no cursor. Results confer no access.", Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)}}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in kegSearchInput) (*sdkmcp.CallToolResult, any, error) {
		if strings.TrimSpace(in.Query) == "" {
			return errorResult(fmt.Errorf("%w: query must not be empty", keg.ErrInvalid)), nil, nil
		}
		provider, ok := flights.(FlightCatalogProvider)
		if !ok {
			return errorResult(fmt.Errorf("%w: flight catalog unavailable", keg.ErrNotSupported)), nil, nil
		}
		rows, err := provider.FlightCatalog(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		out := SearchIdentityFlights(rows, in.Query)
		lines := []string{}
		for _, row := range out.Flights {
			lines = append(lines, tsvField(row.Title)+"\t"+row.Ref+"\t"+tsvField(row.Description))
		}
		if out.Truncated {
			lines = append(lines, "Results truncated to 50 flights; refine the query.")
		}
		res := linesResult(lines)
		res.StructuredContent = out
		return res, nil, nil
	})
}
func (p localFlightProvider) FlightCatalog(ctx context.Context) ([]*tapper.Flight, error) {
	return p.tap.FlightService.FlightCatalog(ctx, "", nil)
}
