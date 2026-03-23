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
  client: "Client of work"
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
	require.Equal(t, "Client of work", config.Tags["client"])
}

func TestParseConfigV2_DoctorNilWhenAbsent(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "No doctor config"
`
	config, err := keg.ParseKegConfig([]byte(yamlData))
	require.NoError(t, err)
	require.Nil(t, config.Doctor, "Doctor should be nil when omitted from config")
}

func TestParseConfigV2_DoctorEntityCheckEnabled(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "With doctor config"
doctor:
  entityCheck: true
`
	config, err := keg.ParseKegConfig([]byte(yamlData))
	require.NoError(t, err)
	require.NotNil(t, config.Doctor, "Doctor should be present")
	require.True(t, config.Doctor.EntityCheck, "EntityCheck should be true")
}

func TestParseConfigV2_DoctorEntityCheckDisabled(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "With doctor config disabled"
doctor:
  entityCheck: false
`
	config, err := keg.ParseKegConfig([]byte(yamlData))
	require.NoError(t, err)
	require.NotNil(t, config.Doctor, "Doctor should be present when explicitly set")
	require.False(t, config.Doctor.EntityCheck, "EntityCheck should be false")
}

func TestParseConfigV2_DoctorTagCheckEnabled(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "With doctor tag check"
doctor:
  tagCheck: true
`
	config, err := keg.ParseKegConfig([]byte(yamlData))
	require.NoError(t, err)
	require.NotNil(t, config.Doctor, "Doctor should be present")
	require.True(t, config.Doctor.TagCheck, "TagCheck should be true")
}

func TestParseConfigV2_DoctorTagCheckDisabled(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "With doctor tag check disabled"
doctor:
  tagCheck: false
`
	config, err := keg.ParseKegConfig([]byte(yamlData))
	require.NoError(t, err)
	require.NotNil(t, config.Doctor, "Doctor should be present when explicitly set")
	require.False(t, config.Doctor.TagCheck, "TagCheck should be false")
}

func TestParseConfigV2_DoctorBothChecks(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "With both doctor checks"
doctor:
  entityCheck: true
  tagCheck: true
`
	config, err := keg.ParseKegConfig([]byte(yamlData))
	require.NoError(t, err)
	require.NotNil(t, config.Doctor, "Doctor should be present")
	require.True(t, config.Doctor.EntityCheck, "EntityCheck should be true")
	require.True(t, config.Doctor.TagCheck, "TagCheck should be true")
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

func TestIndexEntry_QueryOrTags_PrefersQuery(t *testing.T) {
	entry := keg.IndexEntry{
		File:    "dex/custom.md",
		Summary: "custom index",
		Query:   "entity=concept",
		Tags:    "golang",
	}
	require.Equal(t, "entity=concept", entry.QueryOrTags(), "Query should take precedence over Tags")
}

func TestIndexEntry_QueryOrTags_FallsBackToTags(t *testing.T) {
	entry := keg.IndexEntry{
		File:    "dex/custom.md",
		Summary: "custom index",
		Tags:    "golang",
	}
	require.Equal(t, "golang", entry.QueryOrTags(), "should fall back to Tags when Query is empty")
}

func TestIndexEntry_QueryOrTags_EmptyWhenBothEmpty(t *testing.T) {
	entry := keg.IndexEntry{
		File:    "dex/changes.md",
		Summary: "core index",
	}
	require.Equal(t, "", entry.QueryOrTags(), "should return empty when both Query and Tags are empty")
}

func TestParseConfigV2_IndexQueryField(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "Test query field"
indexes:
  - file: "dex/concepts.md"
    summary: "concept nodes"
    query: "entity=concept"
`
	config, err := keg.ParseKegConfig([]byte(yamlData))
	require.NoError(t, err)
	require.Len(t, config.Indexes, 1)
	require.Equal(t, "entity=concept", config.Indexes[0].Query)
	require.Empty(t, config.Indexes[0].Tags, "Tags should be empty when only query is set")
}

func TestParseConfigV2_IndexTagsFieldBackwardCompat(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "Test tags backward compat"
indexes:
  - file: "dex/golang.md"
    summary: "Go nodes"
    tags: "golang"
`
	config, err := keg.ParseKegConfig([]byte(yamlData))
	require.NoError(t, err)
	require.Len(t, config.Indexes, 1)
	require.Empty(t, config.Indexes[0].Query, "Query should be empty when only tags is set")
	require.Equal(t, "golang", config.Indexes[0].Tags)
	require.Equal(t, "golang", config.Indexes[0].QueryOrTags(), "QueryOrTags should return Tags when Query is empty")
}

func TestParseConfigV2_IndexBothQueryAndTags(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "Test both fields"
indexes:
  - file: "dex/custom.md"
    summary: "custom index"
    query: "entity=concept && golang"
    tags: "golang"
`
	config, err := keg.ParseKegConfig([]byte(yamlData))
	require.NoError(t, err)
	require.Len(t, config.Indexes, 1)
	require.Equal(t, "entity=concept && golang", config.Indexes[0].Query)
	require.Equal(t, "golang", config.Indexes[0].Tags)
	require.Equal(t, "entity=concept && golang", config.Indexes[0].QueryOrTags(), "Query should take precedence")
}

func TestParseConfigV2_TimezoneDefaultsToUTC(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "No timezone"
`
	config, err := keg.ParseKegConfig([]byte(yamlData))
	require.NoError(t, err)
	require.Equal(t, "UTC", config.Timezone, "Timezone should default to UTC when omitted")
}

func TestParseConfigV2_TimezoneExplicit(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "With timezone"
timezone: "America/Chicago"
`
	config, err := keg.ParseKegConfig([]byte(yamlData))
	require.NoError(t, err)
	require.Equal(t, "America/Chicago", config.Timezone)
}

func TestConfigLocation_UTC(t *testing.T) {
	cfg := &keg.Config{Kegv: keg.ConfigV2VersionString}
	loc := cfg.Location()
	require.Equal(t, "UTC", loc.String())
}

func TestConfigLocation_ValidTimezone(t *testing.T) {
	cfg := &keg.Config{Kegv: keg.ConfigV2VersionString, Timezone: "America/Chicago"}
	loc := cfg.Location()
	require.Equal(t, "America/Chicago", loc.String())
}

func TestConfigLocation_InvalidTimezoneDefaultsToUTC(t *testing.T) {
	cfg := &keg.Config{Kegv: keg.ConfigV2VersionString, Timezone: "Invalid/Zone"}
	loc := cfg.Location()
	require.Equal(t, "UTC", loc.String())
}

func TestConfigLocation_EmptyTimezoneDefaultsToUTC(t *testing.T) {
	cfg := &keg.Config{Kegv: keg.ConfigV2VersionString, Timezone: ""}
	loc := cfg.Location()
	require.Equal(t, "UTC", loc.String())
}

func TestConfigV1_MigratedToV2_TimezoneDefaultsToUTC(t *testing.T) {
	v1Yaml := `
kegv: "2023-01"
title: "V1 KEG"
`
	config, err := keg.ParseKegConfig([]byte(v1Yaml))
	require.NoError(t, err)
	require.Equal(t, "UTC", config.Timezone, "V1 migrated to V2 should default timezone to UTC")
}
