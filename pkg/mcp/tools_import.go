package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerImportTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerImportFromKeg(srv, tap, defaults)
}

// --- import_from_keg ---

type importFromKegInput struct {
	SourceKeg    string   `json:"source_keg" jsonschema:"source keg alias to import nodes from"`
	NodeIDs      []string `json:"node_ids,omitempty" jsonschema:"source node IDs to import (empty imports all non-zero nodes)"`
	TargetKeg    string   `json:"target_keg,omitempty" jsonschema:"target keg alias (uses default if empty)"`
	Flight       string   `json:"flight,omitempty" jsonschema:"flight ref to cap available kegs (uses server default if empty)"`
	TagQuery     string   `json:"tag_query,omitempty" jsonschema:"boolean tag expression to select additional source nodes"`
	LeaveStubs   bool     `json:"leave_stubs,omitempty" jsonschema:"write forwarding stubs at source locations after import"`
	SkipZeroNode bool     `json:"skip_zero_node,omitempty" jsonschema:"skip importing the source keg zero node"`
}

func registerImportFromKeg(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "import_from_keg",
		Description: "Import nodes from one KEG into another, rewriting links",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in importFromKegInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.ImportFromKegOptions{
			Source:       resolveKegTargetWithFlight(in.SourceKeg, in.Flight, defaults),
			Target:       resolveKegTargetWithFlight(in.TargetKeg, in.Flight, defaults),
			NodeIDs:      in.NodeIDs,
			TagQuery:     in.TagQuery,
			LeaveStubs:   in.LeaveStubs,
			SkipZeroNode: in.SkipZeroNode,
		}

		imported, err := tap.ImportFromKeg(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}

		lines := make([]string, len(imported))
		for i, node := range imported {
			lines[i] = fmt.Sprintf("%s -> %s", node.SourceID.Path(), node.TargetID.Path())
		}
		summary := fmt.Sprintf("imported %d node(s)\n%s", len(imported), strings.Join(lines, "\n"))
		return textResult(summary), nil, nil
	})
}
