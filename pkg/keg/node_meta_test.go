package keg_test

import (
	"context"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestParseMeta_EmptyReturnsEmptyMeta(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	m, err := keg.ParseMeta(ctx, []byte("   \n\t"))
	require.NoError(t, err)
	require.NotNil(t, m)
	_, ok := m.Get("updated")
	require.False(t, ok)
}

func TestSetGetAndUnsetTagsKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	m := keg.NewMeta(ctx, time.Now())
	require.NoError(t, m.Set(ctx, "tags", []string{"Alpha", "beta"}))

	v, ok := m.Get("tags")
	require.True(t, ok)
	require.Equal(t, "alpha,beta", v)

	require.NoError(t, m.Set(ctx, "tags", nil))
	_, ok = m.Get("tags")
	require.False(t, ok)
}

func TestProgrammaticKeysAreRemovedFromMetaYAML(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	raw := []byte(`id: "42"
title: programmatic
hash: abc123
updated: 2025-01-01T00:00:00Z
created: 2025-01-01T00:00:00Z
accessed: 2025-01-01T00:00:00Z
access_count: 3
lead: a summary
links:
  - ../7/README.md
note: keep-me
`)
	m, err := keg.ParseMeta(ctx, raw)
	require.NoError(t, err)

	out := m.ToYAML()
	for _, key := range []string{
		"id:", "title:", "hash:", "updated:", "created:",
		"accessed:", "access_count:", "lead:", "links:",
	} {
		require.NotContains(t, out, key)
	}
	require.Contains(t, out, "note: keep-me")
}

func TestParseMeta_TagsInMeta(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	raw := []byte("title: My Fancy Title\nhash: abc123\ntags: [a, b]\n")
	m, err := keg.ParseMeta(ctx, raw)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, m.Tags())
}

func TestParseMeta_PreservesComments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	raw := []byte(`# keep-this-comment
# another comment line
note: Title With Comment
# inline-note: preserve me
hash: comment-hash
`)
	m, err := keg.ParseMeta(ctx, raw)
	require.NoError(t, err)

	out := m.ToYAML()
	require.Contains(t, out, "another comment line")
	require.Contains(t, out, "note: Title With Comment")
	require.NotContains(t, out, "hash:")
}

func TestToYAMLWithStats_WritesProgrammaticFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	m := keg.NewMeta(ctx, now)
	m.SetTags([]string{"Alpha", "beta"})

	s := keg.NewStats(now)
	s.SetTitle("Node")
	s.SetHash("h1", &now)
	s.SetLead("summary")
	s.SetLinks([]keg.NodeId{{ID: 1}, {ID: 2}})
	s.SetAccessed(now)
	s.SetAccessCount(5)
	s.SetOmega(0.75)

	out := m.ToYAMLWithStats(s)
	require.Contains(t, out, "title: Node")
	require.Contains(t, out, "tags:")
	require.Contains(t, out, "- alpha")
	require.Contains(t, out, "- beta")
	require.Contains(t, out, "hash: h1")
	require.Contains(t, out, "updated:")
	require.Contains(t, out, "created:")
	require.Contains(t, out, "accessed:")
	require.Contains(t, out, "access_count: 5")
	require.Contains(t, out, "lead: summary")
	require.Contains(t, out, "omega: 0.75")
	require.Contains(t, out, "- \"1\"")
	require.Contains(t, out, "- \"2\"")
}

func TestSetAttrs_AppliesKnownAndUnknownKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	raw := []byte(`# initial
title: Orig
hash: h1
tags: [old]
baz: box
`)
	m, err := keg.ParseMeta(ctx, raw)
	require.NoError(t, err)

	attrs := map[string]any{
		"tags": "NewTag, another",
		"foo":  "bar",
		"baz":  "boxy",
	}
	require.NoError(t, m.SetAttrs(ctx, attrs))

	out := m.ToYAML()
	require.Contains(t, out, "foo: bar")
	require.Contains(t, out, "baz: boxy")
	require.NotContains(t, out, "title:")
	require.NotContains(t, out, "hash:")
	require.Equal(t, []string{"another", "newtag"}, m.Tags())

	parsed, err := keg.ParseMeta(ctx, []byte(out))
	require.NoError(t, err)
	require.Equal(t, []string{"another", "newtag"}, parsed.Tags())
}

func TestTagEditsPreserveComments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	raw := []byte(`# top
tags:
  # keep beta comment
  - beta
  - alpha
`)
	m, err := keg.ParseMeta(ctx, raw)
	require.NoError(t, err)

	m.AddTag("Gamma")
	m.RmTag("alpha")

	out := m.ToYAML()
	require.Contains(t, out, "# keep beta comment")
	require.Contains(t, out, "- beta")
	require.Contains(t, out, "- gamma")
	require.NotContains(t, out, "- alpha")
}

// TestSetAttrs_PreservesScalarTypes covers tapper#91: SetAttrs used to tag every
// numeric scalar !!str, so a schema field typed `integer` could never be
// satisfied through create or through an edit carrying inline frontmatter.
func TestSetAttrs_PreservesScalarTypes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	m, err := keg.ParseMeta(ctx, []byte("# initial\ntype: change\n"))
	require.NoError(t, err)

	require.NoError(t, m.SetAttrs(ctx, map[string]any{
		// YAML frontmatter decodes an integer literal as int.
		"contract_count": 1,
		// JSON (the MCP attrs path) has no integer type, so the same value
		// arrives as float64 and must still write as an integer.
		"json_count": float64(2),
		"ratio":      0.75,
		"enabled":    true,
		"label":      "text",
	}))

	out := m.ToYAML()
	require.Contains(t, out, "contract_count: 1")
	require.Contains(t, out, "json_count: 2")
	require.Contains(t, out, "ratio: 0.75")
	require.Contains(t, out, "enabled: true")
	require.Contains(t, out, "label: text")
	// The bug's signature was a quoted scalar where a number belonged.
	require.NotContains(t, out, `"1"`)
	require.NotContains(t, out, `"0.75"`)

	// Round-trip through YAML to confirm the emitted tags actually resolve back
	// to numbers rather than to strings that merely look unquoted.
	var back map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(out), &back))
	require.Equal(t, 1, back["contract_count"])
	require.Equal(t, 2, back["json_count"])
	require.Equal(t, 0.75, back["ratio"])
	require.Equal(t, true, back["enabled"])
	require.Equal(t, "text", back["label"])
}

// TestSetAttrs_NestedMapsAreDeterministic guards the sorted key order in
// valueToYAMLNode: ranging a Go map directly reordered nested keys run to run,
// which made otherwise identical writes produce different meta.yaml bytes.
func TestSetAttrs_NestedMapsAreDeterministic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	render := func() string {
		m, err := keg.ParseMeta(ctx, []byte("# initial\n"))
		require.NoError(t, err)
		require.NoError(t, m.SetAttrs(ctx, map[string]any{
			"nested": map[string]any{
				"zulu": 1, "alpha": 2, "mike": 3, "delta": 4, "papa": 5,
			},
		}))
		return m.ToYAML()
	}

	first := render()
	for i := 0; i < 8; i++ {
		require.Equal(t, first, render())
	}
}
