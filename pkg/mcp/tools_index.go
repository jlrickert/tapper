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
	Keg string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerIndex(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "index_rebuild",
		Description:  "Manage: Rebuild every KEG index (nodes, tags, links, backlinks) from scratch.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			IdempotentHint:  true,
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in indexInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.IndexOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
		}
		result, err := tap.Index(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(result), nil, nil
	})
}

// --- index_list ---

type listIndexesInput struct {
	Keg string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerListIndexes(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "index_list",
		Description:  "Memory: List a KEG's index files.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in listIndexesInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.IndexCatOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
		}
		names, err := tap.ListIndexes(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return linesResult(names), nil, nil
	})
}

// --- index_read ---

type indexCatInput struct {
	Name string `json:"name" jsonschema:"Index file name, such as nodes.tsv, tags, links, backlinks, or changes.md."`
	Keg  string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerIndexCat(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "index_read",
		Description:  "Memory: Read the raw contents of a KEG index file.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in indexCatInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.IndexCatOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
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
