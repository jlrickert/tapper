package tapper

import (
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// mapping used across link-rewriting tests: src node 1→4, src node 2→5.
var importTestMapping = map[string]keg.NodeId{
	"1": {ID: 4},
	"2": {ID: 5},
}

func TestRewriteLiveImportLinks_Rule1_RelativeImported(t *testing.T) {
	t.Parallel()
	// ../N where N is in the mapping → ../NEW_ID
	input := "See [foo](../1) and bare ../2.\n"
	got := rewriteLiveImportLinks([]byte(input), "src", "tgt", importTestMapping)
	require.Contains(t, string(got), "(../4)")
	require.Contains(t, string(got), "../5.")
	require.NotContains(t, string(got), "../1")
	require.NotContains(t, string(got), "../2")
}

func TestRewriteLiveImportLinks_Rule2_RelativeNotImported(t *testing.T) {
	t.Parallel()
	// ../N where N is NOT in the mapping → keg:srcAlias/N
	input := "Ref [bar](../3) and bare ../3.\n"
	got := rewriteLiveImportLinks([]byte(input), "src", "tgt", importTestMapping)
	require.Contains(t, string(got), "(keg:src/3)")
	require.Contains(t, string(got), "keg:src/3.")
	require.NotContains(t, string(got), "../3")
}

func TestRewriteLiveImportLinks_Rule2_NoRewriteWhenSrcAliasEmpty(t *testing.T) {
	t.Parallel()
	// Without a known src alias, non-imported relative links are left alone.
	input := "Ref ../9.\n"
	got := rewriteLiveImportLinks([]byte(input), "", "tgt", importTestMapping)
	require.Equal(t, input, string(got))
}

func TestRewriteLiveImportLinks_Rule3_CrossKegTarget(t *testing.T) {
	t.Parallel()
	// keg:tgtAlias/N → ../N
	input := "Ref [baz](keg:tgt/7).\n"
	got := rewriteLiveImportLinks([]byte(input), "src", "tgt", importTestMapping)
	require.Contains(t, string(got), "../7")
	require.NotContains(t, string(got), "keg:tgt/7")
}

func TestRewriteLiveImportLinks_Rule4_CrossKegSrcImported(t *testing.T) {
	t.Parallel()
	// keg:srcAlias/N where N is imported → ../NEW_ID
	input := "Ref [x](keg:src/2).\n"
	got := rewriteLiveImportLinks([]byte(input), "src", "tgt", importTestMapping)
	require.Contains(t, string(got), "../5")
	require.NotContains(t, string(got), "keg:src/2")
}

func TestRewriteLiveImportLinks_Rule5_CrossKegSrcNotImported(t *testing.T) {
	t.Parallel()
	// keg:srcAlias/N where N is NOT imported → unchanged
	input := "Ref [y](keg:src/99).\n"
	got := rewriteLiveImportLinks([]byte(input), "src", "tgt", importTestMapping)
	require.Equal(t, input, string(got))
}

func TestRewriteLiveImportLinks_Rule6_CrossKegOtherAlias(t *testing.T) {
	t.Parallel()
	// keg:otherAlias/N → unchanged
	input := "Ref [z](keg:other/9).\n"
	got := rewriteLiveImportLinks([]byte(input), "src", "tgt", importTestMapping)
	require.Equal(t, input, string(got))
}

func TestRewriteLiveImportLinks_PassOrderNoInterference(t *testing.T) {
	t.Parallel()
	// Pass 2 produces new ../N links from keg:tgt/N rewrites.
	// Those should NOT be re-processed by the relative-link pass (which already ran).
	// Here keg:tgt/9 → ../9, and 9 is not in the mapping,
	// so the final ../9 must remain as ../9, NOT become keg:src/9.
	mapping := map[string]keg.NodeId{"1": {ID: 4}}
	input := "Ref [q](keg:tgt/9).\n"
	got := rewriteLiveImportLinks([]byte(input), "src", "tgt", mapping)
	require.Equal(t, "Ref [q](../9).\n", string(got))
}

func TestRewriteLiveImportLinks_EmptyContent(t *testing.T) {
	t.Parallel()
	got := rewriteLiveImportLinks(nil, "src", "tgt", importTestMapping)
	require.Nil(t, got)

	got = rewriteLiveImportLinks([]byte{}, "src", "tgt", importTestMapping)
	require.Equal(t, []byte{}, got)
}

func TestRewriteLiveImportLinks_NoBothAliases(t *testing.T) {
	t.Parallel()
	// When both aliases are empty, cross-keg pass is skipped entirely.
	input := "Ref [a](keg:foo/1) and [b](../1).\n"
	// ../1 is in mapping → ../4; keg:foo/1 unchanged.
	got := rewriteLiveImportLinks([]byte(input), "", "", importTestMapping)
	require.Contains(t, string(got), "../4")
	require.Contains(t, string(got), "keg:foo/1")
}

func TestResolveImportSourceAlias_BareIDs(t *testing.T) {
	t.Parallel()
	alias, bareIDs, err := resolveImportSourceAlias([]string{"1", "2", "3"}, "mykeg")
	require.NoError(t, err)
	require.Equal(t, "mykeg", alias)
	require.Equal(t, []string{"1", "2", "3"}, bareIDs)
}

func TestResolveImportSourceAlias_KegRefArgs(t *testing.T) {
	t.Parallel()
	alias, bareIDs, err := resolveImportSourceAlias(
		[]string{"keg:pub/5", "keg:pub/7"}, "",
	)
	require.NoError(t, err)
	require.Equal(t, "pub", alias)
	require.Equal(t, []string{"5", "7"}, bareIDs)
}

func TestResolveImportSourceAlias_KegRefConflictsWithFrom(t *testing.T) {
	t.Parallel()
	_, _, err := resolveImportSourceAlias([]string{"keg:pub/1"}, "other")
	require.Error(t, err)
}

func TestResolveImportSourceAlias_ConflictingAliasesInArgs(t *testing.T) {
	t.Parallel()
	_, _, err := resolveImportSourceAlias(
		[]string{"keg:pub/1", "keg:priv/2"}, "",
	)
	require.Error(t, err)
}

func TestResolveImportSourceAlias_MixedBareAndKegRef(t *testing.T) {
	t.Parallel()
	// Mixing bare IDs with keg: refs is allowed; bare IDs are kept as-is.
	alias, bareIDs, err := resolveImportSourceAlias(
		[]string{"3", "keg:pub/5"}, "",
	)
	require.NoError(t, err)
	require.Equal(t, "pub", alias)
	require.Equal(t, []string{"3", "5"}, bareIDs)
}

func TestFilterZeroImportNode(t *testing.T) {
	t.Parallel()
	ids := []keg.NodeId{{ID: 0}, {ID: 1}, {ID: 2}, {ID: 0, Code: "draft"}}
	got := filterZeroImportNode(ids)
	// Node 0 without code is filtered; Node 0 with code is kept.
	require.Len(t, got, 3)
	require.Equal(t, 1, got[0].ID)
	require.Equal(t, 2, got[1].ID)
	require.Equal(t, 0, got[2].ID)
	require.Equal(t, "draft", got[2].Code)
}

func TestUnionImportNodeIDs_Deduplication(t *testing.T) {
	t.Parallel()
	a := []keg.NodeId{{ID: 1}, {ID: 2}}
	b := []keg.NodeId{{ID: 2}, {ID: 3}}
	got := unionImportNodeIDs(a, b)
	require.Len(t, got, 3)
}
