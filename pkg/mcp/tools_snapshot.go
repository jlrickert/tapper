package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerSnapshotTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerNodeHistory(srv, tap, defaults)
	registerNodeSnapshot(srv, tap, defaults)
	registerNodeSnapshotView(srv, tap, defaults)
	registerNodeRestore(srv, tap, defaults)
}

// --- snapshot_list ---

type nodeHistoryInput struct {
	NodeID string `json:"node_id" jsonschema:"Node ID to show history for."`
	Keg    string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerNodeHistory(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "snapshot_list",
		Description:  "Memory: List a node's snapshot history.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in nodeHistoryInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.NodeHistoryOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
		}
		snapshots, err := tap.NodeHistory(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		if len(snapshots) == 0 {
			return textResult("no snapshots"), nil, nil
		}
		var lines []string
		for _, s := range snapshots {
			line := fmt.Sprintf("rev %d  %s", s.ID, s.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
			if s.Message != "" {
				line += "  " + s.Message
			}
			lines = append(lines, line)
		}
		return linesResult(lines), nil, nil
	})
}

// --- snapshot_create ---

type nodeSnapshotInput struct {
	Nodes []nodeSnapshotItemInput `json:"nodes" jsonschema:"1-100 nodes to snapshot atomically."`
	Keg   string                  `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}
type nodeSnapshotItemInput struct {
	NodeID  string `json:"node_id"`
	Message string `json:"message,omitempty"`
}

type nodeSnapshotOutput struct {
	NodeID   string `json:"node_id" jsonschema:"The node's ID."`
	Revision int64  `json:"revision" jsonschema:"The new snapshot's revision number."`
	Message  string `json:"message,omitempty" jsonschema:"The snapshot's message, when one was given."`
}

// snapshotCreateOutput is snapshot_create's structured content.
type snapshotCreateOutput struct {
	Results []nodeSnapshotOutput `json:"results" jsonschema:"One snapshot per node, in request order."`
}

func registerNodeSnapshot(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "snapshot_create",
		Description:  "Memory: Snapshot the current state of 1-100 nodes atomically.",
		InputSchema:  boundedMutationInputSchema[nodeSnapshotInput]("nodes"),
		OutputSchema: outputSchema[snapshotCreateOutput](),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in nodeSnapshotInput) (*sdkmcp.CallToolResult, any, error) {
		nodes := make([]tapper.BatchSnapshotItem, len(in.Nodes))
		for i, item := range in.Nodes {
			nodes[i] = tapper.BatchSnapshotItem{NodeID: item.NodeID, Message: item.Message}
		}
		snaps, err := tap.NodeSnapshotBatch(ctx, tapper.BatchSnapshotOptions{KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults), Nodes: nodes})
		if err != nil {
			return errorResult(err), nil, nil
		}
		message := fmt.Sprintf("created %d snapshot(s)", len(snaps))
		if len(snaps) == 1 {
			message = fmt.Sprintf("snapshot rev %d created", snaps[0].ID)
		}
		res := textResult(message)
		structured := make([]nodeSnapshotOutput, len(snaps))
		for i, snap := range snaps {
			structured[i] = nodeSnapshotOutput{NodeID: snap.Node.Path(), Revision: int64(snap.ID), Message: snap.Message}
		}
		res.StructuredContent = snapshotCreateOutput{Results: structured}
		return res, nil, nil
	})
}

// --- snapshot_read ---

type nodeSnapshotViewInput struct {
	NodeID string `json:"node_id" jsonschema:"Node ID to view."`
	Rev    string `json:"rev" jsonschema:"Revision number to view."`
	Keg    string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerNodeSnapshotView(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "snapshot_read",
		Description:  "Memory: Read a node's content at a previous snapshot revision.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in nodeSnapshotViewInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.NodeSnapshotViewOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
			Rev:              in.Rev,
		}
		content, err := tap.NodeSnapshotView(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(string(content)), nil, nil
	})
}

// --- snapshot_restore ---

type nodeRestoreInput struct {
	NodeID string `json:"node_id" jsonschema:"Node ID to restore."`
	Rev    string `json:"rev" jsonschema:"Revision number to restore to."`
	Keg    string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerNodeRestore(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "snapshot_restore",
		Description:  "Memory: Restore a node to a previous snapshot revision.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in nodeRestoreInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.NodeRestoreOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
			Rev:              in.Rev,
		}
		if err := tap.NodeRestore(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("node %s restored to rev %s", in.NodeID, strings.TrimSpace(in.Rev))), nil, nil
	})
}
