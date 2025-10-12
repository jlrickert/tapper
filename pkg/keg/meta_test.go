package keg_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jlrickert/go-std/testutils"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestParseMeta_EmptyReturnsEmptyMeta(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	m, err := keg.ParseMeta(ctx, []byte("   \n\t"))
	t.Log(m)
	require.NoError(t, err, "ParseMeta should not error for empty input")
	require.NotNil(t, m, "expected non-nil Meta for empty input")

	// Comment-preserving parsing is out of scope. Just ensure the meta is empty
	// (no tags, no time fields set).
	require.Empty(t, m.Tags(), "expected no tags for empty meta")
	_, ok := m.Get("updated")
	require.False(t, ok, "expected no updated field for empty meta")
}

func TestTags_Normalization_AddRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	m := keg.NewMeta(ctx, time.Now())
	// Set tags as a single string (comma separated)
	err := m.Set(ctx, "tags", "Zeke, Draft  ,  other_tag")
	require.NoError(t, err)

	// Read normalized tags from the method
	tags := m.Tags()

	want := []string{"draft", "other_tag", "zeke"}
	require.Equal(t, want, tags, "initial normalized tags")

	// Add an existing (case-differs) tag -> no duplicate, normalized.
	m.AddTag("ZEKE")
	tags = m.Tags()
	require.Equal(t, want, tags, "after adding duplicate tag should remain deduped")

	// Add a new tag.
	m.AddTag("New Tag!")
	tags = m.Tags()
	want2 := []string{"draft", "new-tag", "other_tag", "zeke"}
	require.Equal(t, want2, tags, "after adding new tag")

	// Remove tag (case-insensitive normalized)
	m.RmTag("other_TAG")
	tags = m.Tags()
	want3 := []string{"draft", "new-tag", "zeke"}
	require.Equal(t, want3, tags, "after removing tag")

	// Remove a tag not present -> no-op (should not panic or error)
	m.RmTag("nonexistent")
	// still same
	tags = m.Tags()
	require.Equal(t, want3, tags, "removing nonexistent tag is no-op")
}

func TestSetGetAndUnsetSimpleKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	m := keg.NewMeta(ctx, time.Now())

	// Use a supported simple key ("hash") rather than arbitrary keys.
	require.NoError(t, m.Set(ctx, "hash", "a"))

	v, ok := m.Get("hash")
	require.True(t, ok, "expected value to be present")
	require.Equal(t, "a", v)

	// Unset key by setting nil
	require.NoError(t, m.Set(ctx, "hash", nil))
	_, ok = m.Get("hash")
	require.False(t, ok, "expected key to be removed after setting nil")
}

func TestTimeFields_SetAndRead(t *testing.T) {
	t.Parallel()
	initial := time.Now()
	// Use Fixture to provide clock in context set to initial + 5h.
	f := NewFixture(t, testutils.WithClock(initial.Add(5*time.Hour)))
	ctx := f.Context()

	m := keg.NewMeta(ctx, f.Now())

	// Initially zero times
	require.True(t,
		m.Updated().Equal(f.Now()) &&
			m.Created().Equal(f.Now()) &&
			m.Accessed().IsZero(),
		"expected zero times for new meta",
	)

	// Set specific times via Set (meta.Set supports time.Time for time fields)
	created := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2021, 6, 7, 8, 9, 10, 0, time.UTC)
	accessed := time.Date(2022, 11, 12, 13, 14, 15, 0, time.UTC)

	require.NoError(t, m.Set(ctx, "created", created))
	require.NoError(t, m.Set(ctx, "updated", updated))
	require.NoError(t, m.Set(ctx, "accessed", accessed))

	require.True(t, m.Created().Equal(created), "Created mismatch")
	require.True(t, m.Updated().Equal(updated), "Updated mismatch")
	require.True(t, m.Accessed().Equal(accessed), "Accessed mismatch")
}

func TestToYAML_NormalizesTagsOutput(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	m := keg.NewMeta(ctx, time.Now())
	// Set tags as a slice; Meta.Set supports []string
	require.NoError(t, m.Set(ctx, "tags", []string{"A B", "a-b", "C,c"}))

	out := m.ToYAML()
	// Ensure normalized tokens appear (lowercase, hyphenized) and deduped.
	lout := strings.ToLower(out)
	require.Contains(t, lout, "a-b", "expected normalized tag a-b in YAML")
	require.Contains(t, lout, "c", "expected normalized tag c in YAML")

	// Also ensure YAML serializes tags section
	require.True(t,
		bytes.Contains([]byte(out), []byte("tags:")) ||
			strings.Contains(out, "tags:"),
		"expected tags section in YAML output",
	)
}

