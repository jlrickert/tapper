package keg

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// mapping used across link-rewriting tests: src node 1→4, src node 2→5.
var importTestMapping = map[string]NodeId{
	"1": {ID: 4},
	"2": {ID: 5},
}

func TestRewriteArchiveLinks_Rule1_RelativeImported(t *testing.T) {
	t.Parallel()
	// ../N where N is in the mapping → ../NEW_ID
	input := "See [foo](../1) and bare ../2.\n"
	got := rewriteArchiveLinks([]byte(input), "src", "tgt", importTestMapping)
	require.Contains(t, string(got), "(../4)")
	require.Contains(t, string(got), "../5.")
	require.NotContains(t, string(got), "../1")
	require.NotContains(t, string(got), "../2")
}

func TestRewriteArchiveLinks_Rule2_RelativeNotImported(t *testing.T) {
	t.Parallel()
	// ../N where N is NOT in the mapping → keg:srcAlias/N
	input := "Ref [bar](../3) and bare ../3.\n"
	got := rewriteArchiveLinks([]byte(input), "src", "tgt", importTestMapping)
	require.Contains(t, string(got), "(keg:src/3)")
	require.Contains(t, string(got), "keg:src/3.")
	require.NotContains(t, string(got), "../3")
}

func TestRewriteArchiveLinks_Rule2_NoRewriteWhenSrcAliasEmpty(t *testing.T) {
	t.Parallel()
	// Without a known src alias, non-imported relative links are left alone.
	input := "Ref ../9.\n"
	got := rewriteArchiveLinks([]byte(input), "", "tgt", importTestMapping)
	require.Equal(t, input, string(got))
}

func TestRewriteArchiveLinks_Rule3_CrossKegTarget(t *testing.T) {
	t.Parallel()
	// keg:tgtAlias/N → ../N
	input := "Ref [baz](keg:tgt/7).\n"
	got := rewriteArchiveLinks([]byte(input), "src", "tgt", importTestMapping)
	require.Contains(t, string(got), "../7")
	require.NotContains(t, string(got), "keg:tgt/7")
}

func TestRewriteArchiveLinks_Rule4_CrossKegSrcImported(t *testing.T) {
	t.Parallel()
	// keg:srcAlias/N where N is imported → ../NEW_ID
	input := "Ref [x](keg:src/2).\n"
	got := rewriteArchiveLinks([]byte(input), "src", "tgt", importTestMapping)
	require.Contains(t, string(got), "../5")
	require.NotContains(t, string(got), "keg:src/2")
}

func TestRewriteArchiveLinks_Rule5_CrossKegSrcNotImported(t *testing.T) {
	t.Parallel()
	// keg:srcAlias/N where N is NOT imported → unchanged
	input := "Ref [y](keg:src/99).\n"
	got := rewriteArchiveLinks([]byte(input), "src", "tgt", importTestMapping)
	require.Equal(t, input, string(got))
}

func TestRewriteArchiveLinks_Rule6_CrossKegOtherAlias(t *testing.T) {
	t.Parallel()
	// keg:otherAlias/N → unchanged
	input := "Ref [z](keg:other/9).\n"
	got := rewriteArchiveLinks([]byte(input), "src", "tgt", importTestMapping)
	require.Equal(t, input, string(got))
}

func TestRewriteArchiveLinks_PassOrderNoInterference(t *testing.T) {
	t.Parallel()
	// Pass 2 produces new ../N links from keg:tgt/N rewrites.
	// Those should NOT be re-processed by the relative-link pass (which already ran).
	// Here keg:tgt/9 → ../9, and 9 is not in the mapping,
	// so the final ../9 must remain as ../9, NOT become keg:src/9.
	mapping := map[string]NodeId{"1": {ID: 4}}
	input := "Ref [q](keg:tgt/9).\n"
	got := rewriteArchiveLinks([]byte(input), "src", "tgt", mapping)
	require.Equal(t, "Ref [q](../9).\n", string(got))
}

func TestRewriteArchiveLinks_EmptyContent(t *testing.T) {
	t.Parallel()
	got := rewriteArchiveLinks(nil, "src", "tgt", importTestMapping)
	require.Nil(t, got)

	got = rewriteArchiveLinks([]byte{}, "src", "tgt", importTestMapping)
	require.Equal(t, []byte{}, got)
}

func TestRewriteArchiveLinks_NoBothAliases(t *testing.T) {
	t.Parallel()
	// When both aliases are empty, cross-keg pass is skipped entirely.
	input := "Ref [a](keg:foo/1) and [b](../1).\n"
	// ../1 is in mapping → ../4; keg:foo/1 unchanged.
	got := rewriteArchiveLinks([]byte(input), "", "", importTestMapping)
	require.Contains(t, string(got), "../4")
	require.Contains(t, string(got), "keg:foo/1")
}
