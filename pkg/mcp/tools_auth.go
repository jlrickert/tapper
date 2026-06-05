package mcp

// MCP surface for the auth subsystem. Today the tool set is read-only:
// only `auth_status` is exposed. `login` stays CLI-only because it
// requires an interactive browser round-trip (the device flow) or a
// pasted token that an agent cannot complete, and `logout` stays CLI-only
// because silent revocation by an agent is a surprise factor we are not
// willing to underwrite.
//
// If a future use case demands an MCP writer, prefer adding a
// narrowly-scoped "auth_revoke" with explicit consent annotations over
// reusing the CLI logout path.

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// authStatusInput mirrors AuthStatusOptions: flat (no keg target) because
// auth state is user-level, not keg-level. Agents that omit Hub get the
// single-hub auto-resolve behavior, the same as the CLI.
type authStatusInput struct {
	Hub     string `json:"hub,omitempty" jsonschema:"hub URL to query; omit when exactly one hub is stored"`
	Offline bool   `json:"offline,omitempty" jsonschema:"skip the live hub check and report from the local store only"`
}

// registerAuthTools omits the KegDefaults parameter that sibling
// register*Tools functions accept because auth state is user-level
// rather than keg-level — there is no default keg to resolve. Callers
// in server.go pass nothing extra; the signature asymmetry is
// intentional, not an oversight.
func registerAuthTools(srv *sdkmcp.Server, tap *tapper.Tap) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "auth_status",
		Description: "Report the login status for a tapper hub and validate the stored token against the hub (pass offline:true to check the local store only).",
		Annotations: &sdkmcp.ToolAnnotations{
			// Read-only (no mutation), but it now reaches the hub to
			// validate the token, so OpenWorldHint=true. Agents that must
			// avoid outbound calls pass offline:true.
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in authStatusInput) (*sdkmcp.CallToolResult, any, error) {
		result, err := tap.AuthStatus(ctx, tapper.AuthStatusOptions{Hub: in.Hub, Offline: in.Offline})
		if err != nil {
			return errorResult(err), nil, nil
		}
		// Emit Formatted verbatim so CLI and MCP are byte-identical.
		// The parity test asserts this; changing it here without
		// updating the CLI will break the parity guard.
		return textResult(result.Formatted), nil, nil
	})
}
