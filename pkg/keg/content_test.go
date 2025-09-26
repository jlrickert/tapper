package keg_test

import (
	"context"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tap"
	"github.com/stretchr/testify/require"
)

func TestParseContent_MarkdownTitleAndLead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ctx = tap.WithHasher(ctx, &tap.MD5Hasher{})

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
	expected := []keg.Node{{ID: 1}, {ID: 2}}
	require.Equal(t, expected, c.Links)
	// hash should match the hasher injected into ctx
	require.Equal(t, tap.HasherFromContext(ctx).Hash([]byte(md)), c.Hash)
}

func TestParseContent_MarkdownFallbackTitleAndLead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ctx = tap.WithHasher(ctx, &tap.MD5Hasher{})

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
	ctx = tap.WithHasher(ctx, &tap.MD5Hasher{})

	empty := "    \n\t\n"
	c, err := keg.ParseContent(ctx, []byte(empty), "README.md")
	require.NoError(t, err)
	require.Equal(t, "empty", c.Format)
	require.Equal(t, "", c.Title)
	require.Equal(t, "", c.Lead)
	require.Empty(t, c.Links)
}
