package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// --- keg_replace ---

type replaceInput struct {
	Find        string `json:"find" jsonschema:"Text to find; with regex, a Go RE2 regular expression (unlike node_grep, which uses PostgreSQL syntax)."`
	Replace     string `json:"replace" jsonschema:"Replacement text; with regex, $1 and ${name} insert captured groups."`
	Regex       bool   `json:"regex,omitempty" jsonschema:"Treat find as a regular expression instead of literal text."`
	Query       string `json:"query,omitempty" jsonschema:"Limit the replace to the nodes this node_list query selects, such as a tag; empty means every node."`
	IncludeMeta bool   `json:"include_meta,omitempty" jsonschema:"Replace in each node's metadata YAML as well as its body."`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"Report what would change without writing."`
	Keg         string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

type replaceNodeOutput struct {
	NodeID      string `json:"node_id"`
	Matches     int    `json:"matches"`
	Error       string `json:"error,omitempty"`
	ContentHash string `json:"content_hash,omitempty"`
	MetaHash    string `json:"meta_hash,omitempty"`
	Hash        string `json:"hash,omitempty"`
}

func registerReplace(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "keg_replace",
		Description: "Manage: Find and replace text across a keg's nodes in one atomic write, for renames and consistent wording. " +
			"Call it with dry_run first: it lists every node that would change and how many matches each has, and writes nothing. " +
			"find is literal unless regex is set; query narrows the nodes (node_list syntax); include_meta also rewrites metadata. " +
			"A node whose replacement would break any write rule (the keg's schemas, metadata that must parse, a body that may not open with frontmatter) is reported with its error (and no bodies) and left alone while the rest are written; dry_run reports the same errors. " +
			fmt.Sprintf("At most %d matching nodes in one call, counting those left alone. ", keg.MaxReplaceNodes) +
			fmt.Sprintf("A node with more than %d matches, or that would grow past %d MiB, is left alone with its error; a replace that would grow the changed nodes past %d MiB in all fails. ", keg.MaxReplaceNodeMatches, keg.MaxReplaceNodeBytes>>20, keg.MaxReplaceTotalBytes>>20) +
			"Results return each changed node's new hashes.",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in replaceInput) (*sdkmcp.CallToolResult, any, error) {
		ctx = keg.WithValidationActor(ctx, keg.ValidationActorAgent)
		result, err := tap.Replace(ctx, tapper.ReplaceOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			ReplaceTextOptions: keg.ReplaceTextOptions{
				Find: in.Find, Replace: in.Replace, Regex: in.Regex, Query: in.Query, IncludeMeta: in.IncludeMeta, DryRun: in.DryRun,
			},
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		nodes := make([]replaceNodeOutput, len(result.Nodes))
		lines := make([]string, 0, len(result.Nodes)+1)
		changed, matches := 0, 0
		for i, node := range result.Nodes {
			nodes[i] = replaceNodeOutput{NodeID: node.ID.Path(), Matches: node.Matches, Error: node.Error, ContentHash: node.ContentHash, MetaHash: node.MetaHash, Hash: node.Hash}
			line := fmt.Sprintf("%s  %d match(es)", node.ID.Path(), node.Matches)
			if node.Error != "" {
				line += "  skipped: " + node.Error
			} else {
				changed++
				matches += node.Matches
			}
			lines = append(lines, line)
		}
		verb := "changed"
		if in.DryRun {
			verb = "would change"
		}
		lines = append([]string{fmt.Sprintf("%s %d node(s), %d match(es)", verb, changed, matches)}, lines...)
		res := linesResult(lines)
		res.StructuredContent = map[string]any{"dry_run": in.DryRun, "nodes": nodes}
		return res, nil, nil
	})
}
