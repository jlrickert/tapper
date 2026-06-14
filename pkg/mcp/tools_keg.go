package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

type kegListInput struct {
	Hub string `json:"hub,omitempty" jsonschema:"hub to list (default: every configured hub)"`
}

type kegGrantsInput struct {
	Keg string `json:"keg" jsonschema:"keg reference (@namespace/keg)"`
}

type kegGrantInput struct {
	Keg  string `json:"keg" jsonschema:"keg reference (@namespace/keg)"`
	User string `json:"user" jsonschema:"username to grant (with or without a leading @)"`
	Role string `json:"role" jsonschema:"role to grant: viewer, editor, or admin"`
}

type kegRevokeInput struct {
	Keg  string `json:"keg" jsonschema:"keg reference (@namespace/keg)"`
	User string `json:"user" jsonschema:"username to revoke (with or without a leading @)"`
}

type kegVisibilityInput struct {
	Keg        string `json:"keg" jsonschema:"keg reference (@namespace/keg)"`
	Visibility string `json:"visibility" jsonschema:"visibility: public or private"`
}

// registerKegTools exposes hub-side keg administration over MCP at parity with
// the `tap keg list/grants/grant/revoke/visibility` CLI commands.
func registerKegTools(srv *sdkmcp.Server, tap *tapper.Tap, _ KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "keg_list",
		Description: "List the kegs available on a hub, qualified as @namespace/keg",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in kegListInput) (*sdkmcp.CallToolResult, any, error) {
		kegs, err := tap.HubListKegs(ctx, tapper.HubListOptions{Hub: in.Hub})
		if err != nil {
			return errorResult(err), nil, nil
		}
		return linesResult(kegs), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "keg_grants",
		Description: "List the access grants (username, role) on a keg",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in kegGrantsInput) (*sdkmcp.CallToolResult, any, error) {
		grants, err := tap.KegGrants(ctx, tapper.KegGrantsOptions{Keg: in.Keg})
		if err != nil {
			return errorResult(err), nil, nil
		}
		lines := make([]string, 0, len(grants))
		for _, g := range grants {
			lines = append(lines, fmt.Sprintf("@%s\t%s", g.Username, g.Role))
		}
		return linesResult(lines), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "keg_grant",
		Description: "Grant a user a role on a keg (viewer, editor, or admin)",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in kegGrantInput) (*sdkmcp.CallToolResult, any, error) {
		if err := tap.KegGrant(ctx, tapper.KegGrantOptions{Keg: in.Keg, User: in.User, Role: in.Role}); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("granted @%s %s on %s", in.User, in.Role, in.Keg)), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "keg_revoke",
		Description: "Revoke a user's grant on a keg",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in kegRevokeInput) (*sdkmcp.CallToolResult, any, error) {
		if err := tap.KegRevoke(ctx, tapper.KegRevokeOptions{Keg: in.Keg, User: in.User}); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("revoked @%s on %s", in.User, in.Keg)), nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "keg_visibility",
		Description: "Set a keg's visibility to public or private",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in kegVisibilityInput) (*sdkmcp.CallToolResult, any, error) {
		if err := tap.KegVisibility(ctx, tapper.KegVisibilityOptions{Keg: in.Keg, Visibility: in.Visibility}); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("set %s visibility to %s", in.Keg, in.Visibility)), nil, nil
	})
}
