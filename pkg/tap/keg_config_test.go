package tap_test

import (
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/tap"
)

func TestParseConfigDataV1(t *testing.T) {
	v1Yaml := `
kegv: "2023-01"
updated: "2023-01-01"
title: "Test KEG V1"
url: "https://example.com"
creator: "Jared Rickert"
state: "living"
summary: "This is a test KEG V1 config"
indexes:
  - file: "index1.md"
    summary: "Index 1 summary"
  - file: "index2.md"
    summary: "Index 2 summary"
`

	config, err := tap.ParseKegConfig([]byte(v1Yaml))
	if err != nil {
		t.Fatalf("ParseConfigData failed: %v", err)
	}

	if config.Kegv != tap.ConfigV2VersionString {
		t.Errorf("Expected Kegv to be %s, got %s", tap.ConfigV2VersionString, config.Kegv)
	}
	if config.Title != "Test KEG V1" {
		t.Errorf("Expected Title to be 'Test KEG V1', got %s", config.Title)
	}
	if len(config.Indexes) != 2 {
		t.Errorf("Expected 2 indexes, got %d", len(config.Indexes))
	} else {
		if config.Indexes[0].File != "index1.md" {
			t.Errorf("expected first index file 'index1.md', got %q", config.Indexes[0].File)
		}
		if config.Indexes[1].File != "index2.md" {
			t.Errorf("expected second index file 'index2.md', got %q", config.Indexes[1].File)
		}
	}
	if len(config.Links) != 0 {
		t.Errorf("Expected Links to be nil or empty, got %v", config.Links)
	}
}

func TestParseConfigDataV2(t *testing.T) {
	v2Yaml := `
kegv: "2025-07"
updated: "2025-07-01"
title: "Test KEG V2"
url: "https://example.com/v2"
creator: "creator-v2"
state: "archived"
summary: "This is a test KEG V2 config"
links:
  - alias: "home"
    url:
      type: "https"
      link: "keg.example.com/@user/home"
  - alias: "docs"
    url:
      type: "https"
      link: "keg.example.com/@user/docs"
indexes:
  - file: "index1.md"
    summary: "Index 1 summary"
`

	config, err := tap.ParseKegConfig([]byte(v2Yaml))
	if err != nil {
		t.Fatalf("ParseConfigData failed: %v", err)
	}

	if config.Kegv != tap.ConfigV2VersionString {
		t.Errorf("Expected Kegv to be %s, got %s", tap.ConfigV2VersionString, config.Kegv)
	}
	if config.Title != "Test KEG V2" {
		t.Errorf("Expected Title to be 'Test KEG V2', got %s", config.Title)
	}
	if len(config.Links) != 2 {
		t.Fatalf("Expected 2 links, got %d", len(config.Links))
	}
	// Verify parsed link entries
	foundHome := false
	foundDocs := false
	for _, l := range config.Links {
		if l.Alias == "home" {
			foundHome = true
			if l.URL.Value != "keg.example.com/@user/home" {
				t.Errorf("home link URL mismatch: expected %q, got %q", "keg.example.com/@user/home", l.URL.Value)
			}
		}
		if l.Alias == "docs" {
			foundDocs = true
			if l.URL.Value != "keg.example.com/@user/docs" {
				t.Errorf("docs link URL mismatch: expected %q, got %q", "keg.example.com/@user/docs", l.URL.Value)
			}
		}
	}
	if !foundHome {
		t.Error("expected to find link with alias 'home'")
	}
	if !foundDocs {
		t.Error("expected to find link with alias 'docs'")
	}

	if len(config.Indexes) != 1 {
		t.Errorf("Expected 1 index, got %d", len(config.Indexes))
	} else if config.Indexes[0].File != "index1.md" {
		t.Errorf("expected index file 'index1.md', got %q", config.Indexes[0].File)
	}
}

func TestParseConfigDataInvalidVersion(t *testing.T) {
	invalidYaml := `
kegv: "invalid-version"
title: "Invalid version test"
`

	_, err := tap.ParseKegConfig([]byte(invalidYaml))
	if err == nil {
		t.Fatal("Expected error for unsupported config version, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported config version") {
		t.Fatalf("unexpected error for invalid version: %v", err)
	}
}

func TestParseConfigDataMissingVersion(t *testing.T) {
	missingVersionYaml := `
title: "Missing version test"
`

	_, err := tap.ParseKegConfig([]byte(missingVersionYaml))
	if err == nil {
		t.Fatal("Expected error for missing version field, got nil")
	}
	if !strings.Contains(err.Error(), "missing or invalid kegv") {
		t.Fatalf("unexpected error for missing version: %v", err)
	}
}
