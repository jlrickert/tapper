package keg

import (
	"bytes"
	"fmt"
	"net/url"
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
	// BaseURL is the keg-root URL prefix for rewritten node links; node N
	// lives at {BaseURL}N. Defaults to "/".
	BaseURL string

	// NodeID identifies the node being rendered. When set, every relative
	// destination is resolved as if the page were {BaseURL}{NodeID}/README.md,
	// which covers ./images/X, ./assets/X, and ../N/README.md in addition to
	// ../N. When empty, only the legacy ../N shape is rewritten and other
	// relative links pass through unchanged.
	NodeID string

	// NoTrailingSlash emits node hrefs as {BaseURL}N instead of {BaseURL}N/.
	NoTrailingSlash bool

	// KegResolver maps keg:-scheme references to hrefs. namespace is "" for
	// the bare-alias form keg:ALIAS/N. Returning "" leaves the link
	// untouched, as does a nil resolver.
	KegResolver func(namespace, alias, nodeID string) string
}

// nodeRelRE matches ../N link patterns (with optional trailing slash or anchor).
var nodeRelRE = regexp.MustCompile(`^\.\./\s*(\d+)\s*(.*)$`)

// kegSchemeRE matches keg:-scheme references: keg:ALIAS/N and keg:@NS/ALIAS/N,
// with an optional fragment or query suffix.
var kegSchemeRE = regexp.MustCompile(`^keg:(?:@([A-Za-z0-9][A-Za-z0-9_-]*)/)?([A-Za-z0-9][A-Za-z0-9_-]*)/([0-9]+)((?:#|\?).*)?$`)

// allDigitsRE matches a string of one or more decimal digits.
var allDigitsRE = regexp.MustCompile(`^[0-9]+$`)

// RenderMarkdown converts raw markdown bytes to HTML, rewriting node-relative
// link and image destinations to site paths per opts. The returned bytes are
// the inner HTML content (no <html> or <body> wrapper).
func RenderMarkdown(src []byte, opts RenderOptions) ([]byte, error) {
	if opts.BaseURL == "" {
		opts.BaseURL = "/"
	}
	if !strings.HasSuffix(opts.BaseURL, "/") {
		opts.BaseURL += "/"
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
				util.Prioritized(&nodeLinkTransformer{opts: opts}, 100),
			),
		),
	)

	var buf bytes.Buffer
	if err := md.Convert(src, &buf); err != nil {
		return nil, fmt.Errorf("render markdown: %w", err)
	}
	return buf.Bytes(), nil
}

// ResolveNodeLink resolves one raw markdown destination against opts. It
// returns the rewritten destination and true, or ("", false) when the
// destination should be left unchanged (absolute URLs, fragments, unparseable
// input, keg: links with no resolver, ...).
func ResolveNodeLink(dest string, opts RenderOptions) (string, bool) {
	dest = strings.TrimSpace(dest)
	if dest == "" || strings.HasPrefix(dest, "#") ||
		strings.HasPrefix(dest, "/") {
		return "", false
	}

	if m := kegSchemeRE.FindStringSubmatch(dest); m != nil {
		if opts.KegResolver == nil {
			return "", false
		}
		resolved := opts.KegResolver(m[1], m[2], m[3])
		if resolved == "" {
			return "", false
		}
		return resolved + m[4], true
	}

	if u, err := url.Parse(dest); err != nil || u.IsAbs() || u.Host != "" {
		// Other schemes (http, https, mailto, ...) and protocol-relative
		// URLs pass through, as does anything net/url rejects.
		return "", false
	}

	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = "/"
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}

	if opts.NodeID == "" {
		// Legacy fast path: only the ../N shape can be resolved without
		// knowing which node the content belongs to.
		m := nodeRelRE.FindStringSubmatch(dest)
		if m == nil {
			return "", false
		}
		newDest := baseURL + m[1]
		if !opts.NoTrailingSlash {
			newDest += "/"
		}
		return newDest + strings.TrimSpace(m[2]), true
	}

	// Resolve relative to the node's content file, mirroring how the link
	// resolves on disk where the page is <keg>/<NodeID>/README.md.
	base, err := url.Parse(baseURL + opts.NodeID + "/README.md")
	if err != nil {
		return "", false
	}
	rel, err := url.Parse(dest)
	if err != nil {
		return "", false
	}
	resolved := base.ResolveReference(rel)

	path := strings.TrimSuffix(resolved.Path, "/README.md")
	if trimmed := strings.TrimSuffix(path, "/"); allDigitsRE.MatchString(trimmed[strings.LastIndex(trimmed, "/")+1:]) {
		// Node URL: normalize the trailing slash per options.
		path = trimmed
		if !opts.NoTrailingSlash {
			path += "/"
		}
	}
	resolved.Path = path
	return resolved.String(), true
}

// nodeLinkTransformer rewrites node-relative link and image destinations in
// the AST to site paths via ResolveNodeLink.
type nodeLinkTransformer struct {
	opts RenderOptions
}

func (t *nodeLinkTransformer) Transform(doc *ast.Document, reader text.Reader, pc parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var dest *[]byte
		switch v := n.(type) {
		case *ast.Link:
			dest = &v.Destination
		case *ast.Image:
			dest = &v.Destination
		default:
			return ast.WalkContinue, nil
		}
		if newDest, ok := ResolveNodeLink(string(*dest), t.opts); ok {
			*dest = []byte(newDest)
		}
		return ast.WalkContinue, nil
	})
}
