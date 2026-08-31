package keg_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

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
summary: "This is a test KEG V1 settings"
indexes:
  - file: "index1.md"
    summary: "Index 1 summary"
  - file: "index2.md"
    summary: "Index 2 summary"
`

	settings, err := keg.ParseKegSettings([]byte(v1Yaml))
	require.NoError(t, err, "ParseKegSettings failed")

	require.Equal(t, keg.SettingsV2VersionString, settings.Kegv)
	require.Equal(t, "Test KEG V1", settings.Title)
	userIndexes := settings.UserIndexEntries()
	require.Len(t, userIndexes, 2)
	require.Equal(t, "index1.md", userIndexes[0].File)
	require.Equal(t, "index2.md", userIndexes[1].File)
	require.Empty(t, settings.Links)
}

func TestParseConfigDataV2(t *testing.T) {
	v2Yaml := `
kegv: "2025-07"
updated: "2025-07-01"
title: "Test KEG V2"
url: "https://example.com/v2"
creator: "creator-v2"
state: "archived"
summary: "This is a test KEG V2 settings"
instructions: "Use this KEG for parser tests."
links:
  - alias: "home"
    url: "https://keg.example.com/@user/home"
  - alias: "docs"
    url: "https://keg.example.com/@user/docs"
indexes:
  - file: "index1.md"
    summary: "Index 1 summary"
schemaPolicy:
  default: warn
  human: off
  agent: block
  api: warn
  import: block
  restore: block
`

	settings, err := keg.ParseKegSettings([]byte(v2Yaml))
	require.NoError(t, err, "ParseKegSettings failed")

	require.Equal(t, keg.SettingsV2VersionString, settings.Kegv)
	require.Equal(t, "Test KEG V2", settings.Title)
	require.Equal(t, "Use this KEG for parser tests.", settings.Instructions)

	require.Len(t, settings.Links, 2, "expected 2 links")
	links := map[string]string{}
	for _, l := range settings.Links {
		links[l.Alias] = l.URL
	}
	require.Contains(t, links, "home")
	require.Contains(t, links, "docs")
	require.Equal(t, "https://keg.example.com/@user/home", links["home"])
	require.Equal(t, "https://keg.example.com/@user/docs", links["docs"])

	userIndexes := settings.UserIndexEntries()
	require.Len(t, userIndexes, 1)
	require.Equal(t, "index1.md", userIndexes[0].File)
	require.Equal(t, keg.ValidationModeOff, settings.SchemaPolicy.Human)
	require.Equal(t, keg.ValidationModeBlock, settings.SchemaPolicy.Agent)
	require.Equal(t, keg.ValidationModeWarn, settings.SchemaPolicy.API)

	yamlOut, err := settings.ToYAML()
	require.NoError(t, err)
	require.NotContains(t, string(yamlOut), "  default:")
	require.NotContains(t, string(yamlOut), "  import:")
	require.NotContains(t, string(yamlOut), "  restore:")
}

func TestParseConfigV2_SnapshotPolicyDefaults(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "No snapshot policy"
`
	settings, err := keg.ParseKegSettings([]byte(yamlData))
	require.NoError(t, err)
	require.NotNil(t, settings.Snapshots)
	require.Equal(t, keg.SnapshotModeAuto, settings.Snapshots.Mode)
	require.Equal(t, "1h", settings.Snapshots.IdleAfter)

	mode, idleAfter, err := settings.SnapshotPolicy()
	require.NoError(t, err)
	require.Equal(t, keg.SnapshotModeAuto, mode)
	require.Equal(t, time.Hour, idleAfter)
}

