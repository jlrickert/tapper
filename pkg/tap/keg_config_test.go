package tap_test

import (
	"os"
	"testing"

	"github.com/jlrickert/tapper/pkg/tap"
)

func TestParseConfigDataV1(t *testing.T) {
	v1Yaml := `
kegv: "2023-01"
updated: "2023-01-01"
title: "Test KEG V1"
url: "https://example.com"
creator: "creator-id"
state: "living"
summary: "This is a test KEG V1 config"
indexes:
  - file: "index1.md"
    summary: "Index 1 summary"
  - file: "index2.md"
    summary: "Index 2 summary"
`

	config, err := tap.ParseConfigData([]byte(v1Yaml))
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
    url: "https://home.example.com"
  - alias: "docs"
    url: "https://docs.example.com"
indexes:
  - file: "index1.md"
    summary: "Index 1 summary"
`

	config, err := tap.ParseConfigData([]byte(v2Yaml))
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
		t.Errorf("Expected 2 links, got %d", len(config.Links))
	}
	if len(config.Indexes) != 1 {
		t.Errorf("Expected 1 index, got %d", len(config.Indexes))
	}
}

func TestExpandEnv(t *testing.T) {
	os.Setenv("TEST_TITLE", "EnvTitle")
	os.Setenv("TEST_URL", "https://env.url")

	config := tap.KegConfig{
		Kegv:    tap.ConfigV2VersionString,
		Title:   "${TEST_TITLE}",
		URL:     "${TEST_URL}",
		Creator: "creator",
		State:   "living",
		Summary: "summary",
		Links: []tap.LinkEntry{
			{Alias: "alias1", URL: "${TEST_URL}"},
		},
		Indexes: []tap.IndexEntry{
			{File: "file1.md", Summary: "summary1"},
		},
	}

	config.ExpandEnv()

	if config.Title != "EnvTitle" {
		t.Errorf("Expected Title to be 'EnvTitle', got %s", config.Title)
	}
	if config.URL != "https://env.url" {
		t.Errorf("Expected URL to be 'https://env.url', got %s", config.URL)
	}
	if config.Links[0].URL != "https://env.url" {
		t.Errorf("Expected Links[0].URL to be 'https://env.url', got %s", config.Links[0].URL)
	}
}

func TestExpandEnvPartial(t *testing.T) {
	os.Setenv("PARTIAL", "partial_value")

	config := tap.KegConfig{
		Kegv:  tap.ConfigV2VersionString,
		Title: "Title with ${PARTIAL}",
	}

	config.ExpandEnv()

	if config.Title != "Title with partial_value" {
		t.Errorf("Expected Title to be 'Title with partial_value', got %s", config.Title)
	}
}

func TestParseConfigDataInvalidVersion(t *testing.T) {
	invalidYaml := `
kegv: "invalid-version"
title: "Invalid version test"
`

	_, err := tap.ParseConfigData([]byte(invalidYaml))
	if err == nil {
		t.Fatal("Expected error for unsupported config version, got nil")
	}
}

func TestParseConfigDataMissingVersion(t *testing.T) {
	missingVersionYaml := `
title: "Missing version test"
`

	_, err := tap.ParseConfigData([]byte(missingVersionYaml))
	if err == nil {
		t.Fatal("Expected error for missing version field, got nil")
	}
}
