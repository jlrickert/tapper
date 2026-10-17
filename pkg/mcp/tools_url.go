package mcp

import (
	"context"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// --- url ---

type urlInput struct {
	Ref string `json:"ref,omitempty" jsonschema:"What to link: a node id in keg (42), a KEG (@ns/keg, keg:name, keg:~alias), a note (@ns/keg/42, keg:name/42, keg:~alias/42), a flight (+slug, @ns/+slug), or a namespace (@ns). Empty links keg itself."`
	Keg string `json:"keg,omitempty" jsonschema:"KEG a bare node id or ~alias resolves against, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
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
