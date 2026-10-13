package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// --- url ---

type urlInput struct {
	Ref string `json:"ref,omitempty" jsonschema:"what to link: a node id in keg (42), a KEG (keg:alias, keg:~alias, @ns/keg), a note (keg:alias/42, keg:~alias/42, @ns/keg/42), a flight (+slug, @ns/+slug), or a namespace (@ns). Empty links keg itself."`
	Keg string `json:"keg,omitempty" jsonschema:"keg a bare node id or keg-local ~alias resolves against (uses default if empty)"`
}

func registerURL(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "url",
		Description: "Memory: Get the Hub web link for a KEG, note, flight, or namespace, to give the reader a clickable link. " +
			"Returns url, kind (namespace, keg, node, or flight), and the canonical ref. Building a link never reads the note.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in urlInput) (*sdkmcp.CallToolResult, any, error) {
		link, err := tap.WebURL(ctx, tapper.WebURLOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			Ref:              in.Ref,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		res := textResult(link.URL)
		res.StructuredContent = link
		return res, nil, nil
	})
}
