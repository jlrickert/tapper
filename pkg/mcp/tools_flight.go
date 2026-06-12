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

type flightCreateInput struct {
	Ref          string   `json:"ref" jsonschema:"flight reference (@namespace/+slug; a bare slug uses the default namespace)"`
	Title        string   `json:"title,omitempty" jsonschema:"flight title"`
	Instructions string   `json:"instructions,omitempty" jsonschema:"markdown instructions"`
	Cover        []string `json:"cover,omitempty" jsonschema:"covered kegs with role caps, e.g. @ns/keg=viewer or @ns/keg=editor (bare entries default to viewer)"`
}

type flightUpdateInput struct {
	Ref          string   `json:"ref" jsonschema:"flight reference (@namespace/+slug; a bare slug uses the default namespace)"`
	Title        *string  `json:"title,omitempty" jsonschema:"new flight title; omit to keep the current title"`
	Instructions *string  `json:"instructions,omitempty" jsonschema:"new markdown instructions; omit to keep the current instructions"`
	Cover        []string `json:"cover,omitempty" jsonschema:"replacement cover entries, e.g. @ns/keg=viewer; omit to keep the current cover"`
}

type flightDeleteInput struct {
	Ref string `json:"ref" jsonschema:"flight reference (@namespace/+slug; a bare slug uses the default namespace)"`
}

// registerFlightTools exposes flight discovery and management over MCP at
// parity with the `tap flight list/show/create/update/delete` CLI commands.
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
		Description: "Show a flight's cover roles and instructions",
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

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "flight_create",
		Description: "Create a Hub-backed flight (cover roles + agent instructions)",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in flightCreateInput) (*sdkmcp.CallToolResult, any, error) {
		cover, err := tapper.ParseFlightCoverSpecs(in.Cover)
		if err != nil {
			return errorResult(err), nil, nil
		}
		flight, err := tap.CreateFlight(ctx, tapper.CreateFlightOptions{
			Ref:          in.Ref,
			Title:        in.Title,
			Instructions: in.Instructions,
			Cover:        cover,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(renderFlight(flight)), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "flight_update",
		Description: "Update a Hub-backed flight; omitted fields keep their current values",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in flightUpdateInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.UpdateFlightOptions{
			Ref:          in.Ref,
			Title:        in.Title,
			Instructions: in.Instructions,
		}
		if in.Cover != nil {
			cover, err := tapper.ParseFlightCoverSpecs(in.Cover)
			if err != nil {
				return errorResult(err), nil, nil
			}
			opts.Cover = &cover
		}
		flight, err := tap.UpdateFlight(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(renderFlight(flight)), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "flight_delete",
		Description: "Delete a Hub-backed flight",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in flightDeleteInput) (*sdkmcp.CallToolResult, any, error) {
		if err := tap.DeleteFlight(ctx, tapper.DeleteFlightOptions{Ref: in.Ref}); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult("deleted " + in.Ref), nil, nil
	})
}

func renderFlight(f *tapper.Flight) string {
	var b strings.Builder
	fmt.Fprintf(&b, "flight: %s\n", f.Name)
	if f.Title != "" {
		fmt.Fprintf(&b, "title: %s\n", f.Title)
	}
	fmt.Fprintf(&b, "source: %s\n", f.Source)
	if len(f.Cover) > 0 {
		b.WriteString("cover:\n")
		for _, c := range f.Cover {
			if c.Namespace != "" {
				fmt.Fprintf(&b, "  @%s/%s=%s\n", c.Namespace, c.Keg, c.Role)
			} else {
				fmt.Fprintf(&b, "  %s=%s\n", c.Keg, c.Role)
			}
		}
	} else {
		b.WriteString("cover: (none; restricts nothing)\n")
	}
	if f.Instructions != "" {
		fmt.Fprintf(&b, "\n%s\n", f.Instructions)
	}
	return b.String()
}
