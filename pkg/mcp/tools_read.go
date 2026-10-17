package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerReadTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerCat(srv, tap, defaults)
	registerList(srv, tap, defaults)
	registerGrep(srv, tap, defaults)
	registerTags(srv, tap, defaults)
	registerBacklinks(srv, tap, defaults)
	registerLinks(srv, tap, defaults)
	registerInfo(srv, tap, defaults)
	registerKegSettings(srv, tap, defaults)
	registerStats(srv, tap, defaults)
	registerURL(srv, tap, defaults)
}

// --- node_read ---

type catInput struct {
	NodeIDs     []string `json:"node_ids,omitempty" jsonschema:"Node IDs to read."`
	Keg         string   `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
	ContentOnly bool     `json:"content_only,omitempty" jsonschema:"Return content only, without metadata."`
	MetaOnly    bool     `json:"meta_only,omitempty" jsonschema:"Return metadata only."`
	StatsOnly   bool     `json:"stats_only,omitempty" jsonschema:"Return stats only."`
	Query       string   `json:"query,omitempty" jsonschema:"Query expression selecting nodes, instead of node_ids."`
}

// nodeReadOutput is one self-contained read result: the node's document
// alongside the precondition token a write must echo back. Splitting those
// across two response surfaces — the hash here, the document only in the
// rendered text — made a read-modify-write cycle require parsing output meant
// for humans, and made multi-node reads correlate rows by position.
//
// Content and Meta are populated to match the read mode and are exactly the
// fields `node_edit` accepts, so a row can be modified and sent straight back.
//
// ContentHash and MetaHash are the per-part tokens node_edit takes, present
// only for the parts the read mode returned. Hash is the combined token
// node_delete and node_move take.
type nodeReadOutput struct {
	NodeID      string `json:"node_id" jsonschema:"The node's ID."`
	Hash        string `json:"hash" jsonschema:"The combined hash node_delete and node_move take as expected_hash."`
	ContentHash string `json:"content_hash,omitempty" jsonschema:"Present when content was read; node_edit's expected_content_hash."`
	MetaHash    string `json:"meta_hash,omitempty" jsonschema:"Present when metadata was read; node_edit's expected_meta_hash."`
	Content     string `json:"content,omitempty" jsonschema:"The markdown body, unless meta_only or stats_only."`
	Meta        string `json:"meta,omitempty" jsonschema:"The metadata document as YAML, unless content_only or stats_only."`
	Stats       string `json:"stats,omitempty" jsonschema:"The node's stats, with stats_only."`
}

// nodeReadResult is node_read's structured content.
type nodeReadResult struct {
	Nodes []nodeReadOutput `json:"nodes" jsonschema:"One entry per node read, in request order."`
}

func nodeReadOutputs(ctx context.Context, views []keg.NodeView, opts tapper.CatOptions) []nodeReadOutput {
	out := make([]nodeReadOutput, 0, len(views))
	for _, view := range views {
		content, meta, stats := tapper.CatViewDocument(ctx, view, opts)
		row := nodeReadOutput{
			NodeID: view.ID.Path(), Hash: view.Hash(),
			Content: content, Meta: meta, Stats: stats,
		}
		if !opts.MetaOnly && !opts.StatsOnly {
			row.ContentHash = view.ContentHash()
		}
		if !opts.ContentOnly && !opts.StatsOnly {
			row.MetaHash = view.MetaHash()
		}
		out = append(out, row)
	}
	return out
}

// nodeReadSummary is node_read's text message when structured rows carry the
// documents: repeating each body in the text would send it twice.
func nodeReadSummary(count int) string {
	if count == 1 {
		return "Read 1 node; its document is in nodes[]"
	}
	return fmt.Sprintf("Read %d nodes; each document is in nodes[]", count)
}

func registerCat(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "node_read",
		Description: "Memory: Read KEG nodes using node_ids or query; they are mutually exclusive, and omitting both returns no nodes. The default returns metadata and content together; " +
			"meta_only returns just the metadata document, which is how you read metadata before " +
			"editing it. Each result carries content_hash (when content was read) and meta_hash (when " +
			"metadata was read): pass content_hash as expected_content_hash and meta_hash as " +
			"expected_meta_hash to node_edit. Each part's hash is independent, so editing one part " +
			"never invalidates the other's. The combined hash is for node_delete and node_move.",
		OutputSchema: outputSchema[nodeReadResult](),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in catInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.CatOptions{
			NodeIDs:          in.NodeIDs,
			Query:            in.Query,
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			ContentOnly:      in.ContentOnly,
			MetaOnly:         in.MetaOnly,
			StatsOnly:        in.StatsOnly,
		}
		// Read once and render from the same views: calling Cat as well would
		// re-read every node and double its access touch.
		views, err := tap.CatViews(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		if len(views) == 0 {
			res := textResult(tapper.FormatCatViews(ctx, views, opts))
			res.StructuredContent = nodeReadResult{Nodes: nodeReadOutputs(ctx, views, opts)}
			return res, nil, nil
		}
		res := textResult(nodeReadSummary(len(views)))
		res.StructuredContent = nodeReadResult{Nodes: nodeReadOutputs(ctx, views, opts)}
		return res, nil, nil
	})
}

// --- list ---

type listInput struct {
	Query   string `json:"query,omitempty" jsonschema:"Query expression selecting nodes: tags (golang), key=value attributes (entity=plan), and stats fields with a leading dot (.created>2026-01-01, .accessCount>=5), combined with and, or, and not."`
	Keg     string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
	Format  string `json:"format,omitempty" jsonschema:"Output template per node. Verbs: %i id, %t title, %d updated, %c created, %a accessed, %% a literal percent. Selectors: %{key} a metadata key such as %{type}, %{.field} a stats field such as %{.accessCount}, %{tags} the tag list. Escapes: \\t, \\n, \\r, \\\\."`
	Reverse bool   `json:"reverse,omitempty" jsonschema:"Reverse the order of each page."`
	Sort    string `json:"sort,omitempty" jsonschema:"Sort order: id (default), updated, created, or accessed."`
	Limit   int    `json:"limit,omitempty" jsonschema:"Maximum results; 0 or omitted means 50, and -1 means no limit."`
	Offset  int    `json:"offset,omitempty" jsonschema:"Results to skip; pass the previous page's next_offset to page."`
}

func registerList(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "node_list",
		Description:  "Memory: List a KEG's nodes, optionally filtered by a query expression, one line per node rendered by format. Follow next_offset for more.",
		OutputSchema: outputSchema[pagedLinesOutput](),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in listInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.ListOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			Query:            in.Query,
			Format:           in.Format,
			Reverse:          in.Reverse,
			Sort:             tapper.ListSortType(in.Sort),
			Limit:            mcpDefaultLimit(in.Limit),
			Offset:           in.Offset,
		}
		lines, err := tap.List(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return pagedLinesResult(lines, in.Offset, mcpDefaultLimit(in.Limit)), nil, nil
	})
}

// --- node_grep ---

type grepInput struct {
	Query      string `json:"query" jsonschema:"PostgreSQL regular expression matched against each line of node content. \\y (not \\b) is a word boundary; lookahead is allowed."`
	Keg        string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
	Format     string `json:"format,omitempty" jsonschema:"Output template per node. Verbs: %i id, %t title, %d updated, %c created, %a accessed, %% a literal percent. Selectors: %{key} a metadata key such as %{type}, %{.field} a stats field such as %{.accessCount}, %{tags} the tag list. Escapes: \\t, \\n, \\r, \\\\."`
	Reverse    bool   `json:"reverse,omitempty" jsonschema:"Reverse the order of each page."`
	IgnoreCase bool   `json:"ignore_case,omitempty" jsonschema:"Match case-insensitively."`
	MaxLines   int    `json:"max_lines,omitempty" jsonschema:"Matched lines to show per node; 0 or omitted means 3, and -1 means all."`
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum results; 0 or omitted means 50, and -1 means no limit."`
	Offset     int    `json:"offset,omitempty" jsonschema:"Results to skip; pass the previous page's next_offset to page."`
}

