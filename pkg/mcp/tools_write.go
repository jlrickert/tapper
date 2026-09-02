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
	Data         string `json:"data" jsonschema:"complete validated KEG YAML document"`
	ExpectedHash string `json:"expected_hash" jsonschema:"precondition token returned by keg_settings"`
	Keg          string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerKegSettingsEdit(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "keg_settings_edit",
		Description: "Call keg_settings with minimal=false first, then replace the complete KEG settings with a validated YAML document using its hash as expected_hash. Requires admin access to the KEG itself, plus admin cover when a flight is selected. On conflict, merge into the returned current settings (or refetch with keg_settings) and retry with the returned current hash.",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in kegSettingsEditInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.KegSettingsEditOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			ExpectedHash:     in.ExpectedHash,
			Stream: &toolkit.Stream{
				IsPiped: true,
				In:      bytes.NewReader([]byte(in.Data)),
			},
		}
		if err := tap.KegSettingsEdit(ctx, opts); err != nil {
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
	Key    string   `json:"key"`
	Schema string   `json:"schema,omitempty" jsonschema:"schema selected for this write; required when strict policy and agent mode both block"`
	Title  string   `json:"title,omitempty"`
	Lead   string   `json:"lead,omitempty"`
	Body   string   `json:"body,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	// map[string]any, not map[string]string: a string-typed map generates an
	// `additionalProperties: {type: string}` schema, which cannot express an
	// integer at all, so schema fields typed `integer` become unwritable.
	Attrs map[string]any `json:"attrs,omitempty"`
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
	ExpectedHash   string `json:"expected_hash" jsonschema:"precondition token returned by cat"`
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
		Description: "Call cat first for every node, then atomically replace the content of 1-100 nodes using each returned hash as that edit's expected_hash. Each optional schema selection is required when strict policy and the resolved agent mode both block. On conflict, merge into the returned current content (or refetch with cat) and retry with the returned current hash.",
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
	ExpectedHash   string `json:"expected_hash" jsonschema:"precondition token returned by cat"`
	SnapshotBefore bool   `json:"snapshot_before,omitempty"`
}

func registerMeta(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "meta",
		Description: "Read metadata for 1-100 nodes without a token, or call cat first and atomically replace metadata for 1-100 nodes using each returned hash as that update's expected_hash. Each optional schema selection on an update is required when strict policy and the resolved agent mode both block. On conflict, merge into the returned current metadata (or refetch with cat) and retry with the returned current hash.",
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
	Nodes []removeNodeInput `json:"nodes" jsonschema:"1-100 nodes to remove atomically"`
	Keg   string            `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

type removeNodeInput struct {
	NodeID       string `json:"node_id"`
	ExpectedHash string `json:"expected_hash" jsonschema:"precondition token returned by cat"`
}

func registerRemove(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "remove",
		Description: "Call cat first for every node, then atomically remove 1-100 nodes using each returned hash as that node's expected_hash. On conflict, refetch with cat and retry with the returned current hash.",
		InputSchema: boundedMutationInputSchema[removeInput]("nodes"),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in removeInput) (*sdkmcp.CallToolResult, any, error) {
		nodeIDs := make([]string, len(in.Nodes))
		expectedHashes := make(map[string]string, len(in.Nodes))
		for i, node := range in.Nodes {
			nodeIDs[i] = node.NodeID
			expectedHashes[node.NodeID] = node.ExpectedHash
		}
		opts := tapper.RemoveOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeIDs:          nodeIDs,
			ExpectedHashes:   expectedHashes,
		}

		if err := tap.Remove(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("removed %d node(s)", len(in.Nodes))), nil, nil
	})
}

// --- move ---

type moveInput struct {
	SourceID     string `json:"source_id" jsonschema:"source node ID"`
	DestID       string `json:"dest_id" jsonschema:"destination node ID"`
	ExpectedHash string `json:"expected_hash" jsonschema:"precondition token returned by cat"`
	Keg          string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerMove(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "move",
		Description: "Call cat first, then move (rename) a KEG node to a new ID using the returned hash as expected_hash. On conflict, refetch with cat and retry with the returned current hash.",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in moveInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.MoveOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			SourceID:         in.SourceID,
			DestID:           in.DestID,
			ExpectedHash:     in.ExpectedHash,
		}

		if err := tap.Move(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("moved node %s to %s", in.SourceID, in.DestID)), nil, nil
	})
}
