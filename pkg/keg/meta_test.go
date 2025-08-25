package keg_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
)

func TestParseMeta_EmptyReturnsEmptyMeta(t *testing.T) {
	t.Parallel()
	m, err := keg.ParseMeta([]byte("   \n\t"), deps)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if m == nil {
		t.Fatalf("expected non-nil Meta for empty input")
	}
	out, err := m.ToBytes()
	if err != nil {
		t.Fatalf("ToBytes error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty bytes for empty meta, got %q", out)
	}
}

func TestToYAML_PreservesRawWhenUnmodifiedAndEmitsWhenModified(t *testing.T) {
	t.Parallel()
	orig := []byte(`# a comment
updated: 2025-08-11T00:00:00Z
title: Example
tags:
  - One
  - two
`)
	m, err := keg.ParseMeta(orig, deps)
	if err != nil {
		t.Fatalf("ParseMeta error: %v", err)
	}

	// When unmodified, ToYAML should return the original bytes verbatim.
	out, err := m.ToBytes()
	if err != nil {
		t.Fatalf("ToYAML error: %v", err)
	}
	if !bytes.Equal(out, orig) {
		t.Fatalf("expected preserved raw yaml, got different output\norig:\n%s\nout:\n%s", orig, out)
	}

	// Mutate (AddTag) and then ToYAML should emit canonicalized YAML (not equal to original).
	if err := m.AddTag("New"); err != nil {
		t.Fatalf("AddTag error: %v", err)
	}
	out2, err := m.ToBytes()
	if err != nil {
		t.Fatalf("ToYAML error after mutation: %v", err)
	}
	if bytes.Equal(out2, orig) {
		t.Fatalf("expected ToYAML to change after mutation, but output equals original")
	}
	// Ensure the new tag appears normalized in output.
	if !strings.Contains(string(out2), "new") {
		t.Fatalf("expected new normalized tag present in output: %s", out2)
	}
}

func TestTags_Normalization_AddRemove(t *testing.T) {
	t.Parallel()
	// Case: tags as a single string with comma and spaces.
	m := keg.NewMetaFromRaw(map[string]any{
		"tags": "Zeke, Draft  ,  other_tag",
	}, deps)
	tags := m.Tags()
	want := []string{"draft", "other_tag", "zeke"}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("Tags() = %v; want %v", tags, want)
	}

	// Add an existing (case-differs) tag -> no duplicate, normalized.
	if err := m.AddTag("ZEKE"); err != nil {
		t.Fatalf("AddTag error: %v", err)
	}
	tags = m.Tags()
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("after AddTag duplicate Tags() = %v; want %v", tags, want)
	}

	// Add a new tag.
	if err := m.AddTag("New Tag!"); err != nil {
		t.Fatalf("AddTag error: %v", err)
	}
	tags = m.Tags()
	want2 := []string{"draft", "new-tag", "other_tag", "zeke"}
	if !reflect.DeepEqual(tags, want2) {
		t.Fatalf("after AddTag new Tags() = %v; want %v", tags, want2)
	}

	// Remove tag (case-insensitive normalized)
	if err := m.RemoveTag("other_TAG"); err != nil {
		t.Fatalf("RemoveTag error: %v", err)
	}
	tags = m.Tags()
	want3 := []string{"draft", "new-tag", "zeke"}
	if !reflect.DeepEqual(tags, want3) {
		t.Fatalf("after RemoveTag Tags() = %v; want %v", tags, want3)
	}

	// Remove a tag not present -> no-op
	if err := m.RemoveTag("nonexistent"); err != nil {
		t.Fatalf("RemoveTag(nonexistent) should be no-op, got error: %v", err)
	}
}

func TestSetGetDeleteAndIntermediateTypeError(t *testing.T) {
	t.Parallel()
	m := keg.NewMetaFromRaw(nil, deps)

	// Set nested value
	if err := m.Set("v", "a", "b", "c"); err != nil {
		t.Fatalf("Set nested error: %v", err)
	}
	v, ok := m.Get("a", "b", "c")
	if !ok {
		t.Fatalf("Get did not find value at path")
	}
	if s, ok := v.(string); !ok || s != "v" {
		t.Fatalf("Get returned %v (type ok=%v); want %q", v, ok, "v")
	}

	// Delete the nested final key
	if err := m.Delete("a", "b", "c"); err != nil {
		t.Fatalf("Delete error: %v", err)
	}
	_, ok = m.Get("a", "b", "c")
	if ok {
		t.Fatalf("expected key to be deleted")
	}

	// If intermediate path exists but is not a map, Set should error.
	if err := m.Set("x", "a"); err != nil {
		t.Fatalf("Set top-level a error: %v", err)
	}
	// Now a is "x" (string). Setting a.b should return an error.
	if err := m.Set("y", "a", "b"); err == nil {
		t.Fatalf("expected error when setting nested under non-map intermediate, got nil")
	}
}

func TestTimeFieldsAndTouchAndGetStats(t *testing.T) {
	t.Parallel()
	m := keg.NewMetaFromRaw(nil, deps)

	// Initially zero times
	if !m.GetUpdated().IsZero() || !m.GetCreated().IsZero() || !m.GetAccessed().IsZero() {
		t.Fatalf("expected zero times for new meta")
	}

	// Set specific times
	created := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	updated := time.Date(2021, 6, 7, 8, 9, 10, 0, time.UTC)
	accessed := time.Date(2022, 11, 12, 13, 14, 15, 0, time.UTC)

	m.SetCreated(created)
	m.SetUpdated(updated)
	m.SetAccessed(accessed)

	if got := m.GetCreated(); !got.Equal(created) {
		t.Fatalf("GetCreated = %v; want %v", got, created)
	}
	if got := m.GetUpdated(); !got.Equal(updated) {
		t.Fatalf("GetUpdated = %v; want %v", got, updated)
	}
	if got := m.GetAccessed(); !got.Equal(accessed) {
		t.Fatalf("GetAccessed = %v; want %v", got, accessed)
	}

	stats := m.GetStats()
	if !stats.Created.Equal(created) || !stats.Updated.Equal(updated) || !stats.Access.Equal(accessed) {
		t.Fatalf("GetStats = %+v; want birth=%v updated=%v access=%v", stats, created, updated, accessed)
	}

	// Touch should set missing times and bump accessed. Use fresh Meta.
	m2 := keg.NewMetaFromRaw(nil, deps)
	// Sleep not required; Touch uses time.Now but we will just assert non-zero.
	m2.Touch()
	if m2.GetCreated().IsZero() || m2.GetUpdated().IsZero() || m2.GetAccessed().IsZero() {
		t.Fatalf("Touch did not set timestamps: created=%v updated=%v access=%v", m2.GetCreated(), m2.GetUpdated(), m2.GetAccessed())
	}
}

func TestToJSON_NormalizesTags(t *testing.T) {
	t.Parallel()
	m := keg.NewMetaFromRaw(map[string]any{
		"tags": []any{"A B", "a-b", "C,c"},
	}, deps)
	jb, err := m.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON error: %v", err)
	}
	out := string(jb)
	// Ensure normalized tokens appear (lowercase, hyphenized) and deduped.
	if !strings.Contains(out, "a-b") {
		t.Fatalf("expected normalized tag a-b in JSON: %s", out)
	}
	if !strings.Contains(out, "c") {
		t.Fatalf("expected normalized tag c in JSON: %s", out)
	}
}
