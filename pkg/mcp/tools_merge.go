package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// --- node_merge ---

type mergeInput struct {
	SourceID            string  `json:"source_id" jsonschema:"ID of the node merged away; it goes to the trash."`
	ExpectedSourceHash  string  `json:"expected_source_hash" jsonschema:"The source's combined hash from node_read, not content_hash or meta_hash."`
	TargetID            string  `json:"target_id" jsonschema:"ID of the node that survives and takes the merged body."`
	Content             *string `json:"content,omitempty" jsonschema:"The target's merged markdown body, replacing its content entirely. Must not start with a YAML frontmatter block. Send content or patch."`
	Patch               *string `json:"patch,omitempty" jsonschema:"Unified diff against the target's current content, an alternative to content when the target already holds most of the merged body."`
	Meta                *string `json:"meta,omitempty" jsonschema:"The target's replacement metadata as YAML, for combining tags and fields; omit to keep it."`
	ExpectedContentHash string  `json:"expected_content_hash" jsonschema:"The target's content_hash from node_read."`
	ExpectedMetaHash    string  `json:"expected_meta_hash,omitempty" jsonschema:"The target's meta_hash from node_read. Required with meta."`
	Schema              string  `json:"schema,omitempty" jsonschema:"Schema for the target, as for node_edit: required on a strict keg when meta is supplied."`
	Keg                 string  `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

// mergeOutput is node_merge's structured content.
type mergeOutput struct {
	Target    nodeUpdateOutput `json:"target" jsonschema:"The surviving node's new hashes."`
	Rewritten []string         `json:"rewritten" jsonschema:"IDs of the other nodes whose links to the source now point at the target."`
}

func registerMerge(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "node_merge",
		Description: "Memory: Merge a duplicate node into another as one atomic write, to deduplicate a keg. " +
			"Call node_read on both first. The target survives: it takes the merged body you write (content, or a patch against its current content) and optionally merged meta. " +
			"Every ../SOURCE link in the keg, the target's own included, is repointed at the target, and the source goes to the trash, recorded as merged into the target, where trash_restore can bring it back. " +
			"Guard the source with its combined hash as expected_source_hash, and the target with its content_hash (and meta_hash when sending meta). " +
			"The result returns the target's new hashes and the other nodes whose links moved.",
		InputSchema:  constrainedInputSchema[mergeInput](),
		OutputSchema: outputSchema[mergeOutput](),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in mergeInput) (*sdkmcp.CallToolResult, any, error) {
		ctx = keg.WithValidationActor(ctx, keg.ValidationActorAgent)
		target := tapper.BatchEditItem{NodeID: in.TargetID, Schema: in.Schema, ExpectedContentHash: in.ExpectedContentHash, ExpectedMetaHash: in.ExpectedMetaHash}
		if in.Content != nil {
			target.Content, target.HasContent = *in.Content, true
		}
		if in.Patch != nil {
			target.ContentPatch, target.HasContentPatch = *in.Patch, true
		}
		if in.Meta != nil {
			target.Meta, target.HasMeta = *in.Meta, true
		}
		result, err := tap.Merge(ctx, tapper.MergeOptions{
			KegTargetOptions:   resolveKegTarget(ctx, in.Keg, defaults),
			SourceID:           in.SourceID,
			ExpectedSourceHash: in.ExpectedSourceHash,
			Target:             target,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		rewritten := make([]string, len(result.Rewritten))
		for i, id := range result.Rewritten {
			rewritten[i] = id.Path()
		}
		res := textResult(fmt.Sprintf("merged %s into %s; %d other node(s) relinked", in.SourceID, result.Target.ID.Path(), len(rewritten)))
		res.StructuredContent = mergeOutput{Target: nodeUpdateOutputs([]keg.NodeUpdateResult{result.Target})[0], Rewritten: rewritten}
		return res, nil, nil
	})
}