func registerGrep(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "node_grep",
		Description:  "Memory: Match node content lines in one KEG against a PostgreSQL regular expression, in node ID order. Use it for exact identifiers, symbols, and audits; use node_search to find the nodes about a topic. Follow next_offset for more nodes, and use node_read for complete bodies.",
		OutputSchema: outputSchema[pagedLinesOutput](),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in grepInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.GrepOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			Query:            in.Query,
			Format:           in.Format,
			Reverse:          in.Reverse,
			IgnoreCase:       in.IgnoreCase,
			MaxLines:         mcpDefaultMaxLines(in.MaxLines),
			Limit:            mcpDefaultLimit(in.Limit),
			Offset:           in.Offset,
		}
		lines, err := tap.Grep(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return pagedLinesResult(lines, in.Offset, mcpDefaultLimit(in.Limit)), nil, nil
	})
}

// --- tags ---

type tagsInput struct {
	Query   string `json:"query,omitempty" jsonschema:"Query expression selecting nodes, such as .created>2026-01-01 and entity=plan. Omit it to list the KEG's tags."`
	Keg     string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
	Format  string `json:"format,omitempty" jsonschema:"Output template per node. Verbs: %i id, %t title, %d updated, %c created, %a accessed, %% a literal percent. Selectors: %{key} a metadata key such as %{type}, %{.field} a stats field such as %{.accessCount}, %{tags} the tag list. Escapes: \\t, \\n, \\r, \\\\."`
	Reverse bool   `json:"reverse,omitempty" jsonschema:"Reverse the order of each page."`
	Limit   int    `json:"limit,omitempty" jsonschema:"Maximum results; 0 or omitted means 50, and -1 means no limit."`
	Offset  int    `json:"offset,omitempty" jsonschema:"Results to skip; pass the previous page's next_offset to page."`
}

