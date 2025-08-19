package keg_test

import (
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
)

func TestBasicLinkResolver_AliasResolution(t *testing.T) {
	cfg := keg.Config{
		Links: []keg.LinkEntry{
			{Alias: "repo", URL: "git@github.com:jlrickert/tapper.git"},
		},
	}

	r := keg.NewBasicLinkResolver(nil)
	got, err := r.Resolve(cfg, "repo")
	if err != nil {
		t.Fatalf("unexpected error resolving alias: %v", err)
	}

	want := "git@github.com:jlrickert/tapper.git"
	if got != want {
		t.Fatalf("resolved URL mismatch: got=%q want=%q", got, want)
	}
}

func TestBasicLinkResolver_KegOwnerNodeResolution(t *testing.T) {
	cfg := keg.Config{
		Links: []keg.LinkEntry{
			{Alias: "jlrickert", URL: "https://keg.jlrickert.me/@jlrickert/public"},
		},
	}

	r := keg.NewBasicLinkResolver(nil)
	got, err := r.Resolve(cfg, "keg:jlrickert/123")
	if err != nil {
		t.Fatalf("unexpected error resolving owner/node token: %v", err)
	}

	want := "https://keg.jlrickert.me/@jlrickert/public/123"
	if got != want {
		t.Fatalf("resolved owner/node URL mismatch: got=%q want=%q", got, want)
	}
}
