package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

type hubListInput struct {
	Hub string `json:"hub,omitempty" jsonschema:"hub to list (default: the local hub)"`
}

// registerHubTools exposes hub keg enumeration over MCP at parity with the
// `tap hub list` CLI command.
func registerHubTools(srv *sdkmcp.Server, tap *tapper.Tap, _ KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "hub_list",
		Description: "List the kegs available on a hub, qualified as @namespace/keg",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in hubListInput) (*sdkmcp.CallToolResult, any, error) {
		kegs, err := tap.HubListKegs(ctx, tapper.HubListOptions{Hub: in.Hub})
		if err != nil {
			return errorResult(err), nil, nil
		}
		return linesResult(kegs), nil, nil
	})
}