func registerTags(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "tag_list",
		Description:  "Memory: List a KEG's tags, or the nodes a query expression selects.",
		OutputSchema: outputSchema[pagedLinesOutput](),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in tagsInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.TagsOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			Query:            in.Query,
			Format:           in.Format,
			Reverse:          in.Reverse,
			Limit:            mcpDefaultLimit(in.Limit),
			Offset:           in.Offset,
		}
		lines, err := tap.Tags(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return pagedLinesResult(lines, in.Offset, mcpDefaultLimit(in.Limit)), nil, nil
	})
}

// --- backlinks ---

type backlinksInput struct {
	NodeIDs []string `json:"node_ids" jsonschema:"Target node IDs to find incoming links for (results merged and deduplicated)."`
	Keg     string   `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
	Format  string   `json:"format,omitempty" jsonschema:"Output template per node. Verbs: %i id, %t title, %d updated, %c created, %a accessed, %% a literal percent. Selectors: %{key} a metadata key such as %{type}, %{.field} a stats field such as %{.accessCount}, %{tags} the tag list. Escapes: \\t, \\n, \\r, \\\\."`
	Reverse bool     `json:"reverse,omitempty" jsonschema:"Reverse the order of each page."`
	Limit   int      `json:"limit,omitempty" jsonschema:"Maximum results; 0 or omitted means 50, and -1 means no limit."`
	Offset  int      `json:"offset,omitempty" jsonschema:"Results to skip; pass the previous page's next_offset to page."`
}

func registerBacklinks(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "node_backlinks",
		Description:  "Memory: List the nodes that link to the given nodes.",
		OutputSchema: outputSchema[pagedLinesOutput](),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in backlinksInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.BacklinksOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeIDs:          in.NodeIDs,
			Format:           in.Format,
			Reverse:          in.Reverse,
			Limit:            mcpDefaultLimit(in.Limit),
			Offset:           in.Offset,
		}
		lines, err := tap.Backlinks(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return pagedLinesResult(lines, in.Offset, mcpDefaultLimit(in.Limit)), nil, nil
	})
}

// --- links ---

type linksInput struct {
	NodeIDs []string `json:"node_ids" jsonschema:"Source node IDs to find outgoing links for (results merged and deduplicated)."`
	Keg     string   `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
	Format  string   `json:"format,omitempty" jsonschema:"Output template per node. Verbs: %i id, %t title, %d updated, %c created, %a accessed, %% a literal percent. Selectors: %{key} a metadata key such as %{type}, %{.field} a stats field such as %{.accessCount}, %{tags} the tag list. Escapes: \\t, \\n, \\r, \\\\."`
	Reverse bool     `json:"reverse,omitempty" jsonschema:"Reverse the order of each page."`
	Limit   int      `json:"limit,omitempty" jsonschema:"Maximum results; 0 or omitted means 50, and -1 means no limit."`
	Offset  int      `json:"offset,omitempty" jsonschema:"Results to skip; pass the previous page's next_offset to page."`
}

