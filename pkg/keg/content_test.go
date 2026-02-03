package keg_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestParseContent_MarkdownTitleAndLead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ctx = toolkit.WithHasher(ctx, &toolkit.MD5Hasher{})

	md := `# My Title

This is the lead paragraph.
It continues on the same paragraph.

## Details

More content and an outgoing link: ../1 and ../2 and ../1
`

	c, err := keg.ParseContent(ctx, []byte(md), "README.md")
	require.NoError(t, err)
	require.Equal(t, "markdown", c.Format)
	require.Equal(t, "My Title", c.Title)
	require.Equal(t, "This is the lead paragraph. It continues on the same paragraph.", c.Lead)

	// links should be deduped and sorted
	expected := []keg.NodeId{{ID: 1}, {ID: 2}}
	require.Equal(t, expected, c.Links)
	// hash should match the hasher injected into ctx
	require.Equal(t, toolkit.HasherFromContext(ctx).Hash([]byte(md)), c.Hash)
}

func TestParseContent_MarkdownFallbackTitleAndLead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ctx = toolkit.WithHasher(ctx, &toolkit.MD5Hasher{})

	md := `Some title line

This is the first paragraph after the title fallback.

Another paragraph.
`

	c, err := keg.ParseContent(ctx, []byte(md), "README.md")
	require.NoError(t, err)
	require.Equal(t, "markdown", c.Format)
	require.Equal(t, "Some title line", c.Title)
	require.Equal(t, "This is the first paragraph after the title fallback.", c.Lead)
}

func TestParseContent_EmptyInputReturnsEmptyFormat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ctx = toolkit.WithHasher(ctx, &toolkit.MD5Hasher{})

	empty := "    \n\t\n"
	c, err := keg.ParseContent(ctx, []byte(empty), "README.md")
	require.NoError(t, err)
	require.Equal(t, "empty", c.Format)
	require.Equal(t, "", c.Title)
	require.Equal(t, "", c.Lead)
	require.Empty(t, c.Links)
}

func TestParseContent_MarkdownFrontmatterAndBody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ctx = toolkit.WithHasher(ctx, &toolkit.MD5Hasher{})

	md := `---
foo: bar
tags:
  - one
  - two
---
# Front Title

This is the lead paragraph extracted after frontmatter.

More content and a link ../3
`

	c, err := keg.ParseContent(ctx, []byte(md), "README.md")
	require.NoError(t, err)

	require.Equal(t, "markdown", c.Format)

	// Frontmatter parsed into a map
	require.NotNil(t, c.Frontmatter)
	require.Equal(t, "bar", c.Frontmatter["foo"])

	// tags should be a slice; YAML unmarshal yields []any elements
	tagsRaw, ok := c.Frontmatter["tags"].([]any)
	require.True(t, ok, "tags should be a slice")
	require.Len(t, tagsRaw, 2)
	require.Equal(t, "one", tagsRaw[0])
	require.Equal(t, "two", tagsRaw[1])

	// Body must not include the frontmatter delimiters
	require.False(t, strings.HasPrefix(c.Body, "---"), "body should not start with frontmatter")
	// Body should start with the first content line (the heading)
	require.True(t, strings.HasPrefix(c.Body, "# Front Title"), "body should retain heading")

	// Title and lead should be derived from the body content
	require.Equal(t, "Front Title", c.Title)
	require.Equal(t, "This is the lead paragraph extracted after frontmatter.", c.Lead)
}

func TestParseContent_MarkdownFrontmatterNoH1UsesFirstLine(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ctx = toolkit.WithHasher(ctx, &toolkit.MD5Hasher{})

	md := `---
meta: value
---
# Fallback title line

Lead paragraph following the fallback title.
`

	c, err := keg.ParseContent(ctx, []byte(md), "README.md")
	require.NoError(t, err)

	require.Equal(t, "markdown", c.Format)
	// When no H1 present, first non-empty line should be title
	require.Equal(t, "Fallback title line", c.Title)
	require.Equal(t, "Lead paragraph following the fallback title.", c.Lead)

	// Ensure frontmatter was parsed
	require.Equal(t, "value", c.Frontmatter["meta"])
	// Body should not contain frontmatter markers
	require.False(t, strings.HasPrefix(c.Body, "---"))
}
