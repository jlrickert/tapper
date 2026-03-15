package keg

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	highlighting "github.com/yuin/goldmark-highlighting/v2"
)

// RenderOptions configures markdown-to-HTML rendering.
type RenderOptions struct {
	// BaseURL is the site base URL prefix for rewritten node links.
	// Defaults to "/".
	BaseURL string
}

// nodeRelRE matches ../N link patterns (with optional trailing slash or anchor).
var nodeRelRE = regexp.MustCompile(`^\.\./\s*(\d+)\s*(.*)$`)

// RenderMarkdown converts raw markdown bytes to HTML, rewriting ../N node
// links to site-relative paths. The returned bytes are the inner HTML content
// (no <html> or <body> wrapper).
func RenderMarkdown(src []byte, opts RenderOptions) ([]byte, error) {
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = "/"
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}

	md := goldmark.New(
		goldmark.WithExtensions(
			extension.Table,
			extension.Strikethrough,
			extension.TaskList,
			extension.Linkify,
			highlighting.NewHighlighting(
				highlighting.WithStyle("github"),
				highlighting.WithFormatOptions(),
			),
		),
		goldmark.WithParserOptions(
			parser.WithASTTransformers(
				util.Prioritized(&nodeLinkTransformer{baseURL: baseURL}, 100),
			),
		),
	)

	var buf bytes.Buffer
	if err := md.Convert(src, &buf); err != nil {
		return nil, fmt.Errorf("render markdown: %w", err)
	}
	return buf.Bytes(), nil
}

// nodeLinkTransformer rewrites ../N link destinations in the AST to
// site-relative paths like /42/ (or {baseURL}42/).
type nodeLinkTransformer struct {
	baseURL string
}

func (t *nodeLinkTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if link, ok := n.(*ast.Link); ok {
			dest := string(link.Destination)
			if m := nodeRelRE.FindStringSubmatch(dest); len(m) >= 2 {
				nodeID := m[1]
				suffix := strings.TrimSpace(m[2])
				newDest := t.baseURL + nodeID + "/"
				if suffix != "" {
					newDest += suffix
				}
				link.Destination = []byte(newDest)
			}
		}
		return ast.WalkContinue, nil
	})
}
