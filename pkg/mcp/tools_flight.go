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
	Visibility   string   `json:"visibility,omitempty" jsonschema:"flight visibility: private (default) or public"`
	Capabilities []string `json:"capabilities,omitempty" jsonschema:"explicit capabilities; supported: full_access, manage_flights"`
	Instructions string   `json:"instructions,omitempty" jsonschema:"markdown instructions"`
	Cover        []string `json:"cover,omitempty" jsonschema:"covered kegs with role caps, e.g. @ns/keg=viewer, @ns/keg=editor, or @ns/keg=admin (bare entries default to viewer)"`
}

type flightEditInput struct {
	Ref          string   `json:"ref" jsonschema:"flight reference (@namespace/+slug; a bare slug uses the default namespace)"`
	Title        *string  `json:"title,omitempty" jsonschema:"new flight title; omit to keep the current title"`
	Visibility   *string  `json:"visibility,omitempty" jsonschema:"new visibility: private or public; omit to keep current"`
	Capabilities []string `json:"capabilities,omitempty" jsonschema:"replacement capabilities; omit to keep current"`
	Instructions *string  `json:"instructions,omitempty" jsonschema:"new markdown instructions; omit to keep the current instructions"`
	Cover        []string `json:"cover,omitempty" jsonschema:"replacement cover entries, e.g. @ns/keg=viewer; omit to keep the current cover"`
}

type flightDeleteInput struct {
	Ref string `json:"ref" jsonschema:"flight reference (@namespace/+slug; a bare slug uses the default namespace)"`
}

// registerFlightTools exposes flight discovery and management over MCP at
// parity with the `tap flight list/show/create/edit/delete` CLI commands.
// flight_edit is the agent-facing equivalent of the CLI's piped
// `flight edit`: agents cannot open editors, so the partial-edit tool
// (omitted fields keep their current values) remains the MCP surface.
func registerFlightTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
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
			Visibility:   in.Visibility,
			Capabilities: flightCapabilitiesFromStrings(in.Capabilities),
			Instructions: in.Instructions,
			Cover:        cover,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(renderFlight(flight)), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "flight_edit",
		Description: "Edit a Hub-backed flight; omitted fields keep their current values",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in flightEditInput) (*sdkmcp.CallToolResult, any, error) {
		if err := defaults.gate.authorizeMutation(sessionIDFromContext(ctx), in.Ref, true); err != nil {
			return errorResult(err), nil, nil
		}
		opts := tapper.UpdateFlightOptions{
			Ref:          in.Ref,
			Title:        in.Title,
			Visibility:   in.Visibility,
			Instructions: in.Instructions,
		}
		if in.Capabilities != nil {
			capabilities := flightCapabilitiesFromStrings(in.Capabilities)
			opts.Capabilities = &capabilities
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
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in flightDeleteInput) (*sdkmcp.CallToolResult, any, error) {
		if err := defaults.gate.authorizeMutation(sessionIDFromContext(ctx), in.Ref, true); err != nil {
			return errorResult(err), nil, nil
		}
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
	fmt.Fprintf(&b, "visibility: %s\n", f.Visibility)
	if len(f.Capabilities) > 0 {
		fmt.Fprintf(&b, "capabilities: %s\n", strings.Join(flightCapabilityNames(f.Capabilities), ", "))
	}
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
		b.WriteString("cover: (none; denies all KEG access)\n")
	}
	if f.Instructions != "" {
		fmt.Fprintf(&b, "\n%s\n", f.Instructions)
	}
	return b.String()
}

func flightCapabilitiesFromStrings(values []string) []tapper.FlightCapability {
	out := make([]tapper.FlightCapability, 0, len(values))
	for _, value := range values {
		out = append(out, tapper.FlightCapability(value))
	}
	return out
}

func flightCapabilityNames(values []tapper.FlightCapability) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}
