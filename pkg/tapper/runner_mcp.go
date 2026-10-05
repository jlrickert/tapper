package tapper

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewRunnerMCPServer returns the `tap runner serve` MCP server: one tool per
// installed runner.
func NewRunnerMCPServer(svc *RunnerService, version string) *sdkmcp.Server {
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "tap-runner", Title: "Coding runners", Version: version}, nil)
	for _, runner := range svc.Installed {
		registerRunnerTool(srv, svc, runner)
	}
	return srv
}

func registerRunnerTool(srv *sdkmcp.Server, svc *RunnerService, runner InstalledRunner) {
	name, openWorld := runner.Name, true
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:  runner.Tool,
		Title: "Run " + runner.Title,
		Description: fmt.Sprintf("Hand a coding task to %s on this machine and return its final reply. "+
			"It works in cwd, which must be under the relay's allowed roots, and runs non-interactively for up to %s. "+
			"Pass the session id from a result to continue that conversation. %s",
			runner.Title, svc.Timeout, runner.Permissions),
		// A runner may edit files and reach the network, so the tool is
		// neither read-only nor closed-world; DestructiveHint is left at its
		// default (true) for the same reason.
		Annotations: &sdkmcp.ToolAnnotations{OpenWorldHint: &openWorld},
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in RunnerInput) (*sdkmcp.CallToolResult, RunnerOutput, error) {
		out, err := svc.Run(ctx, name, in)
		if err != nil {
			return nil, RunnerOutput{}, err
		}
		text := out.Reply
		if out.Session != "" {
			text = fmt.Sprintf("[%s session %s; pass it as session to continue]\n\n%s", out.Runner, out.Session, out.Reply)
		}
		return &sdkmcp.CallToolResult{
			IsError: out.IsError,
			Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: text}},
		}, out, nil
	})
}