// New tests for titles and hashes

func TestParseMeta_TitleAndHash(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	raw := []byte("title: My Fancy Title\nhash: abc123\n")
	m, err := keg.ParseMeta(ctx, raw)
	require.NoError(t, err)
	require.NotNil(t, m)

	require.Equal(t, "My Fancy Title", m.Title(), "parsed title should match")
	require.Equal(t, "abc123", m.Hash(), "parsed hash should match")

	// Ensure ToYAML preserves the title and hash when the original node is present
	out := m.ToYAML()
	low := strings.ToLower(out)
	require.Contains(t, low, "title:", "expected title in YAML output")
	require.Contains(t, low, "my fancy title", "expected title value in YAML output")
	require.Contains(t, low, "hash:", "expected hash in YAML output")
	require.Contains(t, low, "abc123", "expected hash value in YAML output")
}

func TestSetHash_UpdatesUpdatedTimeWhenChanged(t *testing.T) {
	t.Parallel()

	// Use a deterministically controllable clock via Fixture
	initial := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)
	f := NewFixture(t, testutils.WithClock(initial))
	ctx := f.Context()

	m := keg.NewMeta(ctx, f.Now())
	// initial updated should match clock
	require.True(t, m.Updated().Equal(f.Now()),
		"initial updated should equal clock now")

	// Advance clock to simulate later time, then set hash with a supplied time
	f.Advance(2 * time.Hour)
	later := f.Now()

	// ensure hash changes and passing now causes updated timestamp to be set
	m.SetHash(ctx, "hash-v1", &later)
	require.Equal(t, "hash-v1", m.Hash(), "hash should be set")
	require.True(t, m.Updated().Equal(later),
		"updated timestamp should reflect clock now after SetHash")

	// Advance clock again and set same hash; updated should not change
	f.Advance(1 * time.Hour)
	current := f.Now()
	// setting the same hash again should not mutate Updated
	m.SetHash(ctx, "hash-v1", &current) // same hash
	require.Equal(t, "hash-v1", m.Hash(), "hash should remain unchanged")
	// updated should remain the previous time, not current
	require.False(t, m.Updated().Equal(current),
		"updated should not change when setting same hash")
}

func TestSetHash_IdempotentViaSetDoesNotUpdateTime(t *testing.T) {
	t.Parallel()

	initial := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)
	f := NewFixture(t, testutils.WithClock(initial))
	ctx := f.Context()

	m := keg.NewMeta(ctx, f.Now())

	// Use Set (not SetHash) to change hash; Set does not modify timestamps.
	require.NoError(t, m.Set(ctx, "hash", "initial"))
	initialUpdated := m.Updated()

	f.Advance(24 * time.Hour)
	// Change hash via Set again
	require.NoError(t, m.Set(ctx, "hash", "updated-via-set"))
	require.Equal(t, "updated-via-set", m.Hash(), "hash should update via Set")
	// Updated timestamp should be unchanged because Set does not update timestamps
	require.True(t, m.Updated().Equal(initialUpdated),
		"Updated should not change when using Set for hash")
}

// Tests for comment preservation in ParseMeta and ToYAML

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
	require.NotNil(t, m)

	out := m.ToYAML()
	// When ParseMeta parsed a document node, ToYAML should emit the original
	// comments present in the node when encoding that node.
	require.Contains(t, out, "keep-this-comment",
		"expected leading comment to be preserved")
	require.Contains(t, out, "preserve me",
		"expected inline comment to be preserved")
	// Also ensure the key values remain present
	require.Contains(t, out, "Title With Comment",
		"expected title value to be preserved")
	require.Contains(t, out, "comment-hash",
		"expected hash value to be preserved")
}

func TestToYAML_NodeAbsent_DoesNotContainOriginalComments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Create a meta programmatically (node should be nil) and ensure that ToYAML
	// does not accidentally include unrelated comment text.
	m := keg.NewMeta(ctx, time.Now())
	m.SetTitle(ctx, "NoComments")
	require.NoError(t, m.Set(ctx, "hash", "h1"))
	require.NoError(t, m.Set(ctx, "tags", []string{"t1"}))

	out := m.ToYAML()
	// The string used in the comment test should not be present when the meta was
	// not parsed from a node containing that comment.
	require.NotContains(t, out, "keep-this-comment",
		"expected no preserved comments for programmatic meta")
}
