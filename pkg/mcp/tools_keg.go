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

type kegVisibilityInput struct {
	Keg        string `json:"keg" jsonschema:"keg reference (@namespace/keg)"`
	Visibility string `json:"visibility" jsonschema:"visibility: public or private"`
}

// registerKegTools exposes hub-side keg discovery and visibility over MCP.
// User grant and role management stays UI-only for now, so grant tools are
// intentionally not registered.
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
