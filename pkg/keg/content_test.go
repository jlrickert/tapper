package keg

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestParseMeta_Empty(t *testing.T) {
	_, err := ParseMeta([]byte("   \n\t"))
	if err == nil {
		t.Fatalf("expected error for empty meta, got nil")
	}
	if !errors.Is(err, ErrMetaNotFound) {
		t.Fatalf("expected ErrMetaNotFound, got: %v", err)
	}
}

func TestMeta_TagsHandling(t *testing.T) {
	// tags provided as a single string with commas/spaces
	yaml1 := []byte(`updated: 2025-08-04T22:03:53Z
tags: "Zeke, Draft"
`)

	m, err := ParseMeta(yaml1)
	if err != nil {
		t.Fatalf("ParseMeta failed: %v", err)
	}

	want := []string{"draft", "zeke"}
	got := m.Tags()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Tags() = %v; want %v", got, want)
	}

	// AddTag should be idempotent and normalized
	if err := m.AddTag("New Tag"); err != nil {
		t.Fatalf("AddTag failed: %v", err)
	}
	if err := m.AddTag("new-tag"); err != nil {
		t.Fatalf("AddTag failed second time: %v", err)
	}
	expectedSet := []string{"draft", "new-tag", "zeke"}
	if !reflect.DeepEqual(m.Tags(), expectedSet) {
		t.Fatalf("after AddTag Tags() = %v; want %v", m.Tags(), expectedSet)
	}

	// RemoveTag should remove and normalize
	if err := m.RemoveTag("draft"); err != nil {
		t.Fatalf("RemoveTag failed: %v", err)
	}
	expectedAfterRemove := []string{"new-tag", "zeke"}
	if !reflect.DeepEqual(m.Tags(), expectedAfterRemove) {
		t.Fatalf("after RemoveTag Tags() = %v; want %v", m.Tags(), expectedAfterRemove)
	}
}

func TestMeta_ToYAML_PreserveUnmodified(t *testing.T) {
	// Include a comment to ensure verbatim YAML is preserved when unmodified.
	orig := []byte(`# my comment
updated: 2025-08-04 22:03:53Z
title: Example
tags:
  - Zeke
  - draft
`)

	m, err := ParseMeta(orig)
	if err != nil {
		t.Fatalf("ParseMeta failed: %v", err)
	}

	// Should be unmodified initially; ToYAML should return the original bytes verbatim.
	out, err := m.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML failed: %v", err)
	}
	if !bytes.Equal(out, orig) {
		t.Fatalf("ToYAML did not preserve original bytes for unmodified YAML\ngot:\n%s\nwant:\n%s", out, orig)
	}

	// Mutate (AddTag) and then ToYAML should produce a canonical YAML (not verbatim).
	if err := m.AddTag("added"); err != nil {
		t.Fatalf("AddTag failed: %v", err)
	}
	out2, err := m.ToYAML()
	if err != nil {
		t.Fatalf("ToYAML after modification failed: %v", err)
	}
	if bytes.Equal(out2, orig) {
		t.Fatalf("ToYAML should not equal original after modification")
	}
	// Ensure updated tags are present in the produced YAML bytes.
	if !bytes.Contains(out2, []byte("added")) {
		t.Fatalf("serialized YAML missing added tag: %s", out2)
	}
}

func TestMeta_JSON_VerbatimRoundtrip(t *testing.T) {
	jsonOrig := []byte(`{"updated":"2025-08-04T22:03:53Z","tags":"Zeke, Draft"}`)
	m, err := ParseMeta(jsonOrig)
	if err != nil {
		t.Fatalf("ParseMeta(json) failed: %v", err)
	}
	out, err := m.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}
	if !bytes.Equal(bytes.TrimSpace(out), bytes.TrimSpace(jsonOrig)) {
		t.Fatalf("ToJSON did not preserve original JSON bytes for unmodified meta\n got: %s\nwant: %s", out, jsonOrig)
	}
}

func TestMeta_UpdatedParsingAndSetters(t *testing.T) {
	yaml := []byte(`updated: 2025-08-04 22:03:53Z
`)
	m, err := ParseMeta(yaml)
	if err != nil {
		t.Fatalf("ParseMeta failed: %v", err)
	}
	tm := m.GetUpdated()
	if tm.IsZero() {
		t.Fatalf("GetUpdated returned zero time for valid timestamp")
	}
	want, _ := time.Parse("2006-01-02 15:04:05Z", "2025-08-04 22:03:53Z")
	if !tm.Equal(want) {
		t.Fatalf("GetUpdated = %v; want %v", tm, want)
	}

	// Test SetUpdated writes RFC3339 and GetUpdated returns it back.
	now := time.Date(2025, 8, 12, 5, 30, 0, 0, time.UTC)
	m.SetUpdated(now)
	got := m.GetUpdated()
	if !got.Equal(now) {
		t.Fatalf("after SetUpdated GetUpdated = %v; want %v", got, now)
	}
}
