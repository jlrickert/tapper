package keg_test

import (
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
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

	config, err := keg.ParseKegConfig([]byte(v1Yaml))
	require.NoError(t, err, "ParseKegConfig failed")

	require.Equal(t, keg.ConfigV2VersionString, config.Kegv)
	require.Equal(t, "Test KEG V1", config.Title)
	require.Len(t, config.Indexes, 2)
	require.Equal(t, "index1.md", config.Indexes[0].File)
	require.Equal(t, "index2.md", config.Indexes[1].File)
	require.Empty(t, config.Links)
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
    url: "https://keg.example.com/@user/home"
  - alias: "docs"
    url: "https://keg.example.com/@user/docs"
indexes:
  - file: "index1.md"
    summary: "Index 1 summary"
entities:
  entity:
    id: 2045
    summary: "Defines required contents and conventions for all entity notes."
  gear:
    id: 2044
    summary: "Canonical structure for gear/equipment notes."
tags:
  entity: "Canonical notes that define structure rules for entity types"
  client: "Client of ECW"
`

	config, err := keg.ParseKegConfig([]byte(v2Yaml))
	require.NoError(t, err, "ParseKegConfig failed")

	require.Equal(t, keg.ConfigV2VersionString, config.Kegv)
	require.Equal(t, "Test KEG V2", config.Title)

	require.Len(t, config.Links, 2, "expected 2 links")
	links := map[string]string{}
	for _, l := range config.Links {
		links[l.Alias] = l.URL
	}
	require.Contains(t, links, "home")
	require.Contains(t, links, "docs")
	require.Equal(t, "https://keg.example.com/@user/home", links["home"])
	require.Equal(t, "https://keg.example.com/@user/docs", links["docs"])

	require.Len(t, config.Indexes, 1)
	require.Equal(t, "index1.md", config.Indexes[0].File)

	require.Len(t, config.Entities, 2)
	require.Equal(t, 2045, config.Entities["entity"].ID)
	require.Equal(t, "Defines required contents and conventions for all entity notes.", config.Entities["entity"].Summary)
	require.Equal(t, 2044, config.Entities["gear"].ID)
	require.Equal(t, "Canonical structure for gear/equipment notes.", config.Entities["gear"].Summary)

	require.Len(t, config.Tags, 2)
	require.Equal(t, "Canonical notes that define structure rules for entity types", config.Tags["entity"])
	require.Equal(t, "Client of ECW", config.Tags["client"])
}

func TestParseConfigDataInvalidVersion(t *testing.T) {
	invalidYaml := `
kegv: "invalid-version"
title: "Invalid version test"
`

	_, err := keg.ParseKegConfig([]byte(invalidYaml))
	require.Error(t, err, "expected error for unsupported config version")
	require.Contains(t, err.Error(), "unsupported config version")
}

func TestParseConfigDataMissingVersion(t *testing.T) {
	missingVersionYaml := `
title: "Missing version test"
`

	_, err := keg.ParseKegConfig([]byte(missingVersionYaml))
	require.Error(t, err, "expected error for missing version field")
	require.Contains(t, err.Error(), "missing or invalid kegv")
}

func TestAddEntity_AddsAndUpdates(t *testing.T) {
	cfg := &keg.Config{}

	err := cfg.AddEntity("entity", 2045, "original")
	require.NoError(t, err)
	require.Len(t, cfg.Entities, 1)
	require.Equal(t, 2045, cfg.Entities["entity"].ID)
	require.Equal(t, "original", cfg.Entities["entity"].Summary)

	err = cfg.AddEntity("entity", 2046, "updated")
	require.NoError(t, err)
	require.Len(t, cfg.Entities, 1)
	require.Equal(t, 2046, cfg.Entities["entity"].ID)
	require.Equal(t, "updated", cfg.Entities["entity"].Summary)
}

func TestAddEntity_ValidatesRequiredFields(t *testing.T) {
	cfg := &keg.Config{}

	var nilCfg *keg.Config
	err := nilCfg.AddEntity("entity", 1, "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "config is nil")

	err = cfg.AddEntity("", 1, "summary")
	require.Error(t, err)
	require.Contains(t, err.Error(), "entity name is required")

	err = cfg.AddEntity("entity", 0, "summary")
	require.Error(t, err)
	require.Contains(t, err.Error(), "entity id must be greater than zero")
}

func TestAddTag_AddsAndUpdates(t *testing.T) {
	cfg := &keg.Config{}

	err := cfg.AddTag("entity", "original")
	require.NoError(t, err)
	require.Len(t, cfg.Tags, 1)
	require.Equal(t, "original", cfg.Tags["entity"])

	err = cfg.AddTag("entity", "updated")
	require.NoError(t, err)
	require.Len(t, cfg.Tags, 1)
	require.Equal(t, "updated", cfg.Tags["entity"])
}

func TestAddTag_ValidatesRequiredFields(t *testing.T) {
	cfg := &keg.Config{}

	var nilCfg *keg.Config
	err := nilCfg.AddTag("x", "x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "config is nil")

	err = cfg.AddTag("", "summary")
	require.Error(t, err)
	require.Contains(t, err.Error(), "tag name is required")

	err = cfg.AddTag("tag", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "tag summary is required")
}

func TestConfigToYAML_PrependsSchemaModeline(t *testing.T) {
	cfg := keg.NewConfig()
	out, err := cfg.ToYAML()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(out), "# yaml-language-server: $schema="+keg.KegConfigSchemaURL+"\n"))
}
