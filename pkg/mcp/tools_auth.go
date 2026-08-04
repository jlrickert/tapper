package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type authInfoInput struct{}

type authInfoOutput struct {
	Identities []AuthIdentity `json:"identities"`
	Kegs       []string       `json:"kegs"`
}

// registerAuthInfoTool reports credential-safe identity context on every MCP
// transport. Credential material and account-private fields are intentionally
// absent from both the structured and human-readable response.
func registerAuthInfoTool(srv *sdkmcp.Server, _ KegDefaults, identities IdentityProvider, kegs KegDiscoveryProvider) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "auth_info",
		Description: "Report authenticated hub identities and active-flight kegs without exposing credentials or private account data",
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPtr(true)},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ authInfoInput) (*sdkmcp.CallToolResult, any, error) {
		found, err := identities.Identities(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		refs, err := kegs.ListKegs(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		out := authInfoOutput{Identities: found, Kegs: filterKegRefs(ctx, refs)}
		var lines []string
		for _, identity := range found {
			label := "@" + identity.Username
			if strings.TrimSpace(identity.DisplayName) != "" {
				label = identity.DisplayName + " (@" + identity.Username + ")"
			}
			lines = append(lines, fmt.Sprintf("%s — %s", identity.Hub, label))
			if identity.DefaultNamespace != "" {
				lines = append(lines, "Default namespace: @"+strings.TrimPrefix(identity.DefaultNamespace, "@"))
			}
			if len(identity.Namespaces) > 0 {
				lines = append(lines, "Namespaces: @"+strings.Join(identity.Namespaces, ", @"))
			}
		}
		if len(out.Kegs) > 0 {
			lines = append(lines, "Kegs:")
			lines = append(lines, out.Kegs...)
		}
		if len(lines) == 0 {
			lines = append(lines, "No authenticated hub identities")
		}
		res := textResult(strings.Join(lines, "\n"))
		res.StructuredContent = out
		return res, nil, nil
	})
}
