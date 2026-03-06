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
	registerNodeRestore(srv, tap, defaults)
}

// --- node_history ---

type nodeHistoryInput struct {
	NodeID string `json:"node_id" jsonschema:"node ID to show history for"`
	Keg    string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerNodeHistory(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "node_history",
		Description: "List snapshot history for a node",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in nodeHistoryInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.NodeHistoryOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
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

// --- node_snapshot ---

type nodeSnapshotInput struct {
	NodeID  string `json:"node_id" jsonschema:"node ID to snapshot"`
	Message string `json:"message,omitempty" jsonschema:"optional snapshot message"`
	Keg     string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerNodeSnapshot(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "node_snapshot",
		Description: "Create a snapshot of a node's current state",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in nodeSnapshotInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.NodeSnapshotOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			NodeID:           in.NodeID,
			Message:          in.Message,
		}
		snap, err := tap.NodeSnapshot(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("snapshot rev %d created", snap.ID)), nil, nil
	})
}

// --- node_restore ---

type nodeRestoreInput struct {
	NodeID string `json:"node_id" jsonschema:"node ID to restore"`
	Rev    string `json:"rev" jsonschema:"revision number to restore to"`
	Keg    string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerNodeRestore(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "node_restore",
		Description: "Restore a node to a previous snapshot revision",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in nodeRestoreInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.NodeRestoreOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			NodeID:           in.NodeID,
			Rev:              in.Rev,
		}
		if err := tap.NodeRestore(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("node %s restored to rev %s", in.NodeID, strings.TrimSpace(in.Rev))), nil, nil
	})
}
