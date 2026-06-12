package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerGraphTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerGraph(srv, tap, defaults)
}

// --- graph ---

type graphInput struct {
	Keg    string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Flight string `json:"flight,omitempty" jsonschema:"flight ref to cap available kegs (uses server default if empty)"`
}

func registerGraph(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "graph",
		Description: "Generate a self-contained HTML page visualizing the KEG node graph",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in graphInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.GraphOptions{
			KegTargetOptions: resolveKegTargetWithFlight(in.Keg, in.Flight, defaults),
		}

		html, err := tap.Graph(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(html), nil, nil
	})
}
