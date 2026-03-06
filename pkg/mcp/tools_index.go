package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerIndexTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerIndex(srv, tap, defaults)
	registerListIndexes(srv, tap, defaults)
	registerIndexCat(srv, tap, defaults)
}

// --- index (rebuild) ---

type indexInput struct {
	Rebuild bool   `json:"rebuild,omitempty" jsonschema:"force a full index rebuild"`
	Keg     string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerIndex(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "index",
		Description: "Rebuild KEG indexes (nodes, tags, links, backlinks)",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in indexInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.IndexOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			Rebuild:          in.Rebuild,
		}
		result, err := tap.Index(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(result), nil, nil
	})
}

// --- list_indexes ---

type listIndexesInput struct {
	Keg string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerListIndexes(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "list_indexes",
		Description: "List available index files for a KEG",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in listIndexesInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.IndexCatOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
		}
		names, err := tap.ListIndexes(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return linesResult(names), nil, nil
	})
}

// --- index_cat ---

type indexCatInput struct {
	Name string `json:"name" jsonschema:"index file name (e.g. nodes.tsv, tags, links, backlinks, changes.md)"`
	Keg  string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerIndexCat(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "index_cat",
		Description: "Read the raw contents of a KEG index file",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in indexCatInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.IndexCatOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			Name:             in.Name,
		}
		result, err := tap.IndexCat(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		if result == "" {
			return textResult(fmt.Sprintf("index %q is empty", in.Name)), nil, nil
		}
		return textResult(result), nil, nil
	})
}