func TestParseConfigV2_SnapshotPolicyPartialDefaults(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "Partial snapshot policy"
snapshots:
  mode: off
`
	settings, err := keg.ParseKegSettings([]byte(yamlData))
	require.NoError(t, err)
	require.NotNil(t, settings.Snapshots)
	require.Equal(t, keg.SnapshotModeOff, settings.Snapshots.Mode)
	require.Equal(t, "1h", settings.Snapshots.IdleAfter)
}

func TestParseConfigStrict_RejectsInvalidSnapshotPolicy(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "mode",
			body: `
kegv: "2025-07"
snapshots:
  mode: prompt
  idleAfter: 1h
`,
			want: "invalid snapshots.mode",
		},
		{
			name: "duration",
			body: `
kegv: "2025-07"
snapshots:
  mode: auto
  idleAfter: someday
`,
			want: "invalid snapshots.idleAfter",
		},
		{
			name: "nonpositive",
			body: `
kegv: "2025-07"
snapshots:
  mode: auto
  idleAfter: 0s
`,
			want: "greater than zero",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := keg.ParseKegSettingsStrict([]byte(tt.body))
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestConfigSnapshotPolicyYAMLAndJSON(t *testing.T) {
	cfg := keg.NewSettings()

	yamlOut, err := cfg.ToYAML()
	require.NoError(t, err)
	require.Contains(t, string(yamlOut), "snapshots:")
	require.Contains(t, string(yamlOut), "mode: auto")
	require.Contains(t, string(yamlOut), "idleAfter: 1h")

	jsonOut, err := cfg.ToJSON()
	require.NoError(t, err)
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(jsonOut, &decoded))
	snapshots, ok := decoded["snapshots"].(map[string]any)
	require.True(t, ok, "snapshots should be serialized as an object: %s", jsonOut)
	require.Equal(t, "auto", snapshots["mode"])
	require.Equal(t, "1h", snapshots["idleAfter"])
}

func TestKegSettingsJSONSchemaIncludesCurrentProperties(t *testing.T) {
	raw, err := os.ReadFile("../../schemas/keg-settings.json")
	require.NoError(t, err)

	var schema map[string]any
	require.NoError(t, json.Unmarshal(raw, &schema))
	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok)
	snapshots, ok := properties["snapshots"].(map[string]any)
	require.True(t, ok, "schema should define snapshots")
	snapshotProperties, ok := snapshots["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, snapshotProperties, "mode")
	require.Contains(t, snapshotProperties, "idleAfter")
	schemaPolicy, ok := properties["schemaPolicy"].(map[string]any)
	require.True(t, ok, "schema should define schemaPolicy")
	policyProperties, ok := schemaPolicy["properties"].(map[string]any)
	require.True(t, ok)
	for _, name := range []string{"human", "agent", "api"} {
		property, ok := policyProperties[name].(map[string]any)
		require.True(t, ok, "schemaPolicy should define %s", name)
		require.Equal(t, []any{"off", "warn", "block"}, property["enum"])
	}
	for _, legacy := range []string{"default", "import", "restore"} {
		require.NotContains(t, policyProperties, legacy)
	}
	for _, removed := range []string{"entities", "tags", "doctor", "site"} {
		require.NotContains(t, properties, removed)
	}
}

func TestParseConfigDataInvalidVersion(t *testing.T) {
	invalidYaml := `
kegv: "invalid-version"
title: "Invalid version test"
`

	_, err := keg.ParseKegSettings([]byte(invalidYaml))
	require.Error(t, err, "expected error for unsupported settings version")
	require.Contains(t, err.Error(), "unsupported settings version")
}

func TestParseConfigDataMissingVersion(t *testing.T) {
	missingVersionYaml := `
title: "Missing version test"
`

	_, err := keg.ParseKegSettings([]byte(missingVersionYaml))
	require.Error(t, err, "expected error for missing version field")
	require.Contains(t, err.Error(), "missing or invalid kegv")
}

func TestLegacyConfigPropertiesAreToleratedAndDropped(t *testing.T) {
	cfg, err := keg.ParseKegSettings([]byte(`kegv: "2025-07"
title: Legacy
entities:
  note: {id: 1, summary: Notes}
tags:
  project: Projects
doctor:
  entityCheck: true
site:
  output: ./site
`))
	require.NoError(t, err)

	yamlOut, err := cfg.ToYAML()
	require.NoError(t, err)
	for _, removed := range []string{"entities:", "tags:", "doctor:", "site:"} {
		require.NotContains(t, string(yamlOut), removed)
	}
}

func TestConfigToYAML_OmitsSchemaModeline(t *testing.T) {
	// ToYAML is the persistence serializer: its output is what lands in the
	// on-disk `keg` file and what goes over the wire to a hub, where a
	// modeline naming one machine's filesystem would be meaningless. The
	// modeline is added only when a settings is opened in an editor.
	cfg := keg.NewSettings()
	out, err := cfg.ToYAML()
	require.NoError(t, err)
	require.NotContains(t, string(out), "yaml-language-server")
}

func TestParseConfigV2_IndexQueryField(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "Test query field"
indexes:
  - file: "concepts.md"
    summary: "concept nodes"
    query: "entity=concept"
`
	settings, err := keg.ParseKegSettings([]byte(yamlData))
	require.NoError(t, err)
	userIndexes := settings.UserIndexEntries()
	require.Len(t, userIndexes, 1)
	require.Equal(t, "entity=concept", userIndexes[0].Query)
}

