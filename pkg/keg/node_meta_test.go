package keg_test

import (
	"context"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
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

	m := keg.NewMeta(ctx, time.Now())
	require.NoError(t, m.Set(ctx, "hash", "abc123"))

	out := m.ToYAML()
	require.NotContains(t, out, "hash:")
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

	out := m.ToYAMLWithStats(s)
	require.Contains(t, out, "title: Node")
	require.Contains(t, out, "tags:")
	require.Contains(t, out, "- alpha")
	require.Contains(t, out, "- beta")
	require.Contains(t, out, "hash: h1")
	require.Contains(t, out, "updated:")
	require.Contains(t, out, "created:")
	require.Contains(t, out, "accessed:")
	require.Contains(t, out, "lead: summary")
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
