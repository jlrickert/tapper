package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// integrateInput is the parameter surface of mcp__tapper__integrate.
// Host is required; DryRun and Target are optional. Keg rides along
// for KegTargetOptions API consistency and is currently unused by
// Integrate.
type integrateInput struct {
	Host   string `json:"host"              jsonschema:"host identifier (e.g. 'claude' or 'codex')"`
	Keg    string `json:"keg,omitempty"     jsonschema:"keg alias; reserved for future per-keg customization"`
	Flight string `json:"flight,omitempty" jsonschema:"flight ref to cap available kegs (uses server default if empty)"`
	DryRun bool   `json:"dry_run,omitempty" jsonschema:"when true, return target paths without writing any files"`
	Target string `json:"target,omitempty"  jsonschema:"override the default install directory (absolute path)"`
}

// registerIntegrateTools wires the integrate surface onto srv.
func registerIntegrateTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerIntegrate(srv, tap, defaults)
}

func registerIntegrate(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "integrate",
		Description: "Install the embedded tapper integration tree for a host (claude, codex) into the host's standard filesystem location. Returns the absolute target paths. When dry_run is true, the tool returns the paths without writing any files.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  false,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in integrateInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.IntegrateOptions{
			KegTargetOptions: resolveKegTargetWithFlight(in.Keg, in.Flight, defaults),
			Host:             in.Host,
			DryRun:           in.DryRun,
			Target:           in.Target,
		}
		targets, err := tap.Integrate(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		prefix := "Wrote:"
		if in.DryRun {
			prefix = "Would write:"
		}
		lines := make([]string, 0, len(targets)+1)
		lines = append(lines, prefix)
		for _, p := range targets {
			lines = append(lines, "  "+p)
		}
		return linesResult(lines), nil, nil
	})
}