func TestParseConfigV2_IndexFileBareForm(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "Bare-form indexes"
indexes:
  - file: "backlinks"
    summary: "all incoming links"
  - file: "concepts.md"
    summary: "concept nodes"
    query: "entity=concept"
`
	settings, err := keg.ParseKegSettings([]byte(yamlData))
	require.NoError(t, err)
	userIndexes := settings.UserIndexEntries()
	require.Len(t, userIndexes, 1)
	require.Equal(t, "concepts.md", userIndexes[0].File)
	require.Contains(t, indexFiles(settings.Indexes), "backlinks")
}

func TestNewConfig_SystemIndexesAreRuntimeOnly(t *testing.T) {
	cfg := keg.NewSettings()
	require.NotEmpty(t, cfg.Indexes)
	require.Empty(t, cfg.UserIndexEntries())
	for _, entry := range cfg.Indexes {
		require.NotEmpty(t, entry.File)
		require.False(t, strings.HasPrefix(entry.File, "dex/"),
			"default indexes should use bare form, got %q", entry.File)
	}
	out, err := cfg.ToYAML()
	require.NoError(t, err)
	require.NotContains(t, string(out), "nodes.tsv")
	require.NotContains(t, string(out), "changes.md")
}

func TestParseConfigV2_MaterializesSystemIndexes(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "No indexes"
`
	settings, err := keg.ParseKegSettings([]byte(yamlData))
	require.NoError(t, err)
	require.Equal(t,
		[]string{"nodes.tsv", "changes.md", "tags", "links", "backlinks", "timeline", "dirty"},
		indexFiles(settings.Indexes)[:len(keg.SystemIndexEntries())],
	)
	require.Empty(t, settings.UserIndexEntries())
}

func TestParseConfigStrict_RejectsSystemIndex(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "Bad indexes"
indexes:
  - file: "changes.md"
    summary: "try to override"
`
	_, err := keg.ParseKegSettingsStrict([]byte(yamlData))
	require.Error(t, err)
	require.Contains(t, err.Error(), "required system index")
}

func TestParseConfigStrict_RejectsDuplicateUserIndex(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "Bad indexes"
indexes:
  - file: "concepts.md"
    summary: "one"
  - file: "concepts.md"
    summary: "two"
`
	_, err := keg.ParseKegSettingsStrict([]byte(yamlData))
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate user index")
}

func TestParseConfig_ToleratesLegacySystemIndex(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "Legacy indexes"
indexes:
  - file: "changes.md"
    summary: "old"
  - file: "concepts.md"
    summary: "concept nodes"
`
	settings, err := keg.ParseKegSettings([]byte(yamlData))
	require.NoError(t, err)
	require.Equal(t, []string{"concepts.md"}, indexFiles(settings.UserIndexEntries()))

	out, err := settings.ToYAML()
	require.NoError(t, err)
	require.NotContains(t, string(out), `file: changes.md`)
	require.Contains(t, string(out), `file: concepts.md`)
}

func TestParseConfigV2_TimezoneDefaultsToUTC(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "No timezone"
`
	settings, err := keg.ParseKegSettings([]byte(yamlData))
	require.NoError(t, err)
	require.Equal(t, "UTC", settings.Timezone, "Timezone should default to UTC when omitted")
}

func TestParseConfigV2_TimezoneExplicit(t *testing.T) {
	yamlData := `
kegv: "2025-07"
title: "With timezone"
timezone: "America/Chicago"
`
	settings, err := keg.ParseKegSettings([]byte(yamlData))
	require.NoError(t, err)
	require.Equal(t, "America/Chicago", settings.Timezone)
}

func TestConfigLocation_UTC(t *testing.T) {
	cfg := &keg.Settings{Kegv: keg.SettingsV2VersionString}
	loc := cfg.Location()
	require.Equal(t, "UTC", loc.String())
}

func TestConfigLocation_ValidTimezone(t *testing.T) {
	cfg := &keg.Settings{Kegv: keg.SettingsV2VersionString, Timezone: "America/Chicago"}
	loc := cfg.Location()
	require.Equal(t, "America/Chicago", loc.String())
}

func TestConfigLocation_InvalidTimezoneDefaultsToUTC(t *testing.T) {
	cfg := &keg.Settings{Kegv: keg.SettingsV2VersionString, Timezone: "Invalid/Zone"}
	loc := cfg.Location()
	require.Equal(t, "UTC", loc.String())
}

func TestConfigLocation_EmptyTimezoneDefaultsToUTC(t *testing.T) {
	cfg := &keg.Settings{Kegv: keg.SettingsV2VersionString, Timezone: ""}
	loc := cfg.Location()
	require.Equal(t, "UTC", loc.String())
}

func TestConfigV1_MigratedToV2_TimezoneDefaultsToUTC(t *testing.T) {
	v1Yaml := `
kegv: "2023-01"
title: "V1 KEG"
`
	settings, err := keg.ParseKegSettings([]byte(v1Yaml))
	require.NoError(t, err)
	require.Equal(t, "UTC", settings.Timezone, "V1 migrated to V2 should default timezone to UTC")
}

func indexFiles(entries []keg.IndexEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.File)
	}
	return out
}
