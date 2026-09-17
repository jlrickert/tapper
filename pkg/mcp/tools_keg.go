package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/keg"
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
	Query string `json:"query" jsonschema:"required,non-empty case-insensitive literal query matched against canonical ref, title, and description"`
}

type kegCreateInput struct {
	Keg        string `json:"keg" jsonschema:"required,qualified creation reference @namespace/keg; each component is 1-64 lowercase letters, digits, or hyphens and starts with a letter or digit"`
	Title      string `json:"title,omitempty" jsonschema:"human-readable keg title"`
	Visibility string `json:"visibility,omitempty" jsonschema:"keg visibility: private (default) or public"`
}

// registerKegTools exposes identity-authorized discovery, optionally narrowed
// through a call-selected flight snapshot. No-flight sessions may create KEGs;
// real-flight sessions require manage_kegs. Transport-specific hub selection
// is intentionally absent from the agent surface.
func registerKegTools(srv *sdkmcp.Server, defaults KegDefaults, kegs KegDiscoveryProvider, search KegSearchProvider) {
	registerKegDelete(srv, defaults, kegs)
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "keg_list",
		Description: "Discover canonical KEGs and granting-flight provenance. Without a configured root, returns identity-accessible KEGs at their real roles. With a pinned root, omission aggregates its accessible transitive graph for discovery; a discovered child-only KEG still requires that child as flight on operational calls. Supplying flight returns exactly that flight projection",
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
		Description: "Search identity-accessible KEG metadata on the connection-pinned Hub. Search results never grant access: no-flight calls may operate at the returned identity role, while a real-flight call must also cover the KEG",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in kegSearchInput) (*sdkmcp.CallToolResult, any, error) {
		query := strings.TrimSpace(in.Query)
		if query == "" {
			return errorResult(fmt.Errorf("%w: query must not be empty", keg.ErrInvalid)), nil, nil
		}
		found, err := search.SearchKegs(ctx, query)
		if err != nil {
			return errorResult(err), nil, nil
		}
		lines := make([]string, 0, len(found.Warnings)+len(found.Kegs))
		if found.Truncated {
			lines = append(lines, "Results truncated to 50 KEGs; refine the query.")
		}
		for _, warning := range found.Warnings {
			lines = append(lines, "Warning: "+tsvField(warning))
		}
		for _, row := range found.Kegs {
			lines = append(lines, strings.Join([]string{
				row.Ref, row.Role, tsvField(row.Title), tsvField(row.Description), row.Visibility, row.Source,
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

type kegDeleteInput struct {
	Keg string `json:"keg" jsonschema:"explicit canonical @namespace/keg reference to permanently delete"`
}

func registerKegDelete(srv *sdkmcp.Server, defaults KegDefaults, provider KegDiscoveryProvider) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{Name: "keg_delete", Description: "Permanently delete an empty or populated KEG and all its data, including snapshots. Requires identity admin permission; a selected flight also requires delete_kegs and effective admin cover. manage_kegs alone cannot delete. No expected_hash: a settings hash does not cover a whole KEG.", Annotations: &sdkmcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(true)}}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in kegDeleteInput) (*sdkmcp.CallToolResult, any, error) {
		ns, alias, err := tapper.ParseCanonicalKegRef(in.Keg)
		if err != nil {
			return errorResult(fmt.Errorf("keg must be an explicit canonical @namespace/keg reference: %w", err)), nil, nil
		}
		if err := defaults.gate.authorizeCapability(orientationFromContext(ctx), tapper.FlightCapabilityDeleteKegs); err != nil {
			return errorResult(err), nil, nil
		}
		if flight := SessionFlight(ctx); flight != nil {
			role, covered := flight.RoleFor("", ns, alias)
			if !covered || role != tapper.FlightRoleAdmin {
				return errorResult(keg.ErrOrientationDenied), nil, nil
			}
		}
		deleter, ok := provider.(KegDeletionProvider)
		if !ok {
			return errorResult(keg.ErrNotSupported), nil, nil
		}
		if err := deleter.DeleteKeg(ctx, in.Keg); err != nil {
			return errorResult(err), nil, nil
		}
		out := map[string]any{"keg": in.Keg, "deleted": true}
		result := &sdkmcp.CallToolResult{StructuredContent: out}
		return result, nil, nil
	})
}
