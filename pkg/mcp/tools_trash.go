package mcp

import (
	"context"
	"fmt"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerTrashTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerTrashList(srv, tap, defaults)
	registerTrashRestore(srv, tap, defaults)
}

// --- trash_list ---

type trashListInput struct {
	Keg string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

type trashEntryOutput struct {
	NodeID     string `json:"node_id"`
	Title      string `json:"title"`
	Reason     string `json:"reason"`
	MergedInto string `json:"merged_into,omitempty"`
	TrashedAt  string `json:"trashed_at"`
	ExpiresAt  string `json:"expires_at,omitempty"`
	// DisplacedFrom is the id the node was trashed at, when an archive
	// restore needed it and the entry moved to node_id.
	DisplacedFrom string `json:"displaced_from,omitempty"`
}

func trashEntryOutputs(entries []keg.TrashEntry) []trashEntryOutput {
	out := make([]trashEntryOutput, len(entries))
	for i, entry := range entries {
		out[i] = trashEntryOutput{NodeID: entry.ID.Path(), Title: entry.Title, Reason: entry.Reason, TrashedAt: entry.TrashedAt.UTC().Format(time.RFC3339)}
		if entry.MergedInto != nil {
			out[i].MergedInto = entry.MergedInto.Path()
		}
		if entry.ExpiresAt != nil {
			out[i].ExpiresAt = entry.ExpiresAt.UTC().Format(time.RFC3339)
		}
		if entry.DisplacedFrom != nil {
			out[i].DisplacedFrom = entry.DisplacedFrom.Path()
		}
	}
	return out
}

func registerTrashList(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "trash_list",
		Description: "Memory: List a keg's removed nodes, newest first: id, title, why it was removed (manual, or merge with the node it was merged into), when, and when it will be purged for good. " +
			"Restore one with trash_restore. A trashed node's id stays reserved, and links to it are left in place until it is purged. " +
			"An archive restore that needs a trashed node's id moves the entry to a new id first; displaced_from names the id it was trashed at, and restoring puts it back at its new id.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in trashListInput) (*sdkmcp.CallToolResult, any, error) {
		entries, err := tap.TrashList(ctx, resolveKegTarget(ctx, in.Keg, defaults))
		if err != nil {
			return errorResult(err), nil, nil
		}
		if len(entries) == 0 {
			res := textResult("trash is empty")
			res.StructuredContent = map[string]any{"trash": []trashEntryOutput{}}
			return res, nil, nil
		}
		structured := trashEntryOutputs(entries)
		lines := make([]string, len(structured))
		for i, entry := range structured {
			line := fmt.Sprintf("%s  %s  %s", entry.NodeID, entry.TrashedAt, entry.Reason)
			if entry.MergedInto != "" {
				line += " into " + entry.MergedInto
			}
			if entry.DisplacedFrom != "" {
				line += " (moved from " + entry.DisplacedFrom + ")"
			}
			if entry.Title != "" {
				line += "  " + entry.Title
			}
			lines[i] = line
		}
		res := linesResult(lines)
		res.StructuredContent = map[string]any{"trash": structured}
		return res, nil, nil
	})
}

// --- trash_restore ---

type trashRestoreInput struct {
	NodeIDs []string `json:"node_ids" jsonschema:"1-100 trashed node ids to restore atomically"`
	Keg     string   `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerTrashRestore(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "trash_restore",
		Description: "Memory: Atomically restore 1-100 trashed nodes (see trash_list) at their own ids, with their content, metadata, history, files, and images. " +
			"Links that still name a restored node resolve again. A node merged into another comes back as it was before the merge; links the merge repointed stay on the node it was merged into.",
		InputSchema: boundedMutationInputSchema[trashRestoreInput]("node_ids"),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in trashRestoreInput) (*sdkmcp.CallToolResult, any, error) {
		if err := tap.TrashRestore(ctx, tapper.TrashRestoreOptions{KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults), NodeIDs: in.NodeIDs}); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("restored %d node(s)", len(in.NodeIDs))), nil, nil
	})
}
