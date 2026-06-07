package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

type listFlightsInput struct{}

type flightShowInput struct {
	Name string `json:"name" jsonschema:"flight name to inspect"`
}

// registerFlightTools exposes flight discovery over MCP at parity with the
// `tap flight list` / `tap flight show` CLI commands.
func registerFlightTools(srv *sdkmcp.Server, tap *tapper.Tap, _ KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "list_flights",
		Description: "List available flights (keg restrictions + agent instructions)",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ listFlightsInput) (*sdkmcp.CallToolResult, any, error) {
		names, err := tap.ListFlights(ctx, tapper.ListFlightsOptions{})
		if err != nil {
			return errorResult(err), nil, nil
		}
		return linesResult(names), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "flight_show",
		Description: "Show a flight's allowed kegs and instructions",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in flightShowInput) (*sdkmcp.CallToolResult, any, error) {
		flight, err := tap.GetFlight(ctx, tapper.GetFlightOptions{Name: in.Name})
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(renderFlight(flight)), nil, nil
	})
}

func renderFlight(f *tapper.Flight) string {
	var b strings.Builder
	fmt.Fprintf(&b, "flight: %s\n", f.Name)
	if f.Title != "" {
		fmt.Fprintf(&b, "title: %s\n", f.Title)
	}
	fmt.Fprintf(&b, "source: %s\n", f.Source)
	if len(f.AllowedKegs) > 0 {
		fmt.Fprintf(&b, "allowed kegs: %s\n", strings.Join(f.AllowedKegs, ", "))
	} else {
		b.WriteString("allowed kegs: (none — restricts nothing)\n")
	}
	if f.Instructions != "" {
		fmt.Fprintf(&b, "\n%s\n", f.Instructions)
	}
	return b.String()
}
