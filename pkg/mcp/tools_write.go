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
		if property.Items != nil {
			constrainSchemaTypeField(property.Items)
		}
	}
	constrainSchemaTypeField(schema)
	return schema
}

// constrainSchemaTypeField gives an object's schema-selection property the
// same rule keg.ValidSchemaTypeName enforces. Struct tags cannot carry a
// pattern, so it is set on the derived schema; the SDK then refuses a
// sentence or a path as a schema before the handler runs.
func constrainSchemaTypeField(object *jsonschema.Schema) {
	property := object.Properties["schema"]
	if property == nil {
		return
	}
	maxLength := keg.SchemaTypeNameMaxLength
	property.Pattern = keg.SchemaTypeNamePattern
	property.MaxLength = &maxLength
}

// constrainedInputSchema derives T's input schema with its schema-selection
// field constrained, for tools that take one schema outside a batch.
func constrainedInputSchema[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("derive MCP input schema: %v", err))
	}
	constrainSchemaTypeField(schema)
	return schema
}

func registerWriteTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerCreate(srv, tap, defaults)
	registerEdit(srv, tap, defaults)
	registerRemove(srv, tap, defaults)
	registerMove(srv, tap, defaults)
	registerMerge(srv, tap, defaults)
	registerReplace(srv, tap, defaults)
	registerKegSettingsEdit(srv, tap, defaults)
}

// --- keg_settings_edit ---

