package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerArchiveTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerExport(srv, tap, defaults)
}

// --- export ---

type exportInput struct {
	Keg         string   `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	OutputPath  string   `json:"output_path" jsonschema:"filesystem path for the generated tar.gz archive"`
	NodeIDs     []string `json:"node_ids,omitempty" jsonschema:"node IDs to export (empty exports all nodes)"`
	WithHistory bool     `json:"with_history,omitempty" jsonschema:"include snapshot history in the archive"`
}

func registerExport(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "export",
		Description: "Export KEG nodes to a tar.gz archive file",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in exportInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.ExportOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			OutputPath:       in.OutputPath,
			NodeIDs:          in.NodeIDs,
			WithHistory:      in.WithHistory,
		}

		path, err := tap.Export(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("exported to %s", path)), nil, nil
	})
}
