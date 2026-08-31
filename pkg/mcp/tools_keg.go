package mcp

import (
	"context"
	"errors"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

type kegListInput struct{}

type kegListRow struct {
	Ref     string   `json:"ref"`
	Role    string   `json:"role"`
	Flights []string `json:"flights"`
}

type kegListOutput struct {
	Kegs []kegListRow `json:"kegs"`
}

type kegSearchInput struct {
	Query string `json:"query" jsonschema:"required,non-empty case-insensitive literal query matched against canonical ref, title, and summary"`
}

type kegCreateInput struct {
	Keg        string `json:"keg" jsonschema:"alias for the new keg (1-64 lowercase letters, digits, or hyphens)"`
	Namespace  string `json:"namespace,omitempty" jsonschema:"target namespace without the @ sigil; empty uses the session default"`
	Title      string `json:"title,omitempty" jsonschema:"human-readable keg title"`
	Visibility string `json:"visibility,omitempty" jsonschema:"keg visibility: private (default) or public"`
}

// registerKegTools exposes identity-authorized discovery, optionally narrowed
// through a call-selected flight snapshot. No-flight sessions may create KEGs;
// real-flight sessions require manage_kegs. Transport-specific hub selection
// is intentionally absent from the agent surface.
func registerKegTools(srv *sdkmcp.Server, defaults KegDefaults, kegs KegDiscoveryProvider, search KegSearchProvider) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "keg_list",
		Description: "Discover canonical KEGs under current authority. With no flight, every identity-accessible KEG is returned at its real role. Supplying flight selects exactly one available real flight for the call",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in kegListInput) (*sdkmcp.CallToolResult, any, error) {
		if HasSessionOrientation(ctx) {
			rows := SessionOrientationKegs(ctx)
			out := kegListOutput{Kegs: make([]kegListRow, 0, len(rows))}
			lines := make([]string, 0, len(rows))
			for _, row := range rows {
				effective := EffectiveOrientationRole(row)
				flights := append([]string{}, row.Flights...)
				out.Kegs = append(out.Kegs, kegListRow{Ref: row.Ref, Role: effective, Flights: flights})
				lines = append(lines, row.Ref+"\t"+effective+"\t"+strings.Join(flights, ","))
			}
			res := linesResult(lines)
			res.StructuredContent = out
			return res, nil, nil
		}
		// Embedded ungated surfaces retain their identity-only compatibility
		// behavior. The agent-safe MCP server always takes the governed path.
		refs, err := kegs.ListKegs(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		filtered := filterKegRefs(ctx, refs)
		out := kegListOutput{Kegs: make([]kegListRow, 0, len(filtered))}
		lines := make([]string, 0, len(filtered))
		for _, ref := range filtered {
			out.Kegs = append(out.Kegs, kegListRow{Ref: ref, Role: string(tapper.FlightRoleViewer), Flights: []string{}})
			lines = append(lines, ref+"\t"+string(tapper.FlightRoleViewer)+"\t")
		}
		res := linesResult(lines)
		res.StructuredContent = out
		return res, nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "keg_search",
		Description: "Search identity-accessible KEG metadata across all configured hubs. Search results never grant access: no-flight calls may operate at the returned identity role, while a real-flight call must also cover the KEG",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in kegSearchInput) (*sdkmcp.CallToolResult, any, error) {
		query := strings.TrimSpace(in.Query)
		if query == "" {
			return errorResult(errors.New("query must not be empty")), nil, nil
		}
		found, err := search.SearchKegs(ctx, query)
		if err != nil {
			return errorResult(err), nil, nil
		}
		lines := make([]string, 0, len(found.Warnings)+len(found.Kegs))
		for _, warning := range found.Warnings {
			lines = append(lines, "Warning: "+tsvField(warning))
		}
		for _, row := range found.Kegs {
			lines = append(lines, strings.Join([]string{
				row.Ref, row.Role, tsvField(row.Title), tsvField(row.Summary), row.Visibility, row.Source,
			}, "\t"))
		}
		res := linesResult(lines)
		res.StructuredContent = found
		return res, nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "keg_create",
		Description: "Create a new KEG. No-flight sessions use normal namespace membership; " +
			"a selected real flight must grant manage_kegs. Creating a KEG never adds it " +
			"to a real flight's cover.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in kegCreateInput) (*sdkmcp.CallToolResult, any, error) {
		// The gate refuses this tool before dispatch; the check is repeated here
		// so an embedded surface without the session gate cannot reach creation
		// through a flight that never granted it.
		if err := defaults.gate.authorizeKegCreation(ctx); err != nil {
			return errorResult(err), nil, nil
		}
		ref, err := kegs.CreateKeg(ctx, tapper.CreateKegOptions{
			Keg:        in.Keg,
			Namespace:  in.Namespace,
			Title:      in.Title,
			Visibility: in.Visibility,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		if nudge := defaults.gate.fullAccessReconnect(ctx); nudge != "" {
			return textResult("created keg " + ref + "\n\n" + nudge), nil, nil
		}
		text := "created keg " + ref +
			"\n\nIt is not in this flight's cover yet, so KEG tools cannot reach it. " +
			"Add it to a flight's cover, then call `orient`."
		return textResult(text), nil, nil
	})
}

func tsvField(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\t", " ")
	return strings.TrimSpace(value)
}

func filterKegRefs(ctx context.Context, refs []string) []string {
	flight := SessionFlight(ctx)
	// Two governed states have no flight snapshot and must not be conflated.
	// Failed-root recovery reaches nothing, so it filters to empty. No-flight
	// identity authority reaches everything the identity reaches, so it filters
	// nothing — otherwise auth_info would report zero KEGs in a session that can
	// read them all, contradicting keg_list.
	if HasSessionOrientation(ctx) && flight == nil && !SessionFullAccess(ctx) {
		return []string{}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(refs))
	for _, raw := range refs {
		ref := strings.TrimSpace(raw)
		if ref == "" {
			continue
		}
		if flight != nil && !flight.HasCapability(tapper.FlightCapabilityFullAccess) {
			nsAlias := strings.TrimPrefix(ref, "@")
			ns, alias, ok := strings.Cut(nsAlias, "/")
			if !ok {
				continue
			}
			if _, covered := flight.RoleFor("", ns, alias); !covered {
				continue
			}
		}
		if _, duplicate := seen[ref]; duplicate {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}
