package mcp

import (
	"context"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

type kegListInput struct{}

type kegCreateInput struct {
	Keg        string `json:"keg" jsonschema:"alias for the new keg (1-64 lowercase letters, digits, or hyphens)"`
	Namespace  string `json:"namespace,omitempty" jsonschema:"target namespace without the @ sigil; empty uses the session default"`
	Title      string `json:"title,omitempty" jsonschema:"human-readable keg title"`
	Visibility string `json:"visibility,omitempty" jsonschema:"keg visibility: private (default) or public"`
}

// registerKegTools exposes identity-authorized discovery filtered through the
// immutable active flight, plus keg creation for flights that carry
// manage_kegs. Transport-specific hub selection is intentionally absent from
// the agent surface.
func registerKegTools(srv *sdkmcp.Server, defaults KegDefaults, kegs KegDiscoveryProvider) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "keg_list",
		Description: "List identity-authorized kegs covered by the active flight, qualified as @namespace/keg",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ kegListInput) (*sdkmcp.CallToolResult, any, error) {
		refs, err := kegs.ListKegs(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return linesResult(filterKegRefs(ctx, refs)), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "keg_create",
		Description: "Create a new KEG. Requires the active flight to grant manage_kegs. " +
			"The new KEG is not readable until a flight covers it — creating one does not " +
			"add it to the active flight's cover.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:    false,
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in kegCreateInput) (*sdkmcp.CallToolResult, any, error) {
		// The gate refuses this tool before dispatch; the check is repeated here
		// so an embedded surface without the session gate cannot reach creation
		// through a flight that never granted it.
		if err := defaults.gate.authorizeKegCreation(sessionIDFromContext(ctx)); err != nil {
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
		return textResult("created keg " + ref +
			"\n\nIt is not in this flight's cover yet, so KEG tools cannot reach it. " +
			"Add it to a flight's cover, then call `orient`."), nil, nil
	})
}

func filterKegRefs(ctx context.Context, refs []string) []string {
	flight := SessionFlight(ctx)
	if HasSessionOrientation(ctx) && flight == nil {
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
