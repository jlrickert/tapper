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
