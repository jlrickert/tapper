package mcp

import (
	"context"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// integrateInput is the parameter surface of mcp__tapper__integrate.
// Host is required; DryRun, Plugins, and Scope are optional. Keg rides along
// for KegTargetOptions API consistency and is currently unused by
// Integrate.
type integrateInput struct {
	Host    string   `json:"host"              jsonschema:"host identifier (e.g. 'claude' or 'codex')"`
	Keg     string   `json:"keg,omitempty"     jsonschema:"keg alias; reserved for future per-keg customization"`
	DryRun  bool     `json:"dry_run,omitempty" jsonschema:"when true, return target paths without writing any files"`
	Plugins []string `json:"plugins,omitempty" jsonschema:"optional embedded plugin names to add after the mandatory tapper baseline"`
	Scope   string   `json:"scope,omitempty" jsonschema:"host install scope; defaults to user (Claude also supports project and local)"`
}

// registerIntegrateTools wires the integrate surface onto srv.
func registerIntegrateTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerIntegrate(srv, tap, defaults)
}

func registerIntegrate(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "integrate",
		Description: "Extract and install the embedded native Tapper marketplace for Claude or Codex. Always installs the tapper baseline; plugins adds optional embedded plugins. Scope defaults to user. Dry-run reports paths and commands without side effects.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in integrateInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.IntegrateOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			Host:             in.Host,
			DryRun:           in.DryRun,
			Plugins:          in.Plugins,
			Scope:            in.Scope,
		}
		result, err := tap.Integrate(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		prefix := "Extracted:"
		if in.DryRun {
			prefix = "Would extract:"
		}
		lines := make([]string, 0, len(result.Paths)+2)
		lines = append(lines, prefix)
		lines = append(lines, "  "+result.Root)
		for _, p := range result.Paths {
			lines = append(lines, "  "+p)
		}
		if in.DryRun {
			lines = append(lines, "Would run:")
			for _, command := range result.Commands {
				lines = append(lines, "  "+strings.Join(command, " "))
			}
		}
		return linesResult(lines), nil, nil
	})
}
