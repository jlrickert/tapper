package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

type orientInput struct{}
type sessionRefreshInput struct{}

// registerOrientTools wires the orient surface onto srv. Called from
// NewServer alongside the other register*Tools helpers.
func registerOrientTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerOrient(srv, tap, defaults)
	registerSessionRefresh(srv, defaults)
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
			"not — orient. This tool is read-only and never activates or changes the connection's " +
			"session state. Every authority-bearing call accepts an optional top-level flight. " +
			"With no flight, omission uses all identity-authorized KEGs while an explicit value selects any listed real flight exactly. " +
			"For a real root, default orient and keg_list discovery summarize its accessible transitive graph and explicit selection is limited to that graph. Calls reload live authority independently, so concurrent agents may use different flights without " +
			"changing shared session state. " +
			"If an explicitly configured root fails to load, the session fails closed with only recovery tools. " +
			"When no root is configured, no-flight full access stays pinned until the connection ends; pin a restrictive flight outside MCP and start a new connection.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in orientInput) (*sdkmcp.CallToolResult, any, error) {
		if defaults.gate != nil {
			return textResult(defaults.gate.payload(ctx)), nil, nil
		}
		opts := tapper.OrientOptions{KegTargetOptions: resolveKegTarget(ctx, "", defaults)}
		payload, err := tap.Orient(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(payload), nil, nil
	})
}

func registerSessionRefresh(srv *sdkmcp.Server, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "session_refresh",
		Description: "Retry MCP session activation after a failed configured flight becomes available. " +
			"Takes no arguments and never changes an already-active connection. A no-flight full-access " +
			"connection therefore requires a new MCP connection after a restrictive flight is pinned.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ sessionRefreshInput) (*sdkmcp.CallToolResult, any, error) {
		out, err := defaults.gate.refresh(ctx, sessionIDFromContext(ctx))
		if err != nil {
			return sessionRefreshFailureResult(defaults.gate.current(sessionIDFromContext(ctx)), err), nil, nil
		}
		var message string
		switch out.Status {
		case "activated":
			message = "session activated on " + out.Root + "; call `orient`"
		case "already_active":
			if out.NextAction == "new_session" {
				message = "session already active with no flight; " + defaults.gate.fullAccessReconnect(ctx)
			} else {
				message = "session already active on " + out.Root + "; call `orient`"
			}
		default:
			message = "the configured flight is still unavailable; repair it, then call `session_refresh` and `orient`"
		}
		result := textResult(message)
		result.StructuredContent = out
		return result, nil, nil
	})
}
