package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerSiteTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerSite(srv, tap, defaults)
}

// --- site ---

type siteInput struct {
	Keg      string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Output   string `json:"output,omitempty" jsonschema:"output directory (default: ./site)"`
	Title    string `json:"title,omitempty" jsonschema:"override site title"`
	BaseURL  string `json:"base_url,omitempty" jsonschema:"base URL for absolute links (default: /)"`
	NoSearch bool   `json:"no_search,omitempty" jsonschema:"skip pagefind search indexing"`
}

func registerSite(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "site",
		Description: "Generate a static HTML website from a KEG",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in siteInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.SiteOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			Output:           in.Output,
			Title:            in.Title,
			BaseURL:          in.BaseURL,
			NoSearch:         in.NoSearch,
		}

		result, err := tap.Site(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}

		summary := fmt.Sprintf("site generated: %d nodes, %d tags -> %s",
			result.NodeCount, result.TagCount, result.OutputDir)
		if result.HasSearch {
			summary += "\nsearch index created with pagefind"
		}
		return textResult(summary), nil, nil
	})
}
