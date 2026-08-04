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
		Name: "orient",
		// The imperative lives here, not only in the server instructions, because
		// tool descriptions are the one thing every model reads. A weaker model
		// given only server instructions will not infer that it must orient.
		Description: "Call this first, before any other KEG tool. Establishes this session's " +
			"flight authority and returns the Tapper orientation payload listing the KEGs you " +
			"may use. While no flight is active the KEG tools are hidden and only orient, " +
			"list_flights, flight_show, and auth_info are available; call orient again after " +
			"the user selects a flight to unlock them.",
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
