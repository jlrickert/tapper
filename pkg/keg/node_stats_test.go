package keg_test

import (
	"context"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestParseStats_ParsesProgrammaticFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	raw := []byte(`hash: abc
updated: 2024-01-02T03:04:05Z
created: 2024-01-01T03:04:05Z
accessed: 2024-01-03T03:04:05Z
lead: short
tags:
  - alpha
  - beta
links:
  - 1
  - 2
`)

	s, err := keg.ParseStats(ctx, raw)
	require.NoError(t, err)
	require.Equal(t, "abc", s.Hash())
	require.Equal(t, time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC), s.Updated())
	require.Equal(t, time.Date(2024, 1, 1, 3, 4, 5, 0, time.UTC), s.Created())
	require.Equal(t, time.Date(2024, 1, 3, 3, 4, 5, 0, time.UTC), s.Accessed())
	require.Equal(t, "short", s.Lead())
	require.Equal(t, []string{"alpha", "beta"}, s.Tags())
	links := s.Links()
	require.Len(t, links, 2)
	require.Equal(t, 1, links[0].ID)
	require.Equal(t, 2, links[1].ID)
}

func TestParseStats_ParsesLegacyTimeFormat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	raw := []byte(`updated: 2025-09-22 11:18
created: 2025-09-21 10:15
accessed: 2025-09-23 14:21
`)

	s, err := keg.ParseStats(ctx, raw)
	require.NoError(t, err)
	require.Equal(t, time.Date(2025, 9, 22, 11, 18, 0, 0, time.UTC), s.Updated())
	require.Equal(t, time.Date(2025, 9, 21, 10, 15, 0, 0, time.UTC), s.Created())
	require.Equal(t, time.Date(2025, 9, 23, 14, 21, 0, 0, time.UTC), s.Accessed())
}

func TestParseStats_IgnoresInvalidTimeValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	raw := []byte(`updated: not-a-time
created: 2024-01-01T03:04:05Z
accessed: also-not-a-time
`)

	s, err := keg.ParseStats(ctx, raw)
	require.NoError(t, err)
	require.True(t, s.Updated().IsZero())
	require.Equal(t, time.Date(2024, 1, 1, 3, 4, 5, 0, time.UTC), s.Created())
	require.True(t, s.Accessed().IsZero())
}

func TestSetHash_UpdatesUpdatedOnlyOnChange(t *testing.T) {
	t.Parallel()
	now := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)
	later := now.Add(2 * time.Hour)
	stillLater := later.Add(1 * time.Hour)

	s := keg.NewStats(now)
	require.Equal(t, now, s.Updated())

	s.SetHash("h1", &later)
	require.Equal(t, "h1", s.Hash())
	require.Equal(t, later, s.Updated())

	s.SetHash("h1", &stillLater)
	require.Equal(t, "h1", s.Hash())
	require.Equal(t, later, s.Updated())
}

func TestUpdateFromContent_UpdatesLeadHashAndLinks(t *testing.T) {
	t.Parallel()
	now := time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC)
	rt, err := toolkit.NewTestRuntime(t.TempDir(), "/home/testuser", "testuser")
	require.NoError(t, err)

	content, err := keg.ParseContent(rt, []byte("# Title\n\nhello\n\n[one](../1) [two](../2)"), keg.FormatMarkdown)
	require.NoError(t, err)

	s := keg.NewStats(now)
	s.UpdateFromContent(content, &now)

	require.Equal(t, content.Hash, s.Hash())
	require.Equal(t, content.Lead, s.Lead())
	links := s.Links()
	require.Len(t, links, 2)
	require.Equal(t, 1, links[0].ID)
	require.Equal(t, 2, links[1].ID)
}

func TestUpdateFromContent_UpdatesTagsFromFrontmatter(t *testing.T) {
	t.Parallel()
	now := time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC)
	rt, err := toolkit.NewTestRuntime(t.TempDir(), "/home/testuser", "testuser")
	require.NoError(t, err)

	content, err := keg.ParseContent(rt, []byte(`---
tags:
  - Alpha
  - beta
---
# Title

lead
`), keg.FormatMarkdown)
	require.NoError(t, err)

	s := keg.NewStats(now)
	s.SetTags([]string{"old"})
	s.UpdateFromContent(content, &now)
	require.Equal(t, []string{"alpha", "beta"}, s.Tags())
}

func TestSetAndMutateTags(t *testing.T) {
	t.Parallel()
	now := time.Date(2025, 5, 6, 7, 8, 9, 0, time.UTC)
	s := keg.NewStats(now)

	s.SetTags([]string{"Zeke", "draft", "Draft"})
	require.Equal(t, []string{"draft", "zeke"}, s.Tags())

	s.AddTag("New Tag!")
	require.Equal(t, []string{"draft", "new-tag", "zeke"}, s.Tags())

	s.RmTag("DRAFT")
	require.Equal(t, []string{"new-tag", "zeke"}, s.Tags())
}

func TestEnsureTimes_FillsZeroValues(t *testing.T) {
	t.Parallel()
	now := time.Date(2023, 3, 4, 5, 6, 7, 0, time.UTC)
	s := &keg.NodeStats{}

	s.EnsureTimes(now)
	require.Equal(t, now, s.Updated())
	require.Equal(t, now, s.Created())
	require.Equal(t, now, s.Accessed())
}
