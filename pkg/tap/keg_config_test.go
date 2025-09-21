package tap_test

import (
	"testing"

	"github.com/jlrickert/tapper/pkg/tap"
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

	config, err := tap.ParseKegConfig([]byte(v1Yaml))
	require.NoError(t, err, "ParseKegConfig failed")

	require.Equal(t, tap.ConfigV2VersionString, config.Kegv)
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
`

	config, err := tap.ParseKegConfig([]byte(v2Yaml))
	require.NoError(t, err, "ParseKegConfig failed")

	require.Equal(t, tap.ConfigV2VersionString, config.Kegv)
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
}

func TestParseConfigDataInvalidVersion(t *testing.T) {
	invalidYaml := `
kegv: "invalid-version"
title: "Invalid version test"
`

	_, err := tap.ParseKegConfig([]byte(invalidYaml))
	require.Error(t, err, "expected error for unsupported config version")
	require.Contains(t, err.Error(), "unsupported config version")
}

func TestParseConfigDataMissingVersion(t *testing.T) {
	missingVersionYaml := `
title: "Missing version test"
`

	_, err := tap.ParseKegConfig([]byte(missingVersionYaml))
	require.Error(t, err, "expected error for missing version field")
	require.Contains(t, err.Error(), "missing or invalid kegv")
}
