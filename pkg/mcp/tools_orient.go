package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// orientInput is the parameter surface of mcp__tapper__orient. Every
// field is optional: a bare call returns the tier-0 payload with an
// auto-detected keg and no host-specific content.
type orientInput struct {
	Host   string `json:"host,omitempty"   jsonschema:"host identifier for host-specific payload (e.g. 'claude' or 'codex')"`
	Keg    string `json:"keg,omitempty"    jsonschema:"keg alias; reserved for per-keg manifest payloads"`
	Flight string `json:"flight,omitempty" jsonschema:"flight identifier; reserved for flight-scoped manifest payloads"`
	Tier   int    `json:"tier,omitempty"   jsonschema:"payload depth: 0 (purpose + active keg + rules summary), 1 (adds linking + snapshot), 2 (adds full canonical body and host-rendered bytes)"`
}

// registerOrientTools wires the orient surface onto srv. Called from
// NewServer alongside the other register*Tools helpers.
func registerOrientTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerOrient(srv, tap, defaults)
}

func registerOrient(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "orient",
		Description: "Return a tapper orientation payload at the requested tier. Tier 0 is bounded (purpose + active keg + rules summary). Tier 1 adds linking conventions and snapshot policy. Tier 2 adds the full canonical body; when host is set, the rendered host-specific bytes are appended.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in orientInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.OrientOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			Host:             in.Host,
			Flight:           in.Flight,
			Tier:             in.Tier,
		}
		payload, err := tap.Orient(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(payload), nil, nil
	})
}
