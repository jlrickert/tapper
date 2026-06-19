package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

type namespaceListInput struct {
	Hub string `json:"hub,omitempty" jsonschema:"hub to query (default: the resolved default hub)"`
}

// registerNamespaceTools exposes namespace discovery over MCP. User and role
// management stays UI-only for now, so member-listing and mutating tools are
// intentionally not registered.
func registerNamespaceTools(srv *sdkmcp.Server, tap *tapper.Tap, _ KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "namespace_list",
		Description: "List the namespaces you belong to, with your role in each",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in namespaceListInput) (*sdkmcp.CallToolResult, any, error) {
		nss, err := tap.NamespaceList(ctx, tapper.NamespaceListOptions{Hub: in.Hub})
		if err != nil {
			return errorResult(err), nil, nil
		}
		lines := make([]string, 0, len(nss))
		for _, ns := range nss {
			lines = append(lines, fmt.Sprintf("@%s\t%s\t%s", ns.Name, ns.Kind, ns.Role))
		}
		return linesResult(lines), nil, nil
	})
}
