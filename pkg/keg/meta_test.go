package keg_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	std "github.com/jlrickert/go-std/pkg"
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

	m := keg.NewMeta(ctx)
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

	m := keg.NewMeta(ctx)

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
	clock := &std.TestClock{}
	clock.Set(clock.Now().Add(5 * time.Hour))
	ctx := std.WithClock(context.Background(), clock)

	m := keg.NewMeta(ctx)

	// Initially zero times
	require.True(t,
		m.Updated().Equal(clock.Now()) && m.Created().Equal(clock.Now()) && m.Accessed().IsZero(),
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

	m := keg.NewMeta(ctx)
	// Set tags as a slice; Meta.Set supports []string
	require.NoError(t, m.Set(ctx, "tags", []string{"A B", "a-b", "C,c"}))

	out := m.ToYAML()
	// Ensure normalized tokens appear (lowercase, hyphenized) and deduped.
	lout := strings.ToLower(out)
	require.Contains(t, lout, "a-b", "expected normalized tag a-b in YAML")
	require.Contains(t, lout, "c", "expected normalized tag c in YAML")

	// Also ensure YAML serializes tags section
	require.True(t,
		bytes.Contains([]byte(out), []byte("tags:")) || strings.Contains(out, "tags:"),
		"expected tags section in YAML output",
	)
}
