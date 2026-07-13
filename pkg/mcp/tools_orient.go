package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// orientInput is the parameter surface of mcp__tapper__orient. Every
// field is optional: a bare call returns the shared KEG system payload
// with the active keg resolved from the working directory.
type orientInput struct {
	Keg string `json:"keg,omitempty"    jsonschema:"keg alias; pins active-keg resolution"`
}

// registerOrientTools wires the orient surface onto srv. Called from
// NewServer alongside the other register*Tools helpers.
func registerOrientTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerOrient(srv, tap, defaults)
}

func registerOrient(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "orient",
		Description: "Return the shared Tapper KEG system orientation payload, including active KEG context, available KEGs, flight instructions, KEG-level instructions, and canonical guidance.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in orientInput) (*sdkmcp.CallToolResult, any, error) {
		kegOpts := resolveKegTarget(ctx, in.Keg, defaults)
		opts := tapper.OrientOptions{
			KegTargetOptions: kegOpts,
		}
		payload, err := tap.Orient(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(payload), nil, nil
	})
}
