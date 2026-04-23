package mcp

// MCP surface for the auth subsystem. Today the tool set is read-only:
// only `auth_status` is exposed. `login` stays CLI-only because it
// requires a loopback listener + browser round-trip that an agent
// cannot complete, and `logout` stays CLI-only because silent revocation
// by an agent is a surprise factor we are not willing to underwrite.
//
// If a future use case demands an MCP writer, prefer adding a
// narrowly-scoped "auth_revoke" with explicit consent annotations over
// reusing the CLI logout path.

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// authStatusInput mirrors AuthStatusOptions: flat (no keg target) and
// single-field because auth state is user-level, not keg-level. Agents
// that omit Hub get the single-hub auto-resolve behavior, the same as
// the CLI.
type authStatusInput struct {
	Hub string `json:"hub,omitempty" jsonschema:"hub URL to query; omit when exactly one hub is stored"`
}

// registerAuthTools omits the KegDefaults parameter that sibling
// register*Tools functions accept because auth state is user-level
// rather than keg-level — there is no default keg to resolve. Callers
// in server.go pass nothing extra; the signature asymmetry is
// intentional, not an oversight.
func registerAuthTools(srv *sdkmcp.Server, tap *tapper.Tap) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "auth_status",
		Description: "Report the login status for a tapper hub stored in the auth store.",
		Annotations: &sdkmcp.ToolAnnotations{
			// Status is a pure read over local state — no network,
			// no mutation. ReadOnlyHint lets clients cache and
			// OpenWorldHint=false signals no external effects.
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in authStatusInput) (*sdkmcp.CallToolResult, any, error) {
		result, err := tap.AuthStatus(ctx, tapper.AuthStatusOptions{Hub: in.Hub})
		if err != nil {
			return errorResult(err), nil, nil
		}
		// Emit Formatted verbatim so CLI and MCP are byte-identical.
		// The parity test asserts this; changing it here without
		// updating the CLI will break the parity guard.
		return textResult(result.Formatted), nil, nil
	})
}
