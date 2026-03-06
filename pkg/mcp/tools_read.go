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
	registerListKegs(srv, tap)
	registerInfo(srv, tap, defaults)
	registerKegInfo(srv, tap, defaults)
	registerStats(srv, tap, defaults)
	registerDir(srv, tap, defaults)
}

// --- cat ---

type catInput struct {
	NodeIDs     []string `json:"node_ids" jsonschema:"node IDs to read"`
	Keg         string   `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	ContentOnly bool     `json:"content_only,omitempty" jsonschema:"return content without frontmatter"`
	MetaOnly    bool     `json:"meta_only,omitempty" jsonschema:"return metadata only"`
	StatsOnly   bool     `json:"stats_only,omitempty" jsonschema:"return stats only"`
	Tag         string   `json:"tag,omitempty" jsonschema:"tag expression to select nodes (alternative to node_ids)"`
}

func registerCat(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "cat",
		Description: "Read the content of one or more KEG nodes",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in catInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.CatOptions{
			NodeIDs:          in.NodeIDs,
			Tag:              in.Tag,
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
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
	Query   string `json:"query,omitempty" jsonschema:"boolean query expression to filter nodes (e.g. 'golang and entity=concept')"`
	Keg     string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Format  string `json:"format,omitempty" jsonschema:"output format (%i=id %d=date %t=title)"`
	IdOnly  bool   `json:"id_only,omitempty" jsonschema:"return node IDs only"`
	Reverse bool   `json:"reverse,omitempty" jsonschema:"reverse output order"`
	Limit   int    `json:"limit,omitempty" jsonschema:"maximum number of results (0=unlimited)"`
}

func registerList(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "list",
		Description: "List KEG nodes, optionally filtered by a query expression",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in listInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.ListOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			Query:            in.Query,
			Format:           in.Format,
			IdOnly:           in.IdOnly,
			Reverse:          in.Reverse,
			Limit:            in.Limit,
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
	Format     string `json:"format,omitempty" jsonschema:"output format (%i=id %d=date %t=title)"`
	IdOnly     bool   `json:"id_only,omitempty" jsonschema:"return node IDs only"`
	Reverse    bool   `json:"reverse,omitempty" jsonschema:"reverse output order"`
	IgnoreCase bool   `json:"ignore_case,omitempty" jsonschema:"case-insensitive matching"`
}

func registerGrep(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "grep",
		Description: "Search KEG node content with a regex pattern",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in grepInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.GrepOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			Query:            in.Query,
			Format:           in.Format,
			IdOnly:           in.IdOnly,
			Reverse:          in.Reverse,
			IgnoreCase:       in.IgnoreCase,
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
	Query   string `json:"query,omitempty" jsonschema:"boolean expression to filter by tags and attributes"`
	Tag     string `json:"tag,omitempty" jsonschema:"single tag name to filter by"`
	Keg     string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Format  string `json:"format,omitempty" jsonschema:"output format (%i=id %d=date %t=title)"`
	IdOnly  bool   `json:"id_only,omitempty" jsonschema:"return node IDs only"`
	Reverse bool   `json:"reverse,omitempty" jsonschema:"reverse output order"`
}

func registerTags(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "tags",
		Description: "List tags or filter nodes by tag expression",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in tagsInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.TagsOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			Query:            in.Query,
			Tag:              in.Tag,
			Format:           in.Format,
			IdOnly:           in.IdOnly,
			Reverse:          in.Reverse,
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
	NodeID  string `json:"node_id" jsonschema:"target node ID to find incoming links for"`
	Keg     string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Format  string `json:"format,omitempty" jsonschema:"output format (%i=id %d=date %t=title)"`
	IdOnly  bool   `json:"id_only,omitempty" jsonschema:"return node IDs only"`
	Reverse bool   `json:"reverse,omitempty" jsonschema:"reverse output order"`
}

func registerBacklinks(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "backlinks",
		Description: "List nodes that link to a given node",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in backlinksInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.BacklinksOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			NodeID:           in.NodeID,
			Format:           in.Format,
			IdOnly:           in.IdOnly,
			Reverse:          in.Reverse,
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
	NodeID  string `json:"node_id" jsonschema:"source node ID to find outgoing links for"`
	Keg     string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Format  string `json:"format,omitempty" jsonschema:"output format (%i=id %d=date %t=title)"`
	IdOnly  bool   `json:"id_only,omitempty" jsonschema:"return node IDs only"`
	Reverse bool   `json:"reverse,omitempty" jsonschema:"reverse output order"`
}

func registerLinks(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "links",
		Description: "List outgoing links from a node",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in linksInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.LinksOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			NodeID:           in.NodeID,
			Format:           in.Format,
			IdOnly:           in.IdOnly,
			Reverse:          in.Reverse,
		}
		lines, err := tap.Links(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return linesResult(lines), nil, nil
	})
}

// --- list_kegs ---

type listKegsInput struct{}

func registerListKegs(srv *sdkmcp.Server, tap *tapper.Tap) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "list_kegs",
		Description: "List available KEG aliases",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in listKegsInput) (*sdkmcp.CallToolResult, any, error) {
		kegs, err := tap.ListKegs(false)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return linesResult(kegs), nil, nil
	})
}

// --- info ---

type infoInput struct {
	Keg string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerInfo(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "info",
		Description: "Show KEG config (keg file contents)",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in infoInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.InfoOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
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
		Description: "Show diagnostics for a resolved KEG (alias, path, node count)",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in kegInfoInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.KegInfoOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
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
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in statsInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.StatsOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			NodeID:           in.NodeID,
		}
		result, err := tap.Stats(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(result), nil, nil
	})
}

// --- dir ---

type dirInput struct {
	NodeID string `json:"node_id,omitempty" jsonschema:"node ID (omit for keg root directory)"`
	Keg    string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerDir(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "dir",
		Description: "Show the filesystem path of a keg or node directory",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in dirInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.DirOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			NodeID:           in.NodeID,
		}
		result, err := tap.Dir(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(result), nil, nil
	})
}
