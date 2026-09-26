package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type namespaceListInput struct{}

type namespaceSearchInput struct {
	Query string `json:"query,omitempty" jsonschema:"text to match against namespace names and display names; empty browses"`
}

// registerNamespaceTools exposes namespace discovery over MCP, at parity with
// `tap namespace list` and `tap namespace search`. Discovery never grants
// authority. Member rosters and role changes stay UI-only, so those tools are
// intentionally not registered.
func registerNamespaceTools(srv *sdkmcp.Server, namespaces NamespaceProvider) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "namespace_list",
		Description: "List the namespaces you belong to, with your role in each",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, _ namespaceListInput) (*sdkmcp.CallToolResult, any, error) {
		rows, err := namespaces.ListNamespaces(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		lines := make([]string, 0, len(rows))
		for _, ns := range rows {
			lines = append(lines, fmt.Sprintf("@%s\t%s\t%s", ns.Name, ns.Kind, ns.Role))
		}
		res := linesResult(lines)
		res.StructuredContent = map[string]any{"namespaces": rows}
		return res, nil, nil
	})

	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "namespace_search",
		Description: "Search the people and org namespaces visible on the Hub, not only your own. Results are discovery only and never grant access; role is set where you are a member. Use keg_search and flight_search to find their kegs and flights.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(true),
		},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in namespaceSearchInput) (*sdkmcp.CallToolResult, any, error) {
		found, err := namespaces.SearchNamespaces(ctx, in.Query)
		if err != nil {
			return errorResult(err), nil, nil
		}
		lines := make([]string, 0, len(found.Namespaces)+1)
		if found.Truncated {
			lines = append(lines, "Results truncated; refine the query.")
		}
		for _, ns := range found.Namespaces {
			lines = append(lines, fmt.Sprintf("@%s\t%s\t%s\t%s", ns.Name, ns.Kind, tsvField(ns.DisplayName), ns.Role))
		}
		res := linesResult(lines)
		res.StructuredContent = found
		return res, nil, nil
	})
}
