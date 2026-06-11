package keg_test

import (
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestRenderMarkdown_basic(t *testing.T) {
	src := []byte("# Hello World\n\nThis is a paragraph.\n")
	html, err := keg.RenderMarkdown(src, keg.RenderOptions{})
	require.NoError(t, err)
	require.Contains(t, string(html), "<h1>Hello World</h1>")
	require.Contains(t, string(html), "<p>This is a paragraph.</p>")
}

func TestRenderMarkdown_rewriteNodeLinks(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		baseURL  string
		contains string
	}{
		{
			name:     "basic node link",
			src:      "See [node 42](../42) for details.",
			baseURL:  "/",
			contains: `href="/42/"`,
		},
		{
			name:     "node link with custom base URL",
			src:      "See [node 42](../42) for details.",
			baseURL:  "/keg/dev/",
			contains: `href="/keg/dev/42/"`,
		},
		{
			name:     "node link without trailing slash on base",
			src:      "See [node 42](../42) for details.",
			baseURL:  "/keg/dev",
			contains: `href="/keg/dev/42/"`,
		},
		{
			name:     "multiple node links",
			src:      "See [a](../10) and [b](../20).",
			baseURL:  "/",
			contains: `href="/10/"`,
		},
		{
			name:     "non-node link preserved",
			src:      "See [Google](https://google.com).",
			baseURL:  "/",
			contains: `href="https://google.com"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html, err := keg.RenderMarkdown([]byte(tt.src), keg.RenderOptions{BaseURL: tt.baseURL})
			require.NoError(t, err)
			require.Contains(t, string(html), tt.contains)
		})
	}
}

func TestRenderMarkdown_tables(t *testing.T) {
	src := "| A | B |\n|---|---|\n| 1 | 2 |\n"
	html, err := keg.RenderMarkdown([]byte(src), keg.RenderOptions{})
	require.NoError(t, err)
	require.Contains(t, string(html), "<table>")
	require.Contains(t, string(html), "<td>1</td>")
}

func TestRenderMarkdown_strikethrough(t *testing.T) {
	src := "This is ~~deleted~~ text.\n"
	html, err := keg.RenderMarkdown([]byte(src), keg.RenderOptions{})
	require.NoError(t, err)
	require.Contains(t, string(html), "<del>deleted</del>")
}

func TestRenderMarkdown_taskList(t *testing.T) {
	src := "- [x] Done\n- [ ] Not done\n"
	html, err := keg.RenderMarkdown([]byte(src), keg.RenderOptions{})
	require.NoError(t, err)
	require.Contains(t, string(html), "checked")
}

func TestRenderMarkdown_codeBlock(t *testing.T) {
	src := "```go\nfunc main() {}\n```\n"
	html, err := keg.RenderMarkdown([]byte(src), keg.RenderOptions{})
	require.NoError(t, err)
	// Syntax highlighting should produce some styled output.
	require.Contains(t, string(html), "func")
}

func TestRenderMarkdown_emptyInput(t *testing.T) {
	html, err := keg.RenderMarkdown([]byte(""), keg.RenderOptions{})
	require.NoError(t, err)
	require.Equal(t, "", strings.TrimSpace(string(html)))
}

func TestRenderMarkdown_multipleNodeLinksRewritten(t *testing.T) {
	src := "See [a](../10) and [b](../20)."
	html, err := keg.RenderMarkdown([]byte(src), keg.RenderOptions{BaseURL: "/"})
	require.NoError(t, err)
	require.Contains(t, string(html), `href="/10/"`)
	require.Contains(t, string(html), `href="/20/"`)
}

func TestResolveNodeLink(t *testing.T) {
	hubOpts := keg.RenderOptions{
		BaseURL:         "/@foldwise/example/",
		NodeID:          "2",
		NoTrailingSlash: true,
		KegResolver: func(ns, alias, id string) string {
			if ns == "" {
				ns = "foldwise"
			}
			return "/@" + ns + "/" + alias + "/" + id
		},
	}
	cliOpts := keg.RenderOptions{BaseURL: "/", NodeID: "2"}
	legacyOpts := keg.RenderOptions{BaseURL: "/keg/dev/"}

	tests := []struct {
		name    string
		dest    string
		opts    keg.RenderOptions
		want    string
		rewrite bool
	}{
		{"hub sibling node", "../1", hubOpts, "/@foldwise/example/1", true},
		{"hub node with anchor", "../3#section", hubOpts, "/@foldwise/example/3#section", true},
		{"hub explicit README.md", "../3/README.md", hubOpts, "/@foldwise/example/3", true},
		{"hub node trailing slash", "../3/", hubOpts, "/@foldwise/example/3", true},
		{"hub same-node image", "./images/x.png", hubOpts, "/@foldwise/example/2/images/x.png", true},
		{"hub same-node asset", "./assets/doc.pdf", hubOpts, "/@foldwise/example/2/assets/doc.pdf", true},
		{"hub bare relative image", "images/x.png", hubOpts, "/@foldwise/example/2/images/x.png", true},
		{"hub keg full ref", "keg:@acme/notes/5", hubOpts, "/@acme/notes/5", true},
		{"hub keg bare alias", "keg:public/7", hubOpts, "/@foldwise/public/7", true},
		{"hub keg with anchor", "keg:public/7#a", hubOpts, "/@foldwise/public/7#a", true},
		{"cli sibling node keeps slash", "../1", cliOpts, "/1/", true},
		{"cli same-node image", "./images/x.png", cliOpts, "/2/images/x.png", true},
		{"cli keg without resolver", "keg:public/7", cliOpts, "", false},
		{"legacy node link", "../42", legacyOpts, "/keg/dev/42/", true},
		{"legacy non-node relative", "./images/x.png", legacyOpts, "", false},
		{"absolute http", "https://example.com/a", hubOpts, "", false},
		{"mailto", "mailto:a@b.c", hubOpts, "", false},
		{"fragment only", "#frag", hubOpts, "", false},
		{"absolute path", "/abs/path", hubOpts, "", false},
		{"protocol relative", "//example.com/x", hubOpts, "", false},
		{"empty", "", hubOpts, "", false},
		{"non-node sibling path", "../abc", hubOpts, "/@foldwise/example/abc", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := keg.ResolveNodeLink(tt.dest, tt.opts)
			require.Equal(t, tt.rewrite, ok)
			if tt.rewrite {
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestRenderMarkdown_hubStyleResolution(t *testing.T) {
	opts := keg.RenderOptions{
		BaseURL:         "/@foldwise/example/",
		NodeID:          "2",
		NoTrailingSlash: true,
		KegResolver: func(ns, alias, id string) string {
			if ns == "" {
				ns = "foldwise"
			}
			return "/@" + ns + "/" + alias + "/" + id
		},
	}
	src := strings.Join([]string{
		"See [node 1](../1) and ![diagram](./images/d.png).",
		"",
		"Cross-keg: [pub](keg:public/7).",
		"",
		"Raw HTML stays: <a href=\"../9\">nine</a>",
	}, "\n")
	html, err := keg.RenderMarkdown([]byte(src), opts)
	require.NoError(t, err)
	out := string(html)
	require.Contains(t, out, `href="/@foldwise/example/1"`)
	require.Contains(t, out, `src="/@foldwise/example/2/images/d.png"`)
	require.Contains(t, out, `href="/@foldwise/public/7"`)
	// goldmark omits raw HTML by default; either way, raw <a href="../9">
	// is never rewritten by the transformer.
	require.NotContains(t, out, `href="/@foldwise/example/9"`)
}

func TestRenderMarkdown_autolinkUntouched(t *testing.T) {
	src := "Visit https://example.com/page now."
	html, err := keg.RenderMarkdown([]byte(src), keg.RenderOptions{NodeID: "2"})
	require.NoError(t, err)
	require.Contains(t, string(html), `href="https://example.com/page"`)
}
