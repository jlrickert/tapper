package keg

import (
	"bytes"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	highlighting "github.com/yuin/goldmark-highlighting/v2"
)

// RenderOptions configures markdown-to-HTML rendering.
type RenderOptions struct {
	// Aliases supplies the source KEG's settings aliases, for keg:~alias/N.
	Aliases []AliasEntry
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
var kegSchemeRE = regexp.MustCompile(`^keg:(?:@([A-Za-z0-9][A-Za-z0-9_-]*)/)?(~?[A-Za-z0-9][A-Za-z0-9_-]*)/([0-9]+)((?:#|\?).*)?$`)

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
		goldmark.WithRendererOptions(
			renderer.WithNodeRenderers(util.Prioritized(videoRenderer{}, 100)),
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
		namespace, alias := m[1], m[2]
		if strings.HasPrefix(alias, "~") {
			if namespace != "" {
				return "", false
			}
			var err error
			namespace, alias, err = ResolveSettingsAlias(opts.Aliases, strings.TrimPrefix(alias, "~"))
			if err != nil {
				return "", false
			}
		}
		resolved := opts.KegResolver(namespace, alias, m[3])
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

	// Resolve relative to the node's logical content path. This preserves the
	// established ../NODEID link semantics without requiring local storage.
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
	var videos []*ast.Image
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
			if img, ok := n.(*ast.Image); ok && isVideoPath(newDest) {
				videos = append(videos, img)
			}
		}
		return ast.WalkContinue, nil
	})
	// An image whose destination is one of a node's videos embeds a player.
	// Replaced after the walk, which must not see its tree change.
	for _, img := range videos {
		video := &videoNode{src: string(img.Destination), caption: string(img.Text(reader.Source()))} //nolint:staticcheck // the alt text, as goldmark renders it
		img.Parent().ReplaceChild(img.Parent(), img, video)
	}
}

// isVideoPath reports whether a resolved destination names a file in a
// node's videos directory.
func isVideoPath(dest string) bool {
	u, err := url.Parse(dest)
	return err == nil && path.Base(path.Dir(u.Path)) == "videos"
}

// videoKind is the kind of a video embedded with image syntax.
var videoKind = ast.NewNodeKind("Video")

// videoNode is a video embedded with image syntax: ![caption](./videos/NAME).
type videoNode struct {
	ast.BaseInline
	src     string
	caption string
}

func (n *videoNode) Kind() ast.NodeKind { return videoKind }

func (n *videoNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"Src": n.src, "Caption": n.caption}, nil)
}

// videoRenderer renders a videoNode as a player that falls back to a link
// to the file.
type videoRenderer struct{}

func (videoRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(videoKind, func(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkSkipChildren, nil
		}
		v := n.(*videoNode)
		src := util.EscapeHTML(util.URLEscape([]byte(v.src), true))
		label := v.caption
		if label == "" {
			label = path.Base(v.src)
			if name, err := url.PathUnescape(label); err == nil {
				label = name
			}
		}
		_, _ = w.WriteString(`<video controls preload="metadata" src="`)
		_, _ = w.Write(src)
		if v.caption != "" {
			_, _ = w.WriteString(`" title="`)
			_, _ = w.Write(util.EscapeHTML([]byte(v.caption)))
		}
		_, _ = w.WriteString(`"><a href="`)
		_, _ = w.Write(src)
		_, _ = w.WriteString(`">`)
		_, _ = w.Write(util.EscapeHTML([]byte(label)))
		_, _ = w.WriteString(`</a></video>`)
		return ast.WalkSkipChildren, nil
	})
}

// unqualifiedKegLinkRE matches keg:NS/KEG/N, a cross-KEG link missing the @
// that marks its namespace.
var unqualifiedKegLinkRE = regexp.MustCompile(`^keg:([A-Za-z0-9][A-Za-z0-9_-]*)/([A-Za-z0-9][A-Za-z0-9_-]*)/([0-9]+)`)

// KegLinkProblems reports each keg: link in markdown src that cannot be
// followed: one outside the keg: syntax, which renders as a dead link and
// links nothing, or a keg:~alias/N whose alias aliases does not name. Each
// problem is reported once, in order of first appearance.
func KegLinkProblems(src []byte, aliases []AliasEntry) []string {
	doc := goldmark.New().Parser().Parse(text.NewReader(src))
	var problems []string
	seen := map[string]bool{}
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		link, ok := n.(*ast.Link)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}
		dest := strings.TrimSpace(string(link.Destination))
		if !strings.HasPrefix(dest, "keg:") || seen[dest] {
			return ast.WalkContinue, nil
		}
		seen[dest] = true
		m := kegSchemeRE.FindStringSubmatch(dest)
		switch {
		case m == nil:
			if u := unqualifiedKegLinkRE.FindStringSubmatch(dest); u != nil {
				problems = append(problems, fmt.Sprintf("link %q is not a keg link; to link another KEG write keg:@%s/%s/%s", dest, u[1], u[2], u[3]))
			} else {
				problems = append(problems, fmt.Sprintf("link %q is not a keg link; write keg:@namespace/keg/N, or keg:~alias/N through the settings aliases", dest))
			}
		case strings.HasPrefix(m[2], "~"):
			if m[1] != "" {
				problems = append(problems, fmt.Sprintf("link %q names both a namespace and an alias; write keg:~alias/N or keg:@namespace/keg/N", dest))
			} else if _, _, err := ResolveSettingsAlias(aliases, strings.TrimPrefix(m[2], "~")); err != nil {
				problems = append(problems, fmt.Sprintf("link %q cannot be followed: %v", dest, err))
			}
		}
		return ast.WalkContinue, nil
	})
	return problems
}
