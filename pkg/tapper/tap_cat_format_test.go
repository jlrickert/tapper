package tapper

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatFrontmatter_ClosingDelimiterOnOwnLine(t *testing.T) {
	t.Parallel()
	meta := []byte("tags:\n  - wow\n  - gaming")
	content := []byte("# Devastation Evoker priorities\n")

	got := formatFrontmatter(meta, content)

	require.Contains(t, got, "\n---\n# Devastation Evoker priorities\n")
	require.NotContains(t, got, "gaming---")
}

func TestFormatFrontmatter_NoExtraBlankLineWhenMetaEndsWithNewline(t *testing.T) {
	t.Parallel()
	meta := []byte("title: Example\n")
	content := []byte("body")

	got := formatFrontmatter(meta, content)

	require.Equal(t, "---\ntitle: Example\n---\nbody", got)
}
