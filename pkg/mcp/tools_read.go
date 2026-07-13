package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

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
	registerKegInfo(srv, tap, defaults)
	registerStats(srv, tap, defaults)
}

// --- cat ---

type catInput struct {
	NodeIDs     []string `json:"node_ids" jsonschema:"node IDs to read"`
	Keg         string   `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	ContentOnly bool     `json:"content_only,omitempty" jsonschema:"return content without frontmatter"`
	MetaOnly    bool     `json:"meta_only,omitempty" jsonschema:"return metadata only"`
	StatsOnly   bool     `json:"stats_only,omitempty" jsonschema:"return stats only"`
	Query       string   `json:"query,omitempty" jsonschema:"boolean expression to select nodes (alternative to node_ids)"`
}

func registerCat(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "cat",
		Description: "Read the content of one or more KEG nodes",
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
		result, err := tap.Cat(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(result), nil, nil
	})
}

// --- list ---

type listInput struct {
	Query   string `json:"query,omitempty" jsonschema:"boolean query expression to filter nodes. Supports tags ('golang'), key=value attributes ('entity=plan'), and dot-prefix stats fields ('.created>2026-01-01', '.accessCount>=5', '.hash=abc123'). Combine with 'and', 'or', 'not'."`
	Keg     string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Format  string `json:"format,omitempty" jsonschema:"output format (%i=id %d=date %t=title)"`
	IdOnly  bool   `json:"id_only,omitempty" jsonschema:"return node IDs only"`
	Reverse bool   `json:"reverse,omitempty" jsonschema:"reverse output order"`
	Sort    string `json:"sort,omitempty" jsonschema:"sort order: 'id' (default), 'updated', 'created', or 'accessed'"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum results to return (default: 50, 0 in request means use default, -1 for unlimited)"`
	Offset  int    `json:"offset,omitempty" jsonschema:"skip the first N results (for pagination)"`
}

func registerList(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "list",
		Description: "List KEG nodes, optionally filtered by a query expression",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in listInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.ListOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			Query:            in.Query,
			Format:           in.Format,
			IdOnly:           in.IdOnly,
			Reverse:          in.Reverse,
			Sort:             tapper.ListSortType(in.Sort),
			Limit:            mcpDefaultLimit(in.Limit),
			Offset:           in.Offset,
		}
		lines, err := tap.List(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return linesResult(lines), nil, nil
	})
}

// --- grep ---

type grepInput struct {
	Query      string `json:"query" jsonschema:"regex pattern to search node content"`
	Keg        string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Format     string `json:"format,omitempty" jsonschema:"output format (%i=id %d=date %t=title); use id_only for compact MCP output"`
	IdOnly     bool   `json:"id_only,omitempty" jsonschema:"return node IDs only (recommended for MCP to reduce token usage)"`
	Reverse    bool   `json:"reverse,omitempty" jsonschema:"reverse output order"`
	IgnoreCase bool   `json:"ignore_case,omitempty" jsonschema:"case-insensitive matching"`
	MaxLines   int    `json:"max_lines,omitempty" jsonschema:"max matched lines per node (default: unlimited for CLI, 3 for MCP)"`
	Limit      int    `json:"limit,omitempty" jsonschema:"maximum results to return (default: 50, 0 in request means use default, -1 for unlimited)"`
	Offset     int    `json:"offset,omitempty" jsonschema:"skip the first N results (for pagination)"`
}

func registerGrep(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "grep",
		Description: "Search KEG node content with a regex pattern",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in grepInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.GrepOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			Query:            in.Query,
			Format:           in.Format,
			IdOnly:           in.IdOnly,
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
		return linesResult(lines), nil, nil
	})
}

// --- tags ---

type tagsInput struct {
	Query   string `json:"query,omitempty" jsonschema:"boolean expression to filter by tags, attributes, and dot-prefix stats fields (e.g. '.created>2026-01-01 and entity=plan')"`
	Keg     string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Format  string `json:"format,omitempty" jsonschema:"output format (%i=id %d=date %t=title)"`
	IdOnly  bool   `json:"id_only,omitempty" jsonschema:"return node IDs only"`
	Reverse bool   `json:"reverse,omitempty" jsonschema:"reverse output order"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum results to return (default: 50, 0 in request means use default, -1 for unlimited)"`
	Offset  int    `json:"offset,omitempty" jsonschema:"skip the first N results (for pagination)"`
}