type kegSettingsEditInput struct {
	Data         string `json:"data" jsonschema:"Complete validated KEG YAML document."`
	ExpectedHash string `json:"expected_hash" jsonschema:"Precondition token returned by keg_settings_read."`
	Keg          string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerKegSettingsEdit(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "keg_settings_edit",
		Description:  "Manage: Call keg_settings_read with minimal=false first, then replace the complete KEG settings with a validated YAML document using its hash as expected_hash. Requires admin access to the KEG itself, plus admin cover when a flight is selected, or a Hub flight that manages the namespace's KEGs (manage_kegs). On conflict, merge into the returned current settings (or refetch with keg_settings_read) and retry with the returned current hash.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in kegSettingsEditInput) (*sdkmcp.CallToolResult, any, error) {
		target := resolveKegTarget(ctx, in.Keg, defaults)
		target.KegAdministration = true
		opts := tapper.KegSettingsEditOptions{
			KegTargetOptions: target,
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
	Nodes []createNodeInput `json:"nodes" jsonschema:"1-100 nodes to create atomically."`
	Keg   string            `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}
type createNodeInput struct {
	Key     string `json:"key" jsonschema:"Caller-chosen label for this node within the batch, unique across the batch. Other nodes in the same batch refer to it by writing {{node:KEY}} in their content; each placeholder is replaced with the bare id this node is assigned, so link to it as [Title](../{{node:KEY}}) (a placeholder that is the whole link target gets the ../ added)."`
	Content string `json:"content" jsonschema:"The node's complete markdown body, opening with an H1 title. Must not start with a YAML frontmatter block: metadata goes in the meta field."`
	Meta    string `json:"meta,omitempty" jsonschema:"The node's complete metadata document as YAML, including keys such as type, tags, and any schema-defined attributes. Omit for no metadata."`
	Schema  string `json:"schema,omitempty" jsonschema:"Schema selected for this write: a type name such as task, one of the types schema_list returns, never a description. Writes meta.type; a different type declared in meta is a hard error, not an override. Required on a strict keg (the default for new kegs), where type in meta alone does not count as a selection."`
}

type createNodeOutput struct {
	Key        string                      `json:"key" jsonschema:"The key the node was sent with."`
	NodeID     string                      `json:"node_id" jsonschema:"The ID the node was assigned."`
	Hash       string                      `json:"hash" jsonschema:"The node's combined hash."`
	Validation *keg.SchemaValidationResult `json:"validation,omitempty" jsonschema:"How the node validated against its schema, when it has one."`
}

// nodeCreateOutput is node_create's structured content.
type nodeCreateOutput struct {
	Results []createNodeOutput `json:"results" jsonschema:"One entry per node created, in request order."`
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
		Name: "node_create",
		Description: "Memory: Atomically create 1-100 KEG nodes. " +
			"Each node is a markdown content document plus an optional YAML metadata document. " +
			"content must not begin with a YAML frontmatter block — metadata goes in meta. " +
			"A node's title is its content H1 and its lead is the first paragraph, so there are no separate title, lead, tags, or attrs fields. " +
			"Nodes in one batch can reference each other's not-yet-assigned ids by writing {{node:KEY}} in content: it becomes the bare id, so a link is [Title](../{{node:KEY}}) and a cross-KEG reference keg:@namespace/keg/{{node:KEY}}. " +
			"On a strict keg (the default for new kegs) every node needs schema, and type in meta alone does not count; call schema_list once per keg before the first write to learn its types.",
		InputSchema:  boundedMutationInputSchema[createInput]("nodes"),
		OutputSchema: outputSchema[nodeCreateOutput](),
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
		res.StructuredContent = nodeCreateOutput{Results: createNodeOutputs(results)}
		return res, nil, nil
	})
}

// --- edit ---

type editInput struct {
	Nodes []editItemInput `json:"nodes" jsonschema:"1-100 nodes to update atomically."`
	Keg   string          `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}
type editItemInput struct {
	NodeID              string  `json:"node_id" jsonschema:"ID of the node to update."`
	Content             *string `json:"content,omitempty" jsonschema:"Replacement markdown body, replacing the node's content entirely. Must not start with a YAML frontmatter block: metadata goes in the meta field. Omit to leave content unchanged."`
	Patch               *string `json:"patch,omitempty" jsonschema:"Unified diff (the @@ hunks of diff -u) against the node's current content, an alternative to content. Each hunk's context and removed lines must match the current content exactly; header line numbers are a hint. Prefer it for small changes to long notes. The body keeps its line endings; a '\\ No newline at end of file' marker may only follow a hunk that reaches the end."`
	Meta                *string `json:"meta,omitempty" jsonschema:"Replacement metadata document as YAML, replacing the node's metadata entirely. Omit to leave metadata unchanged."`
	ExpectedContentHash string  `json:"expected_content_hash,omitempty" jsonschema:"The content_hash from node_read or a previous node_edit result. Required with content or patch."`
	ExpectedMetaHash    string  `json:"expected_meta_hash,omitempty" jsonschema:"The meta_hash from node_read or a previous node_edit result. Required with meta."`
	Schema              string  `json:"schema,omitempty" jsonschema:"Schema selected for this write: a type name such as task, one of the types schema_list returns, never a description. Writes meta.type; a different type declared in meta is a hard error, not an override. On a strict keg (the default for new kegs) required whenever meta is supplied; a content-only edit keeps the node's current type and needs none."`
}

// nodeUpdateOutput carries the written node's tokens. content_hash and
// meta_hash are what a follow-up node_edit echoes back, so a second edit
// needs no re-read; hash is the combined token node_delete and node_move take.
type nodeUpdateOutput struct {
	NodeID      string                      `json:"node_id" jsonschema:"The node's ID."`
	Hash        string                      `json:"hash" jsonschema:"The node's new combined hash."`
	ContentHash string                      `json:"content_hash,omitempty" jsonschema:"The node's new content_hash, for the next edit."`
	MetaHash    string                      `json:"meta_hash,omitempty" jsonschema:"The node's new meta_hash, for the next edit."`
	Validation  *keg.SchemaValidationResult `json:"validation,omitempty" jsonschema:"How the node validated against its schema, when it has one."`
}

// nodeEditOutput is node_edit's structured content.
type nodeEditOutput struct {
	Results []nodeUpdateOutput `json:"results" jsonschema:"One entry per node updated, in request order."`
}

func nodeUpdateOutputs(results []keg.NodeUpdateResult) []nodeUpdateOutput {
	out := make([]nodeUpdateOutput, len(results))
	for i, item := range results {
		out[i] = nodeUpdateOutput{NodeID: item.ID.Path(), Hash: item.Hash, ContentHash: item.ContentHash, MetaHash: item.MetaHash, Validation: item.Validation}
	}
	return out
}

func registerEdit(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "node_edit",
		Description: "Memory: Atomically change the content and/or metadata of 1-100 nodes. " +
			"For each node supply content or patch, meta, or both; at least one is required. " +
			"content is the full replacement markdown body and must not begin with a YAML frontmatter block — metadata goes in meta. " +
			"patch is a unified diff against the current body (@@ -a,b +c,d @@ hunks of context ' ', removed '-' and added '+' lines), an alternative to content: prefer it for small changes to a long note, since only the changed lines are sent. " +
			"Each hunk's context and removed lines must match the current body exactly and in only one place; the header line numbers are a hint. A patch that does not fit is refused with the lines it expected and nothing is written: re-read the node and rebuild the patch, or send content. " +
			"A patch keeps the body's line endings (CRLF stays CRLF), and '\\ No newline at end of file' may only follow a hunk that reaches the end of the body. " +
			"Each part has its own precondition token: pass node_read's content_hash as expected_content_hash when supplying content or patch, and its meta_hash as expected_meta_hash when supplying meta. " +
			"Editing content never invalidates meta_hash and editing meta never invalidates content_hash, so a concurrent edit to the other part does not conflict. " +
			"Call node_read first for every node; node_read meta_only reads metadata. A part sent without its token is refused, writing nothing, with the exact node_read call that supplies every missing token. " +
			"Each result returns the node's new content_hash and meta_hash, so a follow-up edit needs no re-read; its combined hash (for node_delete or node_move) changes with every edit. " +
			"Take a snapshot with snapshot_create before a large or destructive edit. " +
			"On a strict keg (the default for new kegs) an edit that supplies meta needs schema; a content-only edit keeps the node's current type and is validated against it. " +
			"On conflict, merge into the returned current content (or refetch with node_read) and retry with the returned current hash for the part you are writing: currentContentHash as expected_content_hash, currentMetaHash as expected_meta_hash.",
		InputSchema:  boundedMutationInputSchema[editInput]("nodes"),
		OutputSchema: outputSchema[nodeEditOutput](),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in editInput) (*sdkmcp.CallToolResult, any, error) {
		ctx = keg.WithValidationActor(ctx, keg.ValidationActorAgent)
		edits := make([]tapper.BatchEditItem, len(in.Nodes))
		for i, item := range in.Nodes {
			edit := tapper.BatchEditItem{NodeID: item.NodeID, Schema: item.Schema, ExpectedContentHash: item.ExpectedContentHash, ExpectedMetaHash: item.ExpectedMetaHash}
			if item.Content != nil {
				edit.Content, edit.HasContent = *item.Content, true
			}
			if item.Patch != nil {
				edit.ContentPatch, edit.HasContentPatch = *item.Patch, true
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
		res.StructuredContent = nodeEditOutput{Results: nodeUpdateOutputs(results)}
		return res, nil, nil
	})
}

// --- remove ---

type removeInput struct {
	Nodes []removeNodeInput `json:"nodes" jsonschema:"1-100 nodes to remove atomically."`
	Keg   string            `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

type removeNodeInput struct {
	NodeID       string `json:"node_id"`
	ExpectedHash string `json:"expected_hash" jsonschema:"The combined hash from node_read, not content_hash or meta_hash."`
}

func registerRemove(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "node_delete",
		Description: "Memory: Call node_read first for every node, then atomically remove 1-100 nodes using the hash from node_read (the combined hash, not content_hash or meta_hash) as that node's expected_hash. On conflict, refetch with node_read and retry with the returned current hash. " +
			"Where the keg keeps a trash, a removed node goes there with its history and assets and trash_restore brings it back; links to it are left alone until it is purged. Otherwise the node and its snapshots are gone and links to it point at node 0.",
		InputSchema:  boundedMutationInputSchema[removeInput]("nodes"),
		OutputSchema: textOutputSchema(),
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
	SourceID     string `json:"source_id" jsonschema:"Source node ID."`
	DestID       string `json:"dest_id" jsonschema:"Destination node ID."`
	ExpectedHash string `json:"expected_hash" jsonschema:"The combined hash from node_read, not content_hash or meta_hash."`
	Keg          string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerMove(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "node_move",
		Description:  "Memory: Call node_read first, then move (rename) a KEG node to a new ID using the hash from node_read (the combined hash, not content_hash or meta_hash) as expected_hash. The destination must be free: the id of a node in the trash stays reserved until it is restored or purged. On conflict, refetch with node_read and retry with the returned current hash.",
		OutputSchema: textOutputSchema(),
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