func registerLinks(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "node_links",
		Description:  "Memory: List the nodes the given nodes link to.",
		OutputSchema: outputSchema[pagedLinesOutput](),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in linksInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.LinksOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeIDs:          in.NodeIDs,
			Format:           in.Format,
			Reverse:          in.Reverse,
			Limit:            mcpDefaultLimit(in.Limit),
			Offset:           in.Offset,
		}
		lines, err := tap.Links(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return pagedLinesResult(lines, in.Offset, mcpDefaultLimit(in.Limit)), nil, nil
	})
}

// --- keg_settings_read ---

type kegSettingsInput struct {
	Keg     string   `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
	Kegs    []string `json:"kegs,omitempty" jsonschema:"1-100 @namespace/keg references to read together; minimal mode only. Not combined with keg."`
	Minimal *bool    `json:"minimal,omitempty" jsonschema:"Return only the core settings fields (default true)."`
}

func registerKegSettings(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "keg_settings_read",
		Description:  "Memory: Read targeted KEG settings and instructions. Defaults to a minimal summary; set minimal=false for one complete YAML data document and its hash, required by keg_settings_edit. Use keg or 1-100 kegs, never both.",
		OutputSchema: outputSchema[kegSettingsReadOutput](),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in kegSettingsInput) (*sdkmcp.CallToolResult, any, error) {
		minimal := true
		if in.Minimal != nil {
			minimal = *in.Minimal
		}
		if in.Keg != "" && in.Kegs != nil {
			return errorResult(fmt.Errorf("keg and kegs are mutually exclusive")), nil, nil
		}
		if in.Kegs != nil {
			if len(in.Kegs) == 0 || len(in.Kegs) > 100 {
				return errorResult(fmt.Errorf("kegs must contain 1 to 100 canonical references")), nil, nil
			}
			if !minimal && len(in.Kegs) != 1 {
				return errorResult(fmt.Errorf("minimal=false requires exactly one keg")), nil, nil
			}
		}
		target := in.Keg
		if !minimal && len(in.Kegs) == 1 {
			target = in.Kegs[0]
		}
		opts := tapper.KegSettingsOptions{
			KegTargetOptions: resolveKegTarget(ctx, target, defaults),
			Minimal:          minimal,
		}
		if minimal {
			opts.Kegs = in.Kegs
		}
		result, err := tap.KegSettings(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		res := textResult(result)
		if !minimal {
			// The document travels once, in data; the text is only a pointer.
			res = textResult("Settings are in data.")
			// Only the full read returns the stored document verbatim (raw
			// file bytes, or cfg.String() for a remote keg) — the same source
			// keg_settings_edit replaces. The minimal render is a cross-keg
			// summary, so hashing it would hand back a token for something
			// nobody can write.
			res.StructuredContent = kegSettingsReadOutput{Hash: keg.DocumentHash([]byte(result)), Data: result}
		}
		return res, nil, nil
	})
}

// kegSettingsReadOutput is keg_settings_read's structured content. The
// minimal summary is text only; minimal=false adds the document and its hash.
type kegSettingsReadOutput struct {
	Hash string `json:"hash,omitempty" jsonschema:"With minimal=false, the settings document's hash: keg_settings_edit's expected_hash."`
	Data string `json:"data,omitempty" jsonschema:"With minimal=false, the complete settings document as YAML."`
}

// --- info ---

type infoInput struct {
	Keg string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerInfo(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "keg_info",
		Description:  "Memory: Show concise diagnostics for a KEG: canonical reference, flight, description, node count, and capabilities.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in infoInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.InfoOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
		}
		result, err := tap.Info(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(result), nil, nil
	})
}

// --- stats ---

type statsInput struct {
	NodeID string `json:"node_id" jsonschema:"Node ID to inspect."`
	Keg    string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerStats(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "node_stats",
		Description:  "Memory: Show a node's stats: hash, timestamps, and access count.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in statsInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.StatsOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
		}
		result, err := tap.Stats(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(result), nil, nil
	})
}
