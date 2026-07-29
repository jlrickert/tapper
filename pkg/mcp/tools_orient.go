package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// orientInput is intentionally empty: orientation adopts flight-scoped
// authority and never selects a KEG.
type orientInput struct{}

// registerOrientTools wires the orient surface onto srv. Called from
// NewServer alongside the other register*Tools helpers.
func registerOrientTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerOrient(srv, tap, defaults)
}

func registerOrient(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "orient",
		Description: "Refresh this session's flight authority and return the shared Tapper orientation payload.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in orientInput) (*sdkmcp.CallToolResult, any, error) {
		if defaults.gate != nil {
			current, err := defaults.gate.refresh(ctx, sessionIDFromContext(ctx))
			if err != nil {
				return errorResult(err), nil, nil
			}
			return textResult(current.payload), nil, nil
		}
		opts := tapper.OrientOptions{KegTargetOptions: resolveKegTarget(ctx, "", defaults)}
		payload, err := tap.Orient(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(payload), nil, nil
	})
}
