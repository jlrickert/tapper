package mcp

import (
	"context"
	"sort"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

type kegListInput struct{}

// registerKegTools exposes identity-authorized discovery filtered through the
// immutable active flight. Transport-specific hub selection is intentionally
// absent from the agent surface.
func registerKegTools(srv *sdkmcp.Server, _ KegDefaults, kegs KegDiscoveryProvider) {
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
