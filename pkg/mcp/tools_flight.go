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
	Capabilities []string `json:"capabilities,omitempty" jsonschema:"explicit capabilities; supported: full_access, manage_flights, manage_kegs"`
	Instructions string   `json:"instructions,omitempty" jsonschema:"markdown instructions"`
	Cover        []string `json:"cover,omitempty" jsonschema:"covered kegs with role caps, e.g. @ns/keg=viewer, @ns/keg=editor, or @ns/keg=admin (bare entries default to viewer)"`
	Subflights   []string `json:"subflights,omitempty" jsonschema:"ordered child flight references; bare +slug references use this flight's namespace"`
}

type flightEditInput struct {
	Ref          string   `json:"ref" jsonschema:"flight reference (@namespace/+slug; a bare slug uses the default namespace)"`
	Title        *string  `json:"title,omitempty" jsonschema:"new flight title; omit to keep the current title"`
	Visibility   *string  `json:"visibility,omitempty" jsonschema:"new visibility: private or public; omit to keep current"`
	Capabilities []string `json:"capabilities,omitempty" jsonschema:"replacement capabilities; omit to keep current"`
	Instructions *string  `json:"instructions,omitempty" jsonschema:"new markdown instructions; omit to keep the current instructions"`
	Cover        []string `json:"cover,omitempty" jsonschema:"replacement cover entries, e.g. @ns/keg=viewer; omit to keep the current cover"`
	Subflights   []string `json:"subflights,omitempty" jsonschema:"replacement ordered child flight references; omit to keep current"`
	ExpectedHash string   `json:"expected_hash" jsonschema:"precondition token returned by flight_show"`
}

type flightDeleteInput struct {
	Ref          string `json:"ref" jsonschema:"flight reference (@namespace/+slug; a bare slug uses the default namespace)"`
	ExpectedHash string `json:"expected_hash" jsonschema:"precondition token returned by flight_show"`
}

// registerFlightTools exposes flight discovery and management over MCP at
// parity with the `tap flight list/show/create/edit/delete` CLI commands.
// flight_edit is the agent-facing equivalent of the CLI's piped
// `flight edit`: agents cannot open editors, so the partial-edit tool
// (omitted fields keep their current values) remains the MCP surface.
func registerFlightTools(srv *sdkmcp.Server, defaults KegDefaults, flights FlightProvider) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "list_flights",
		Description: "List available flights (keg restrictions + agent instructions)",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ listFlightsInput) (*sdkmcp.CallToolResult, any, error) {
		names, err := flights.ListFlights(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return linesResult(names), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "flight_show",
		Description: "Show a flight's cover roles and instructions. The result carries the " +
			"flight's manifest hash; pass it back as expected_hash when editing this flight.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in flightShowInput) (*sdkmcp.CallToolResult, any, error) {
		flight, err := flights.GetFlight(ctx, in.Name)
		if err != nil {
			return errorResult(err), nil, nil
		}
		res := textResult(renderFlight(flight))
		res.StructuredContent = map[string]any{
			"name": flight.Name,
			"hash": flight.ManifestHash,
		}
		return res, nil, nil
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
		flight, err := flights.CreateFlight(ctx, tapper.CreateFlightOptions{
			Ref:          in.Ref,
			Title:        in.Title,
			Visibility:   in.Visibility,
			Capabilities: flightCapabilitiesFromStrings(in.Capabilities),
			Instructions: in.Instructions,
			Cover:        cover,
			Subflights:   append([]string(nil), in.Subflights...),
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		text := renderFlight(flight)
		if nudge := defaults.gate.fullAccessReconnect(ctx); nudge != "" {
			text += "\n" + nudge + "\n"
		}
		return textResult(text), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "flight_edit",
		Description: "Call flight_show first, then edit a Hub-backed flight using its manifest hash as expected_hash; omitted fields keep their current values. On conflict, merge into the returned current flight (or refetch with flight_show) and retry with the returned current hash.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in flightEditInput) (*sdkmcp.CallToolResult, any, error) {
		if err := defaults.gate.authorizeMutation(ctx); err != nil {
			return errorResult(err), nil, nil
		}
		rootTarget, activeTarget, err := defaults.gate.orientationTarget(ctx, in.Ref)
		if err != nil {
			return errorResult(err), nil, nil
		}
		opts := tapper.UpdateFlightOptions{
			Ref:          in.Ref,
			Title:        in.Title,
			Visibility:   in.Visibility,
			Instructions: in.Instructions,
			ExpectedHash: in.ExpectedHash,
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
		if in.Subflights != nil {
			subflights := append([]string(nil), in.Subflights...)
			opts.Subflights = &subflights
		}
		flight, err := flights.UpdateFlight(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		text := renderFlight(flight)
		if rootTarget || activeTarget {
			text += "\nThe next authority-bearing call will resolve this live flight graph and authority automatically.\n"
		}
		return textResult(text), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "flight_delete",
		Description: "Call flight_show first, then delete a Hub-backed flight using its manifest hash as expected_hash. On conflict, refetch with flight_show and retry with the returned current hash.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in flightDeleteInput) (*sdkmcp.CallToolResult, any, error) {
		if err := defaults.gate.authorizeMutation(ctx); err != nil {
			return errorResult(err), nil, nil
		}
		rootTarget, activeTarget, err := defaults.gate.orientationTarget(ctx, in.Ref)
		if err != nil {
			return errorResult(err), nil, nil
		}
		if err := flights.DeleteFlight(ctx, tapper.DeleteFlightOptions{Ref: in.Ref, ExpectedHash: in.ExpectedHash}); err != nil {
			return errorResult(err), nil, nil
		}
		text := "deleted " + in.Ref
		if rootTarget {
			text += "\nORIENTATION_ROOT_UNAVAILABLE: the connection-pinned root was deleted. No replacement root will be adopted; start a new session after the user selects a root."
		} else if activeTarget {
			text += "\nThe deleted flight is no longer selectable from the pinned root; future calls that request it will be denied."
		}
		return textResult(text), nil, nil
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
	if len(f.Subflights) > 0 {
		b.WriteString("subflights:\n")
		for _, ref := range f.Subflights {
			fmt.Fprintf(&b, "  %s\n", ref)
		}
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
