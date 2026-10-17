package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

type namespaceListInput struct{}

// InvitationLister is implemented by namespace providers that can list the
// caller's pending invitations. It is optional so a provider without it still
// satisfies NamespaceProvider; invitation_list registers only when present.
type InvitationLister interface {
	// ListInvitations returns the caller's pending, unexpired invitations.
	ListInvitations(context.Context) ([]tapper.HubInvitation, error)
}

// registerNamespaceTools exposes namespace discovery over MCP, at parity with
// `tap namespace list`. The hub-wide people directory is explore_people. Listing never grants
// authority. Member rosters and role changes stay UI-only, so those tools are
// intentionally not registered. Pending invitations are listed read-only:
// accepting or declining access is the account holder's own decision, made in
// the Hub UI or `tap invitation`, never by an agent.
func registerNamespaceTools(srv *sdkmcp.Server, namespaces NamespaceProvider) {
	if lister, ok := namespaces.(InvitationLister); ok {
		sdkmcp.AddTool(srv, &sdkmcp.Tool{
			Name:        "invitation_list",
			Description: "Session: List pending invitations addressed to you: org memberships and keg grants that take effect only if you accept them. Read-only; accepting or declining is your user's decision, made in the Hub UI or with `tap invitation`, so ask them rather than acting.",
			Annotations: &sdkmcp.ToolAnnotations{
				ReadOnlyHint:  true,
				OpenWorldHint: boolPtr(false),
			},
		}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ namespaceListInput) (*sdkmcp.CallToolResult, any, error) {
			rows, err := lister.ListInvitations(ctx)
			if err != nil {
				return errorResult(err), nil, nil
			}
			lines := make([]string, 0, len(rows))
			for _, inv := range rows {
				lines = append(lines, fmt.Sprintf("%d\t%s\t%s\t%s\t@%s", inv.ID, inv.Kind, inv.Target, inv.Role, inv.InvitedBy))
			}
			res := linesResult(lines)
			res.StructuredContent = map[string]any{"invitations": rows}
			return res, nil, nil
		})
	}
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "namespace_list",
		Description: "Session: List the namespaces you belong to, with your role in each.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ namespaceListInput) (*sdkmcp.CallToolResult, any, error) {
		rows, err := namespaces.ListNamespaces(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		lines := make([]string, 0, len(rows))
		for _, ns := range rows {
			lines = append(lines, fmt.Sprintf("@%s\t%s\t%s", ns.Name, ns.Kind, ns.Role))
		}
		res := linesResult(lines)
		res.StructuredContent = map[string]any{"namespaces": rows}
		return res, nil, nil
	})
}
