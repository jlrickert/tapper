package mcp

import (
	"context"
	"fmt"
	"slices"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// AlwaysAvailableTools stay listed and callable under every tool allowlist:
// they carry no KEG authority. orient and guide are how a session learns what
// it may do; session_info reports the identity and authority it already acts
// with; session_refresh retries a failed flight activation and cannot change
// a working one.
var AlwaysAvailableTools = []string{"orient", "guide", "session_info", "session_refresh"}

// ToolAllowlist limits a server to an agent's tools. The zero value (and a
// nil *ToolAllowlist) admits every tool.
type ToolAllowlist struct {
	// Resolve reloads the live policy for each tool request. Errors fail closed.
	Resolve func(context.Context) (*ToolAllowlist, error)
	// Names are the admitted tools; empty admits every tool unless Refused
	// is set.
	Names []string
	// Refused, when set, admits no tools beyond AlwaysAvailableTools and is
	// the reason a refused call reports.
	Refused string
	// Agent names whose tools these are, for refusal messages.
	Agent string
}

// Allows reports whether the allowlist admits the tool name.
func (a *ToolAllowlist) Allows(name string) bool {
	if a == nil || slices.Contains(AlwaysAvailableTools, name) {
		return true
	}
	if a.Refused != "" {
		return false
	}
	return len(a.Names) == 0 || slices.Contains(a.Names, name)
}

// Middleware filters tools/list to the admitted tools and answers a
// tools/call outside them with a tool error, so the model sees why.
func (a *ToolAllowlist) Middleware(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
	return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
		if a != nil && a.Resolve != nil && (method == "tools/list" || method == "tools/call") {
			policy, err := a.Resolve(ctx)
			if err != nil {
				policy = &ToolAllowlist{Agent: a.Agent, Refused: fmt.Sprintf("agent %s could not be loaded: %v", a.Agent, err)}
			}
			if policy == nil {
				policy = &ToolAllowlist{Agent: a.Agent, Refused: "agent policy is unavailable"}
			}
			return policy.Middleware(next)(ctx, method, req)
		}
		switch method {
		case "tools/list":
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			list, ok := result.(*sdkmcp.ListToolsResult)
			if !ok {
				return result, nil
			}
			filtered := *list
			filtered.Tools = slices.DeleteFunc(slices.Clone(list.Tools), func(tool *sdkmcp.Tool) bool {
				return !a.Allows(tool.Name)
			})
			return &filtered, nil
		case "tools/call":
			params, _ := req.GetParams().(*sdkmcp.CallToolParamsRaw)
			if params != nil && !a.Allows(params.Name) {
				msg := a.Refused
				if msg == "" {
					msg = fmt.Sprintf("tool %q is not one of agent %s's tools", params.Name, a.Agent)
				}
				return &sdkmcp.CallToolResult{
					IsError: true,
					Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: msg}},
				}, nil
			}
		}
		return next(ctx, method, req)
	}
}
