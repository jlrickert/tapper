package keg_test

import (
	"bytes"
	"context"
	"strings"
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
	require.Empty(t, m.Tags())
	_, ok := m.Get("updated")
	require.False(t, ok)
}

func TestTags_Normalization_AddRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	m := keg.NewMeta(ctx, time.Now())
	err := m.Set(ctx, "tags", "Zeke, Draft  ,  other_tag")
	require.NoError(t, err)

	tags := m.Tags()
	require.Equal(t, []string{"draft", "other_tag", "zeke"}, tags)

	m.AddTag("ZEKE")
	require.Equal(t, []string{"draft", "other_tag", "zeke"}, m.Tags())

	m.AddTag("New Tag!")
	require.Equal(t, []string{"draft", "new-tag", "other_tag", "zeke"}, m.Tags())

	m.RmTag("other_TAG")
	require.Equal(t, []string{"draft", "new-tag", "zeke"}, m.Tags())

	m.RmTag("nonexistent")
	require.Equal(t, []string{"draft", "new-tag", "zeke"}, m.Tags())
}

func TestSetGetAndUnsetTitleKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	m := keg.NewMeta(ctx, time.Now())
	require.NoError(t, m.Set(ctx, "title", "hello"))

	v, ok := m.Get("title")
	require.True(t, ok)
	require.Equal(t, "hello", v)

	require.NoError(t, m.Set(ctx, "title", nil))
	_, ok = m.Get("title")
	require.False(t, ok)
}

func TestProgrammaticKeysNotReturnedByMetaGet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	m := keg.NewMeta(ctx, time.Now())
	require.NoError(t, m.Set(ctx, "hash", "abc123"))

	_, ok := m.Get("hash")
	require.False(t, ok)
	out := m.ToYAML()
	require.Contains(t, out, "hash: abc123")
}

func TestToYAML_NormalizesTagsOutput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	m := keg.NewMeta(ctx, time.Now())
	require.NoError(t, m.Set(ctx, "tags", []string{"A B", "a-b", "C,c"}))

	out := m.ToYAML()
	lout := strings.ToLower(out)
	require.Contains(t, lout, "a-b")
	require.Contains(t, lout, "c")
	require.True(t,
		bytes.Contains([]byte(out), []byte("tags:")) || strings.Contains(out, "tags:"),
	)
}

func TestParseMeta_TitleAndStatsSplit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	raw := []byte("title: My Fancy Title\nhash: abc123\n")
	m, err := keg.ParseMeta(ctx, raw)
	require.NoError(t, err)
	require.Equal(t, "My Fancy Title", m.Title())
	_, ok := m.Get("hash")
	require.False(t, ok)

	s, err := keg.ParseStats(ctx, raw)
	require.NoError(t, err)
	require.Equal(t, "abc123", s.Hash())
}

func TestParseMeta_PreservesComments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	raw := []byte(`# keep-this-comment
# another comment line
title: Title With Comment
# inline-note: preserve me
hash: comment-hash
`)
	m, err := keg.ParseMeta(ctx, raw)
	require.NoError(t, err)

	out := m.ToYAML()
	require.Contains(t, out, "keep-this-comment")
	require.Contains(t, out, "preserve me")
	require.Contains(t, out, "Title With Comment")
	require.Contains(t, out, "comment-hash")
}

func TestToYAMLWithStats_WritesProgrammaticFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	m := keg.NewMeta(ctx, now)
	m.SetTitle(ctx, "Node")
	m.SetTags(ctx, []string{"alpha"})

	s := keg.NewStats(now)
	s.SetHash("h1", &now)
	s.SetLead("summary")
	s.SetLinks([]keg.NodeId{{ID: 1}, {ID: 2}})
	s.SetAccessed(now)

	out := m.ToYAMLWithStats(s)
	require.Contains(t, out, "title: Node")
	require.Contains(t, out, "tags:")
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

	require.Equal(t, []string{"another", "newtag"}, m.Tags())

	out := m.ToYAML()
	require.Contains(t, out, "foo: bar")
	require.Contains(t, out, "baz: boxy")
}
