package mcp

import (
	"bytes"
	"context"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jlrickert/cli-toolkit/toolkit"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

func boundedMutationInputSchema[T any](arrayFields ...string) *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("derive MCP mutation schema: %v", err))
	}
	minItems, maxItems := 1, keg.MaxMutationBatchSize
	for _, field := range arrayFields {
		property := schema.Properties[field]
		if property == nil {
			panic(fmt.Sprintf("MCP mutation schema has no %q property", field))
		}
		property.MinItems = &minItems
		property.MaxItems = &maxItems
	}
	return schema
}

func registerWriteTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerCreate(srv, tap, defaults)
	registerEdit(srv, tap, defaults)
	registerMeta(srv, tap, defaults)
	registerRemove(srv, tap, defaults)
	registerMove(srv, tap, defaults)
	registerKegSettingsEdit(srv, tap, defaults)
}

// --- keg_settings_edit ---

type kegSettingsEditInput struct {
	Data string `json:"data" jsonschema:"complete validated KEG YAML document"`
	Keg  string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerKegSettingsEdit(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "keg_settings_edit",
		Description: "Replace the complete KEG configuration with a validated YAML document; requires admin flight authority and editor KEG access",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in kegSettingsEditInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.KegConfigEditOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			Stream: &toolkit.Stream{
				IsPiped: true,
				In:      bytes.NewReader([]byte(in.Data)),
			},
		}
		if err := tap.KegConfigEdit(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult("KEG settings updated"), nil, nil
	})
}

// --- create ---

type createInput struct {
	Nodes []createNodeInput `json:"nodes" jsonschema:"1-100 nodes to create atomically"`
	Keg   string            `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}
type createNodeInput struct {
	Key    string            `json:"key"`
	Schema string            `json:"schema,omitempty" jsonschema:"schema selected for this write; required when strict policy and agent mode both block"`
	Title  string            `json:"title,omitempty"`
	Lead   string            `json:"lead,omitempty"`
	Body   string            `json:"body,omitempty"`
	Tags   []string          `json:"tags,omitempty"`
	Attrs  map[string]string `json:"attrs,omitempty"`
}

type createNodeOutput struct {
	Key        string                      `json:"key"`
	NodeID     string                      `json:"node_id"`
	Hash       string                      `json:"hash"`
	Validation *keg.SchemaValidationResult `json:"validation,omitempty"`
}

func createNodeOutputs(results []keg.CreateNodeResult) []createNodeOutput {
	out := make([]createNodeOutput, len(results))
	for i, item := range results {
		out[i] = createNodeOutput{Key: item.Key, NodeID: item.ID.Path(), Hash: item.Hash, Validation: item.Validation}
	}
	return out
}

func registerCreate(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "create",
		Description: "Atomically create 1-100 KEG nodes with optional intra-batch references. Each optional schema selection is required when strict policy and the resolved agent mode both block.",
		InputSchema: boundedMutationInputSchema[createInput]("nodes"),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in createInput) (*sdkmcp.CallToolResult, any, error) {
		ctx = keg.WithValidationActor(ctx, keg.ValidationActorAgent)
		nodes := make([]tapper.BatchCreateNode, len(in.Nodes))
		for i, item := range in.Nodes {
			nodes[i] = tapper.BatchCreateNode{Key: item.Key, Schema: item.Schema, Title: item.Title, Lead: item.Lead, Body: item.Body, Tags: item.Tags, Attrs: item.Attrs}
		}
		results, err := tap.CreateBatch(ctx, tapper.BatchCreateOptions{KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults), Nodes: nodes})
		if err != nil {
			return errorResult(err), nil, nil
		}
		message := fmt.Sprintf("created %d node(s)", len(results))
		if len(results) == 1 {
			message = results[0].ID.String()
		}
		res := textResult(message)
		res.StructuredContent = map[string]any{"results": createNodeOutputs(results)}
		return res, nil, nil
	})
}

// --- edit ---

type editInput struct {
	Edits []editItemInput `json:"edits" jsonschema:"1-100 node replacements to apply atomically"`
	Keg   string          `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}
type editItemInput struct {
	NodeID         string `json:"node_id"`
	Schema         string `json:"schema,omitempty" jsonschema:"schema selected for this write; required when strict policy and agent mode both block"`
	Content        string `json:"content"`
	ExpectedHash   string `json:"expected_hash,omitempty"`
	SnapshotBefore bool   `json:"snapshot_before,omitempty"`
}