func registerTags(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "tags",
		Description: "List tags or filter nodes by tag expression",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in tagsInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.TagsOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			Query:            in.Query,
			Format:           in.Format,
			IdOnly:           in.IdOnly,
			Reverse:          in.Reverse,
			Limit:            mcpDefaultLimit(in.Limit),
			Offset:           in.Offset,
		}
		lines, err := tap.Tags(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return linesResult(lines), nil, nil
	})
}

// --- backlinks ---

type backlinksInput struct {
	NodeIDs []string `json:"node_ids" jsonschema:"target node IDs to find incoming links for (results merged and deduplicated)"`
	Keg     string   `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Format  string   `json:"format,omitempty" jsonschema:"output format (%i=id %d=date %t=title)"`
	IdOnly  bool     `json:"id_only,omitempty" jsonschema:"return node IDs only"`
	Reverse bool     `json:"reverse,omitempty" jsonschema:"reverse output order"`
	Limit   int      `json:"limit,omitempty" jsonschema:"maximum results to return (default: 50, 0 in request means use default, -1 for unlimited)"`
	Offset  int      `json:"offset,omitempty" jsonschema:"skip the first N results (for pagination)"`
}

func registerBacklinks(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "backlinks",
		Description: "List nodes that link to a given node",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in backlinksInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.BacklinksOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeIDs:          in.NodeIDs,
			Format:           in.Format,
			IdOnly:           in.IdOnly,
			Reverse:          in.Reverse,
			Limit:            mcpDefaultLimit(in.Limit),
			Offset:           in.Offset,
		}
		lines, err := tap.Backlinks(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return linesResult(lines), nil, nil
	})
}

// --- links ---

type linksInput struct {
	NodeIDs []string `json:"node_ids" jsonschema:"source node IDs to find outgoing links for (results merged and deduplicated)"`
	Keg     string   `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Format  string   `json:"format,omitempty" jsonschema:"output format (%i=id %d=date %t=title)"`
	IdOnly  bool     `json:"id_only,omitempty" jsonschema:"return node IDs only"`
	Reverse bool     `json:"reverse,omitempty" jsonschema:"reverse output order"`
	Limit   int      `json:"limit,omitempty" jsonschema:"maximum results to return (default: 50, 0 in request means use default, -1 for unlimited)"`
	Offset  int      `json:"offset,omitempty" jsonschema:"skip the first N results (for pagination)"`
}

func registerLinks(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "links",
		Description: "List outgoing links from a node",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in linksInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.LinksOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeIDs:          in.NodeIDs,
			Format:           in.Format,
			IdOnly:           in.IdOnly,
			Reverse:          in.Reverse,
			Limit:            mcpDefaultLimit(in.Limit),
			Offset:           in.Offset,
		}
		lines, err := tap.Links(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return linesResult(lines), nil, nil
	})
}

// --- info ---

type infoInput struct {
	Keg     string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Minimal *bool  `json:"minimal,omitempty" jsonschema:"return only core config fields (default true)"`
}

func registerInfo(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "info",
		Description: "Show KEG config (keg file contents). Returns minimal output by default; set minimal=false for full config.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in infoInput) (*sdkmcp.CallToolResult, any, error) {
		minimal := true
		if in.Minimal != nil {
			minimal = *in.Minimal
		}
		opts := tapper.InfoOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			Minimal:          minimal,
		}
		result, err := tap.Info(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(result), nil, nil
	})
}

// --- keg_info ---

type kegInfoInput struct {
	Keg string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerKegInfo(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "keg_info",
		Description: "Show concise path-free diagnostics for a resolved KEG (canonical ref, flight, summary, node count, and capabilities)",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in kegInfoInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.KegInfoOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
		}
		result, err := tap.KegInfo(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(result), nil, nil
	})
}

// --- stats ---

type statsInput struct {
	NodeID string `json:"node_id" jsonschema:"node ID to inspect"`
	Keg    string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerStats(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "stats",
		Description: "Show stats (hash, timestamps, access count) for a node",
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
