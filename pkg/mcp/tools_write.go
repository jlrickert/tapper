package mcp

import (
	"bytes"
	"context"
	"fmt"

	"github.com/jlrickert/cli-toolkit/toolkit"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerWriteTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerCreate(srv, tap, defaults)
	registerEdit(srv, tap, defaults)
	registerMeta(srv, tap, defaults)
	registerRemove(srv, tap, defaults)
	registerMove(srv, tap, defaults)
}

// --- create ---

type createInput struct {
	Title string            `json:"title,omitempty" jsonschema:"node title (H1 heading)"`
	Lead  string            `json:"lead,omitempty" jsonschema:"lead paragraph after the title"`
	Body  string            `json:"body,omitempty" jsonschema:"full markdown content (overrides title and lead if set)"`
	Tags  []string          `json:"tags,omitempty" jsonschema:"metadata tags"`
	Attrs map[string]string `json:"attrs,omitempty" jsonschema:"metadata attributes (e.g. entity=task)"`
	Keg   string            `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerCreate(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "create",
		Description: "Create a new KEG node",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in createInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.CreateOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			Title:            in.Title,
			Lead:             in.Lead,
			Tags:             in.Tags,
			Attrs:            in.Attrs,
		}

		if in.Body != "" {
			opts.Stream = &toolkit.Stream{
				IsPiped: true,
				In:      bytes.NewReader([]byte(in.Body)),
			}
		}

		id, err := tap.Create(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(id.String()), nil, nil
	})
}

// --- edit ---

type editInput struct {
	NodeID  string `json:"node_id" jsonschema:"node ID to edit"`
	Content string `json:"content" jsonschema:"full markdown content with optional YAML frontmatter"`
	Keg     string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerEdit(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "edit",
		Description: "Replace the content of a KEG node",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in editInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.EditOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			NodeID:           in.NodeID,
			Stream: &toolkit.Stream{
				IsPiped: true,
				In:      bytes.NewReader([]byte(in.Content)),
			},
		}

		if err := tap.Edit(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("node %s updated", in.NodeID)), nil, nil
	})
}

// --- meta ---

type metaInput struct {
	NodeID  string `json:"node_id" jsonschema:"node ID to inspect or update"`
	Content string `json:"content,omitempty" jsonschema:"YAML metadata to write (omit to read current metadata)"`
	Keg     string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerMeta(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "meta",
		Description: "Read or write node metadata (tags, attributes)",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in metaInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.MetaOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			NodeID:           in.NodeID,
		}

		if in.Content != "" {
			opts.Stream = &toolkit.Stream{
				IsPiped: true,
				In:      bytes.NewReader([]byte(in.Content)),
			}
		}

		result, err := tap.Meta(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		if in.Content != "" {
			return textResult(fmt.Sprintf("metadata for node %s updated", in.NodeID)), nil, nil
		}
		return textResult(result), nil, nil
	})
}

// --- remove ---

type removeInput struct {
	NodeIDs []string `json:"node_ids" jsonschema:"node IDs to remove"`
	Keg     string   `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerRemove(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "remove",
		Description: "Remove one or more KEG nodes",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in removeInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.RemoveOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			NodeIDs:          in.NodeIDs,
		}

		if err := tap.Remove(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("removed %d node(s)", len(in.NodeIDs))), nil, nil
	})
}

// --- move ---

type moveInput struct {
	SourceID string `json:"source_id" jsonschema:"source node ID"`
	DestID   string `json:"dest_id" jsonschema:"destination node ID"`
	Keg      string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerMove(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "move",
		Description: "Move (rename) a KEG node to a new ID",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in moveInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.MoveOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			SourceID:         in.SourceID,
			DestID:           in.DestID,
		}

		if err := tap.Move(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("moved node %s to %s", in.SourceID, in.DestID)), nil, nil
	})
}
