package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

type namespaceListInput struct {
	Hub string `json:"hub,omitempty" jsonschema:"hub to query (default: the resolved default hub)"`
}

type namespaceMembersInput struct {
	Namespace string `json:"namespace" jsonschema:"namespace name (with or without a leading @)"`
}

type namespaceAddMemberInput struct {
	Namespace string `json:"namespace" jsonschema:"namespace name (with or without a leading @)"`
	User      string `json:"user" jsonschema:"username to add (with or without a leading @)"`
	Role      string `json:"role" jsonschema:"role: owner, admin, or member"`
}

type namespaceSetRoleInput struct {
	Namespace string `json:"namespace" jsonschema:"namespace name (with or without a leading @)"`
	User      string `json:"user" jsonschema:"username whose role to change (with or without a leading @)"`
	Role      string `json:"role" jsonschema:"role: owner, admin, or member"`
}

type namespaceRemoveMemberInput struct {
	Namespace string `json:"namespace" jsonschema:"namespace name (with or without a leading @)"`
	User      string `json:"user" jsonschema:"username to remove (with or without a leading @)"`
}

type namespaceCreateInput struct {
	Name string `json:"name" jsonschema:"org namespace name to create"`
	Hub  string `json:"hub,omitempty" jsonschema:"hub to open (default: resolved default/fallback hub)"`
}

// registerNamespaceTools exposes namespace administration over MCP at parity
// with the `tap namespace list/members/add-member/set-role/remove-member/create`
// CLI commands.
func registerNamespaceTools(srv *sdkmcp.Server, tap *tapper.Tap, _ KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "namespace_list",
		Description: "List the namespaces you belong to, with your role in each",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in namespaceListInput) (*sdkmcp.CallToolResult, any, error) {
		nss, err := tap.NamespaceList(ctx, tapper.NamespaceListOptions{Hub: in.Hub})
		if err != nil {
			return errorResult(err), nil, nil
		}
		lines := make([]string, 0, len(nss))
		for _, ns := range nss {
			lines = append(lines, fmt.Sprintf("@%s\t%s\t%s", ns.Name, ns.Kind, ns.Role))
		}
		return linesResult(lines), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "namespace_members",
		Description: "List the members of a namespace",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in namespaceMembersInput) (*sdkmcp.CallToolResult, any, error) {
		members, err := tap.NamespaceMembers(ctx, tapper.NamespaceMembersOptions{Namespace: in.Namespace})
		if err != nil {
			return errorResult(err), nil, nil
		}
		lines := make([]string, 0, len(members))
		for _, m := range members {
			lines = append(lines, fmt.Sprintf("@%s\t%s", m.Username, m.Role))
		}
		return linesResult(lines), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "namespace_add_member",
		Description: "Add a member to a namespace (owner, admin, or member)",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in namespaceAddMemberInput) (*sdkmcp.CallToolResult, any, error) {
		if err := tap.NamespaceAddMember(ctx, tapper.NamespaceAddMemberOptions{Namespace: in.Namespace, User: in.User, Role: in.Role}); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("added @%s to @%s as %s", in.User, in.Namespace, in.Role)), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "namespace_set_role",
		Description: "Change a namespace member's role (owner, admin, or member)",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in namespaceSetRoleInput) (*sdkmcp.CallToolResult, any, error) {
		if err := tap.NamespaceSetRole(ctx, tapper.NamespaceSetRoleOptions{Namespace: in.Namespace, User: in.User, Role: in.Role}); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("set @%s role in @%s to %s", in.User, in.Namespace, in.Role)), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "namespace_remove_member",
		Description: "Remove a member from a namespace",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in namespaceRemoveMemberInput) (*sdkmcp.CallToolResult, any, error) {
		if err := tap.NamespaceRemoveMember(ctx, tapper.NamespaceRemoveMemberOptions{Namespace: in.Namespace, User: in.User}); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("removed @%s from @%s", in.User, in.Namespace)), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "namespace_create",
		Description: "Return the hub UI URL for creating an org namespace",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in namespaceCreateInput) (*sdkmcp.CallToolResult, any, error) {
		ns, err := tap.NamespaceCreate(ctx, tapper.NamespaceCreateOptions{Name: in.Name, Hub: in.Hub})
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("Create @%s in the hub UI:\n%s", ns.Name, ns.URL)), nil, nil
	})
}