type nodeUpdateOutput struct {
	NodeID     string                      `json:"node_id"`
	Hash       string                      `json:"hash"`
	Validation *keg.SchemaValidationResult `json:"validation,omitempty"`
}

func nodeUpdateOutputs(results []keg.NodeUpdateResult) []nodeUpdateOutput {
	out := make([]nodeUpdateOutput, len(results))
	for i, item := range results {
		out[i] = nodeUpdateOutput{NodeID: item.ID.Path(), Hash: item.Hash, Validation: item.Validation}
	}
	return out
}

func registerEdit(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "edit",
		Description: "Atomically replace the content of 1-100 KEG nodes. Each optional schema selection is required when strict policy and the resolved agent mode both block.",
		InputSchema: boundedMutationInputSchema[editInput]("edits"),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in editInput) (*sdkmcp.CallToolResult, any, error) {
		ctx = keg.WithValidationActor(ctx, keg.ValidationActorAgent)
		edits := make([]tapper.BatchEditItem, len(in.Edits))
		for i, item := range in.Edits {
			edits[i] = tapper.BatchEditItem{NodeID: item.NodeID, Schema: item.Schema, Content: item.Content, ExpectedHash: item.ExpectedHash, SnapshotBefore: item.SnapshotBefore}
		}
		results, err := tap.EditBatch(ctx, tapper.BatchEditOptions{KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults), Edits: edits})
		if err != nil {
			return errorResult(err), nil, nil
		}
		res := textResult(fmt.Sprintf("updated %d node(s)", len(results)))
		res.StructuredContent = map[string]any{"results": nodeUpdateOutputs(results)}
		return res, nil, nil
	})
}

// --- meta ---

type metaInput struct {
	NodeIDs []string          `json:"node_ids,omitempty" jsonschema:"1-100 node IDs to read"`
	Updates []metaUpdateInput `json:"updates,omitempty" jsonschema:"1-100 metadata replacements to apply atomically"`
	Keg     string            `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}
type metaUpdateInput struct {
	NodeID         string `json:"node_id"`
	Schema         string `json:"schema,omitempty" jsonschema:"schema selected for this write; required when strict policy and agent mode both block"`
	Content        string `json:"content"`
	ExpectedHash   string `json:"expected_hash,omitempty"`
	SnapshotBefore bool   `json:"snapshot_before,omitempty"`
}

func registerMeta(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "meta",
		Description: "Read metadata for 1-100 nodes or atomically replace metadata for 1-100 nodes. Each optional schema selection on an update is required when strict policy and the resolved agent mode both block.",
		InputSchema: boundedMutationInputSchema[metaInput]("node_ids", "updates"),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in metaInput) (*sdkmcp.CallToolResult, any, error) {
		ctx = keg.WithValidationActor(ctx, keg.ValidationActorAgent)
		updates := make([]tapper.BatchMetaUpdate, len(in.Updates))
		for i, item := range in.Updates {
			updates[i] = tapper.BatchMetaUpdate{NodeID: item.NodeID, Schema: item.Schema, Content: item.Content, ExpectedHash: item.ExpectedHash, SnapshotBefore: item.SnapshotBefore}
		}
		reads, writes, err := tap.MetaBatch(ctx, tapper.BatchMetaOptions{KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults), NodeIDs: in.NodeIDs, Updates: updates})
		if err != nil {
			return errorResult(err), nil, nil
		}
		if len(writes) > 0 {
			res := textResult(fmt.Sprintf("updated metadata for %d node(s)", len(writes)))
			res.StructuredContent = map[string]any{"results": nodeUpdateOutputs(writes)}
			return res, nil, nil
		}
		message := fmt.Sprintf("read metadata for %d node(s)", len(reads))
		if len(reads) == 1 {
			message = reads[0].Content
		}
		res := textResult(message)
		res.StructuredContent = map[string]any{"results": reads}
		return res, nil, nil
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
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in removeInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.RemoveOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
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
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in moveInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.MoveOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			SourceID:         in.SourceID,
			DestID:           in.DestID,
		}

		if err := tap.Move(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("moved node %s to %s", in.SourceID, in.DestID)), nil, nil
	})
}
