package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerServeTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerServe(srv, tap, defaults)
}

// --- serve ---

type serveInput struct {
	Keg     string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Host    string `json:"host,omitempty" jsonschema:"bind address (default: 127.0.0.1)"`
	Port    int    `json:"port,omitempty" jsonschema:"port to listen on (default: 0 for random)"`
	Title   string `json:"title,omitempty" jsonschema:"override site title"`
	BaseURL string `json:"base_url,omitempty" jsonschema:"base URL for links (default: /)"`
	Watch   *bool  `json:"watch,omitempty" jsonschema:"enable filesystem watcher and browser auto-refresh via SSE (default: true)"`
}

func registerServe(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "serve",
		Description: "Start an HTTP server that renders KEG pages dynamically. Blocks until cancelled.",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in serveInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.ServeOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			Host:             in.Host,
			Port:             in.Port,
			Title:            in.Title,
			BaseURL:          in.BaseURL,
			Watch:            in.Watch,
		}

		result, err := tap.Serve(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}

		summary := fmt.Sprintf("server stopped (was serving at %s)", result.URL)
		return textResult(summary), nil, nil
	})
}
