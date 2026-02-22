package tapper_test

import (
	"fmt"
	"path/filepath"
	"testing"

	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestWriteUserConfig_PreservesComments(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	raw := `# Top comment
# another top comment
defaultKeg: main

kegs:
  main: "~/keg" # inline url comment
  # kegs trailing comment

kegMap:
  - alias: main
    pathPrefix: "~/projects" # prefix comment
`

	uc, err := tapper.ParseConfig(fx.Context(), []byte(raw))
	require.NoError(t, err, "ParseConfig failed")
	data, err := uc.ToYAML(fx.Context())
	require.NoError(t, err, "ToYAML failed")
	out := string(data)

	// Ensure top comments are preserved
	require.Contains(t, out, "# Top comment", "expected top comment preserved in output")
	require.Contains(t, out, "# another top comment",
		"expected second top comment preserved in output")

	// Ensure inline comment on url preserved
	require.Contains(t, out, "# inline url comment",
		"expected inline url comment preserved in output")

	// Ensure sequence trailing comment preserved
	require.Contains(t, out, "# kegs trailing comment",
		"expected kegs trailing comment preserved in output")

	// Ensure prefix comment preserved
	require.Contains(t, out, "# prefix comment",
		"expected pathPrefix inline comment preserved in output")
}

func TestClone_PreservesComments(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	raw := `# config header
defaultKeg: main
kegs:
  main: "~/keg" # keep this inline
`

	uc, err := tapper.ParseConfig(fx.Context(), []byte(raw))
	require.NoError(t, err, "ParseConfig failed")

	clone := uc.Clone(fx.Context())
	require.NotNil(t, clone, "expected clone to be non-nil")

	data, err := clone.ToYAML(fx.Context())
	out := string(data)

	require.Contains(t, out, "# config header",
		"expected header comment preserved in cloned output")
	require.Contains(t, out, "# keep this inline",
		"expected inline comment preserved in cloned output")
}

func TestParseUserConfig_KegExamples(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	// Examples of different keg value forms:
	// - api scheme
	// - https URL with username and password
	// - a value that is only a URL-like host/path without scheme
	// - another api example
	raw := `kegs:
  work:
    api: work.com/keg/api
    token: ${WORK_TOKEN}
    readonly: true
  api: "api://api.example.com/v1"
  https_keg: "https://alice:secret@secure.example.com/project"
  only_url: "keg.only.example/path"
  api_alt: "api://other.example"
`

	uc, err := tapper.ParseConfig(fx.Context(), []byte(raw))
	require.NoError(t, err, "ParseConfig failed")

	data, err := uc.ToYAML(fx.Context())
	out := string(data)

	// Ensure the various keg examples were preserved in the serialized YAML.
	require.Contains(t, out, "api://api.example.com/v1", "expected api URL present")
	require.Contains(t, out, "https://alice:secret@secure.example.com/project",
		"expected https URL with user:pass present")
	require.Contains(t, out, "keg.only.example/path",
		"expected only-url style value present")
	require.Contains(t, out, "api://other.example", "expected second api URL present")

	// Also ensure the nested mapping and token were preserved for the 'work' keg.
	require.Contains(t, out, "work:", "expected 'work' keg mapping present")
	require.Contains(t, out, "work.com/keg/api", "expected work api host/path present")
	require.Contains(t, out, "${WORK_TOKEN}", "expected token env var preserved")
	require.Contains(t, out, "readonly: true", "expected readonly flag preserved")
}

func TestResolveAlias_Behavior(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	// Create a small config with one alias and one complex form.
	raw := `kegs:
  main: "https://example.com/main"
  nested:
    url: "api.example.com/v1"
`
	uc, err := tapper.ParseConfig(fx.Context(), []byte(raw))
	require.NoError(t, err)

	t.Log(uc.Kegs())
	// Successful resolve
	kt, err := uc.ResolveAlias("main")
	require.NoError(t, err, "expected ResolveAlias to succeed for existing alias")
	require.NotNil(t, kt)
	require.Contains(t, kt.String(), "https://example.com/main")

	// Nested mapping should also be present as a keg entry
	kt2, err := uc.ResolveAlias("nested")
	require.NoError(t, err, "expected ResolveAlias to succeed for nested mapping")
	require.NotNil(t, kt2)
	// The Parse behavior may normalize to an api scheme or similar; ensure non-empty.
	require.NotZero(t, kt2.String())

	// Missing alias yields error
	_, err = uc.ResolveAlias("missing")
	require.Error(t, err, "expected ResolveAlias to error for unknown alias")
}

func TestResolveProjectKeg_PrefixAndRegexPrecedence(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	// Build a config exercising regex precedence and longest-prefix selection.
	raw := fmt.Sprintf(`defaultKeg: default
kegs:
  regex: "https://example.com/regex"
  proj: "https://example.com/proj"
  projfoo: "https://example.com/projfoo"
  default: "https://example.com/default"
  work: "example.com/default"
kegMap:
  - alias: regex
    pathRegex: "^%s/.*/special$"
  - alias: projfoo
    pathPrefix: "%s/projects/foo"
  - alias: proj
    pathPrefix: "%s/projects"
`, fx.GetJail(), fx.GetJail(), fx.GetJail())

	uc, err := tapper.ParseConfig(fx.Context(), []byte(raw))
	require.NoError(t, err, "ParseConfig failed")

	// Path matching the regex should prefer the regex alias
	pathRegexMatch := filepath.Join(fx.GetJail(), "x", "special")
	kt, err := uc.ResolveKegMap(fx.Context(), fx.Runtime(), pathRegexMatch)
	require.NoError(t, err, "expected ResolveProjectKeg to match regex")
	require.Contains(t, kt.String(), "https://example.com/regex")

	// Path that matches both proj and projfoo should choose the longest prefix
	pathLongPrefix := filepath.Join(fx.GetJail(), "projects", "foo", "bar")
	kt2, err := uc.ResolveKegMap(fx.Context(), fx.Runtime(), pathLongPrefix)
	require.NoError(t, err, "expected ResolveProjectKeg to match a prefix")
	require.Contains(t, kt2.String(), "https://example.com/projfoo")

	// Path that only matches proj prefix
	pathProj := filepath.Join(fx.GetJail(), "projects", "other")
	kt3, err := uc.ResolveKegMap(fx.Context(), fx.Runtime(), pathProj)
	require.NoError(t, err, "expected ResolveProjectKeg to match proj prefix")
	require.Contains(t, kt3.String(), "https://example.com/proj")

	// Path that matches nothing falls back to defaultKeg
	pathNone := filepath.Join(fx.GetJail(), "unmatched")
	_, err = uc.ResolveKegMap(fx.Context(), fx.Runtime(), pathNone)
	require.Error(t, err, "expected ResolveProjectKeg not return anything")

	// If no default and no match, expect an error.
	rawNoDefault := fmt.Sprintf(`kegs:
  proj: "https://example.com/proj"
kegMap:
  - alias: proj
    pathPrefix: "%s/projects"
`, fx.GetJail())
	uc2, err := tapper.ParseConfig(fx.Context(), []byte(rawNoDefault))
	require.NoError(t, err)

	_, err = uc2.ResolveKegMap(fx.Context(), fx.Runtime(), filepath.Join(fx.GetJail(), "nope"))
	require.Error(t, err, "expected ResolveProjectKeg to error when no match and no default")
}

func TestAddKeg_AddsAndUpdatesEntries(t *testing.T) {
	t.Parallel()

	raw := `kegs:
  existing: "https://example.com/existing"
`
	cfg, err := tapper.ParseConfig(t.Context(), []byte(raw))
	require.NoError(t, err)

	// Add a new keg
	newTarget := kegurl.NewFile("/path/to/keg")
	err = cfg.AddKeg("newkeg", newTarget)
	require.NoError(t, err)

	// Verify it's in the kegs map
	kegs := cfg.Kegs()
	require.Contains(t, kegs, "newkeg")
	target := kegs["newkeg"]
	require.Equal(t, newTarget.String(), target.String())

	// Verify existing entry is still there
	require.Contains(t, kegs, "existing")

	// Update an existing keg
	updatedTarget := kegurl.NewFile("/path/to/updated")
	err = cfg.AddKeg("existing", updatedTarget)
	require.NoError(t, err)

	kegs = cfg.Kegs()
	target = kegs["existing"]
	require.Equal(t, updatedTarget.String(), target.String())

	// Serialize and verify the changes are present
	data, err := cfg.ToYAML(t.Context())
	require.NoError(t, err)
	out := string(data)
	require.Contains(t, out, "newkeg")
	require.Contains(t, out, "/path/to/keg")
}

func TestAddKeg_ReturnsErrorOnNilOrEmptyAlias(t *testing.T) {
	t.Parallel()

	cfg := tapper.DefaultUserConfig("testuser", "/tmp")

	// Test nil config
	var nilCfg *tapper.Config
	err := nilCfg.AddKeg("test", kegurl.NewFile("/path"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "config is nil")

	// Test empty alias
	err = cfg.AddKeg("", kegurl.NewFile("/path"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "alias is required")
}

func TestAddKegMap_AddsAndUpdatesEntries(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	raw := `kegMap:
  - alias: existing
    pathPrefix: "/existing"
`
	cfg, err := tapper.ParseConfig(fx.Context(), []byte(raw))
	require.NoError(t, err)

	// Add a new keg map entry
	newEntry := tapper.KegMapEntry{
		Alias:      "newentry",
		PathPrefix: "/new/prefix",
	}
	err = cfg.AddKegMap(newEntry)
	require.NoError(t, err)

	// Verify it's in the kegMap
	kegMap := cfg.KegMap()
	found := false
	for _, e := range kegMap {
		if e.Alias == "newentry" && e.PathPrefix == "/new/prefix" {
			found = true
			break
		}
	}
	require.True(t, found, "expected newentry to be present in kegMap")

	// Verify existing entry is still there
	found = false
	for _, e := range kegMap {
		if e.Alias == "existing" && e.PathPrefix == "/existing" {
			found = true
			break
		}
	}
	require.True(t, found, "expected existing entry to still be present")

	// Update an existing entry
	updatedEntry := tapper.KegMapEntry{
		Alias:      "existing",
		PathPrefix: "/updated/prefix",
		PathRegex:  "^/regex",
	}
	err = cfg.AddKegMap(updatedEntry)
	require.NoError(t, err)

	kegMap = cfg.KegMap()
	found = false
	for _, e := range kegMap {
		if e.Alias == "existing" && e.PathPrefix == "/updated/prefix" && e.PathRegex == "^/regex" {
			found = true
			break
		}
	}
	require.True(t, found, "expected existing entry to be updated")

	// Verify serialization includes the changes
	data, err := cfg.ToYAML(fx.Context())
	require.NoError(t, err)
	out := string(data)
	require.Contains(t, out, "newentry")
	require.Contains(t, out, "/new/prefix")
}

func TestAddKegMap_ReturnsErrorOnNilOrEmptyAlias(t *testing.T) {
	t.Parallel()
	cfg := tapper.DefaultUserConfig("testuser", "/tmp")

	// Test nil config
	var nilCfg *tapper.Config
	err := nilCfg.AddKegMap(tapper.KegMapEntry{Alias: "test"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "config is nil")

	// Test empty alias
	err = cfg.AddKegMap(tapper.KegMapEntry{Alias: ""})
	require.Error(t, err)
	require.Contains(t, err.Error(), "alias is required")
}
