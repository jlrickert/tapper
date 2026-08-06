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
		// tool descriptions are the one channel that is re-delivered with every
		// tools/list and therefore survives a context reset. The flight's own
		// instructions arrive in this tool's *result*, which lives in the
		// conversation and is exactly what a clear or compact discards — so the
		// instruction to come back must live somewhere the reset cannot reach.
		Description: "Call this first in every session, before any other tool and before " +
			"replying to the user — not only when KEG work is expected. The active flight " +
			"carries this session's operating instructions, so until you orient you do not " +
			"know what the session is for or which KEGs you may touch. Returns that flight, " +
			"its instructions, and the available KEGs. Call it again after any context reset " +
			"such as a clear or a compact: the connection survives those but the flight " +
			"instructions do not, and the server cannot detect the reset to re-send them. " +
			"If you cannot tell whether you have oriented in the current context, you have " +
			"not — orient. It is idempotent and also picks up configuration changed since " +
			"you connected. While no flight is active the KEG tools are hidden and only " +
			"orient, list_flights, flight_show, and auth_info are available; call orient " +
			"again once the user selects a flight to unlock them.",
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
