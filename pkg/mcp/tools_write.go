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
	Key     string `json:"key" jsonschema:"caller-chosen label for this node within the batch, unique across the batch. Other nodes in the same batch link to it by writing {{node:KEY}} in their content; each placeholder is replaced with the id this node is assigned."`
	Content string `json:"content" jsonschema:"the node's complete markdown body, opening with an H1 title. Must not start with a YAML frontmatter block: metadata goes in the meta field."`
	Meta    string `json:"meta,omitempty" jsonschema:"the node's complete metadata document as YAML, including keys such as type, tags, and any schema-defined attributes. Omit for no metadata."`
	Schema  string `json:"schema,omitempty" jsonschema:"schema selected for this write. Writes meta.type; a different type declared in meta is a hard error, not an override. Required when strict policy and agent mode both block."`
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
		Name: "create",
		Description: "Atomically create 1-100 KEG nodes. " +
			"Each node is a markdown content document plus an optional YAML metadata document. " +
			"content must not begin with a YAML frontmatter block — metadata goes in meta. " +
			"A node's title is its content H1 and its lead is the first paragraph, so there are no separate title, lead, tags, or attrs fields. " +
			"Nodes in one batch can reference each other's not-yet-assigned ids by writing {{node:KEY}} in content. " +
			"A schema selection is required only when strict policy and the resolved agent mode both block.",
		InputSchema: boundedMutationInputSchema[createInput]("nodes"),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in createInput) (*sdkmcp.CallToolResult, any, error) {
		ctx = keg.WithValidationActor(ctx, keg.ValidationActorAgent)
		nodes := make([]tapper.BatchCreateNode, len(in.Nodes))
		for i, item := range in.Nodes {
			nodes[i] = tapper.BatchCreateNode{Key: item.Key, Schema: item.Schema, Content: item.Content, Meta: item.Meta}
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
	Edits []editItemInput `json:"edits" jsonschema:"1-100 nodes to update atomically"`
	Keg   string          `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}
type editItemInput struct {
	NodeID       string  `json:"node_id" jsonschema:"id of the node to update"`
	Content      *string `json:"content,omitempty" jsonschema:"replacement markdown body, replacing the node's content entirely. Must not start with a YAML frontmatter block: metadata goes in the meta field. Omit to leave content unchanged."`
	Meta         *string `json:"meta,omitempty" jsonschema:"replacement metadata document as YAML, replacing the node's metadata entirely. Omit to leave metadata unchanged."`
	ExpectedHash string  `json:"expected_hash" jsonschema:"precondition token returned by cat. One hash covers content and metadata together, so a call that changes only one of them still invalidates the other's hash."`
	Schema       string  `json:"schema,omitempty" jsonschema:"schema selected for this write. Writes meta.type; a different type declared in meta is a hard error, not an override. Required when strict policy and agent mode both block."`
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
		Name: "edit",
		Description: "Atomically replace the content and/or metadata of 1-100 nodes. " +
			"Supply content, meta, or both for each node; at least one is required. " +
			"content is the markdown body and must not begin with a YAML frontmatter block — metadata goes in meta. " +
			"One expected_hash covers a node's content and metadata together, so changing either invalidates the hash for both. " +
			"Call cat first for every node and pass each returned hash as that node's expected_hash; cat meta_only reads metadata. " +
			"Take a snapshot with node_snapshot before a large or destructive edit. " +
			"A schema selection is required only when strict policy and the resolved agent mode both block. " +
			"On conflict, merge into the returned current content (or refetch with cat) and retry with the returned current hash.",
		InputSchema: boundedMutationInputSchema[editInput]("edits"),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in editInput) (*sdkmcp.CallToolResult, any, error) {
		ctx = keg.WithValidationActor(ctx, keg.ValidationActorAgent)
		edits := make([]tapper.BatchEditItem, len(in.Edits))
		for i, item := range in.Edits {
			edit := tapper.BatchEditItem{NodeID: item.NodeID, Schema: item.Schema, ExpectedHash: item.ExpectedHash}
			if item.Content != nil {
				edit.Content, edit.HasContent = *item.Content, true
			}
			if item.Meta != nil {
				edit.Meta, edit.HasMeta = *item.Meta, true
			}
			edits[i] = edit
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
