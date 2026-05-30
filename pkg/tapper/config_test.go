package tapper_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestWriteUserConfig_NormalizesWithoutComments(t *testing.T) {
	t.Parallel()

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

	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err, "ParseConfig failed")
	data, err := uc.ToYAML()
	require.NoError(t, err, "ToYAML failed")
	out := string(data)

	// Comment preservation is no longer required.
	require.NotContains(t, out, "# Top comment")
	require.NotContains(t, out, "# inline url comment")
	require.Contains(t, out, "defaultKeg: main")
	require.Contains(t, out, "pathPrefix: ~/projects")
}

func TestClone_CopiesData(t *testing.T) {
	t.Parallel()

	raw := `# config header
defaultKeg: main
kegs:
  main: "~/keg" # keep this inline
`

	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err, "ParseConfig failed")

	clone := uc.Clone()
	require.NotNil(t, clone, "expected clone to be non-nil")

	data, err := clone.ToYAML()
	require.NoError(t, err)
	out := string(data)

	require.Contains(t, out, "defaultKeg: main")
	require.Contains(t, out, "main:")
	require.NotContains(t, out, "# config header")
	require.NotContains(t, out, "# keep this inline")
}

func TestParseConfig_AcceptsUnknownFields(t *testing.T) {
	t.Parallel()

	raw := `defaultKeg: main
unknownKey: value
`

	cfg, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)
	require.Equal(t, "main", cfg.DefaultKeg())
}

func TestParseUserConfig_KegExamples(t *testing.T) {
	t.Parallel()

	// Keg values are (hub, namespace, name) triples. Hub shorthand
	// ("hub:@ns/name") is accepted on input and normalized to the triple form.
	raw := `hubs:
  work:
    kind: remote
    url: https://work.example.com
    tokenEnv: WORK_TOKEN
kegs:
  notes: { hub: work, namespace: alice, name: notes }
  short: "work:@bob/blog"
`

	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err, "ParseConfig failed")

	kegs := uc.Kegs()
	require.Equal(t, tapper.KegRef{Hub: "work", Namespace: "alice", Name: "notes"}, kegs["notes"])
	require.Equal(t, tapper.KegRef{Hub: "work", Namespace: "bob", Name: "blog"}, kegs["short"])

	data, err := uc.ToYAML()
	require.NoError(t, err)
	out := string(data)

	// The canonical triple form and the hub definition round-trip.
	require.Contains(t, out, "name: notes")
	require.Contains(t, out, "namespace: alice")
	require.Contains(t, out, "url: https://work.example.com")
}

func TestResolveAlias_Behavior(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	raw := `hubs:
  remote:
    kind: remote
    url: https://example.com
kegs:
  main: { hub: remote, namespace: alice, name: main }
  nested: { hub: remote, namespace: bob, name: notes }
`
	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)

	// Successful resolve
	kt, err := uc.ResolveAlias(fx.Runtime(), "main")
	require.NoError(t, err, "expected ResolveAlias to succeed for existing alias")
	require.NotNil(t, kt)
	require.Equal(t, "remote:@alice/main", kt.String())

	kt2, err := uc.ResolveAlias(fx.Runtime(), "nested")
	require.NoError(t, err, "expected ResolveAlias to succeed for nested mapping")
	require.NotNil(t, kt2)
	require.Equal(t, "remote:@bob/notes", kt2.String())

	// Missing alias yields an error
	_, err = uc.ResolveAlias(fx.Runtime(), "missing")
	require.Error(t, err, "expected ResolveAlias to error for unknown alias")
}

func TestResolveProjectKeg_PrefixAndRegexPrecedence(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	// Build a config exercising regex precedence and longest-prefix selection.
	raw := fmt.Sprintf(`defaultKeg: default
hubs:
  remote:
    kind: remote
    url: https://example.com
kegs:
  regex: { hub: remote, namespace: ns, name: regex }
  proj: { hub: remote, namespace: ns, name: proj }
  projfoo: { hub: remote, namespace: ns, name: projfoo }
  default: { hub: remote, namespace: ns, name: default }
  work: { hub: remote, namespace: ns, name: work }
kegMap:
  - alias: regex
    pathRegex: "^%s/.*/special$"
  - alias: projfoo
    pathPrefix: "%s/projects/foo"
  - alias: proj
    pathPrefix: "%s/projects"
`, fx.GetJail(), fx.GetJail(), fx.GetJail())

	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err, "ParseConfig failed")

	// Path matching the regex should prefer the regex alias
	pathRegexMatch := filepath.Join(fx.GetJail(), "x", "special")
	kt, err := uc.ResolveKegMap(fx.Runtime(), pathRegexMatch)
	require.NoError(t, err, "expected ResolveProjectKeg to match regex")
	require.Equal(t, "remote:@ns/regex", kt.String())

	// Path that matches both proj and projfoo should choose the longest prefix
	pathLongPrefix := filepath.Join(fx.GetJail(), "projects", "foo", "bar")
	kt2, err := uc.ResolveKegMap(fx.Runtime(), pathLongPrefix)
	require.NoError(t, err, "expected ResolveProjectKeg to match a prefix")
	require.Equal(t, "remote:@ns/projfoo", kt2.String())

	// Path that only matches proj prefix
	pathProj := filepath.Join(fx.GetJail(), "projects", "other")
	kt3, err := uc.ResolveKegMap(fx.Runtime(), pathProj)
	require.NoError(t, err, "expected ResolveProjectKeg to match proj prefix")
	require.Equal(t, "remote:@ns/proj", kt3.String())

	// Path that matches nothing yields an alias-not-found error
	pathNone := filepath.Join(fx.GetJail(), "unmatched")
	_, err = uc.ResolveKegMap(fx.Runtime(), pathNone)
	require.Error(t, err, "expected ResolveProjectKeg not return anything")

	// If no default and no match, expect an error.
	rawNoDefault := fmt.Sprintf(`hubs:
  remote: { kind: remote, url: https://example.com }
kegs:
  proj: { hub: remote, namespace: ns, name: proj }
kegMap:
  - alias: proj
    pathPrefix: "%s/projects"
`, fx.GetJail())
	uc2, err := tapper.ParseConfig([]byte(rawNoDefault))
	require.NoError(t, err)

	_, err = uc2.ResolveKegMap(fx.Runtime(), filepath.Join(fx.GetJail(), "nope"))
	require.Error(t, err, "expected ResolveProjectKeg to error when no match and no default")
}

func TestAddKeg_AddsAndUpdatesEntries(t *testing.T) {
	t.Parallel()

	raw := `kegs:
  existing: { hub: atlas, namespace: alice, name: existing }
`
	cfg, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)

	// Add a new keg
	newRef := tapper.KegRef{Hub: "local", Namespace: "local", Name: "newkeg"}
	err = cfg.AddKeg("newkeg", newRef)
	require.NoError(t, err)

	// Verify it's in the kegs map
	kegs := cfg.Kegs()
	require.Contains(t, kegs, "newkeg")
	require.Equal(t, newRef, kegs["newkeg"])

	// Verify the existing entry is still there
	require.Contains(t, kegs, "existing")

	// Update an existing keg
	updatedRef := tapper.KegRef{Hub: "atlas", Namespace: "alice", Name: "renamed"}
	err = cfg.AddKeg("existing", updatedRef)
	require.NoError(t, err)
	require.Equal(t, updatedRef, cfg.Kegs()["existing"])

	// Serialize and verify the changes are present
	data, err := cfg.ToYAML()
	require.NoError(t, err)
	out := string(data)
	require.Contains(t, out, "newkeg")
	require.Contains(t, out, "name: newkeg")
}

func TestAddKeg_ReturnsErrorOnNilOrEmptyAlias(t *testing.T) {
	t.Parallel()

	cfg := tapper.DefaultUserConfig("testuser", "/tmp")

	// Test nil config
	var nilCfg *tapper.Config
	err := nilCfg.AddKeg("test", tapper.KegRef{Name: "test"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "config is nil")

	// Test empty alias
	err = cfg.AddKeg("", tapper.KegRef{Name: "x"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "alias is required")
}

func TestAddKegMap_AddsAndUpdatesEntries(t *testing.T) {
	t.Parallel()

	raw := `kegMap:
  - alias: existing
    pathPrefix: "/existing"
`
	cfg, err := tapper.ParseConfig([]byte(raw))
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

	// Verify the existing entry is still there
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
	data, err := cfg.ToYAML()
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

func TestAddKegMap_PreservesMultipleEntriesWithSameAlias(t *testing.T) {
	t.Parallel()

	raw := `kegMap:
  - alias: work
    pathPrefix: ~/repos/github.com/work-devel/
  - alias: work
    pathPrefix: ~/repos/github.com/jared52/
`
	cfg, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)

	kegMap := cfg.KegMap()
	require.Len(t, kegMap, 2, "both work entries should be preserved after parse")

	// Verify both prefixes are present.
	var prefixes []string
	for _, e := range kegMap {
		if e.Alias == "work" {
			prefixes = append(prefixes, e.PathPrefix)
		}
	}
	require.ElementsMatch(t, []string{
		"~/repos/github.com/work-devel/",
		"~/repos/github.com/jared52/",
	}, prefixes)
}

func TestMergeConfig_PreservesMultipleEntriesWithSameAlias(t *testing.T) {
	t.Parallel()

	userRaw := `kegMap:
  - alias: work
    pathPrefix: ~/repos/github.com/work-devel/
  - alias: work
    pathPrefix: ~/repos/github.com/jared52/
`
	projectRaw := `kegMap: []
`
	user, err := tapper.ParseConfig([]byte(userRaw))
	require.NoError(t, err)
	project, err := tapper.ParseConfig([]byte(projectRaw))
	require.NoError(t, err)

	merged := tapper.MergeConfig(user, project)
	kegMap := merged.KegMap()
	require.Len(t, kegMap, 2, "both work entries should survive merge")
}

func TestParseConfig_LegacyKegSearchPathsIgnored(t *testing.T) {
	t.Parallel()

	// kegSearchPaths was removed; legacy configs that still carry it (and any
	// other unknown keys) must load without error, with the key ignored and
	// dropped on re-serialization.
	raw := `fallbackKeg: pub
kegSearchPaths:
  - ~/Documents/kegs
  - ~/repos/kegs
userRepoPath: ~/Documents/legacy
kegMap: []
kegs: {}
`
	cfg, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)
	require.Equal(t, "pub", cfg.FallbackKeg())

	out, err := cfg.ToYAML()
	require.NoError(t, err)
	require.NotContains(t, string(out), "kegSearchPaths")
}

func TestMergeConfig_DefaultFallbackPrecedence(t *testing.T) {
	t.Parallel()

	userRaw := `defaultKeg: pub
fallbackKeg: pub
fallbackHub: atlas
fallbackNamespace: pub
kegMap: []
kegs: {}
`
	projectRaw := `defaultKeg: work
fallbackKeg: work
defaultHub: atlas
defaultNamespace: work
kegMap: []
kegs: {}
`

	userCfg, err := tapper.ParseConfig([]byte(userRaw))
	require.NoError(t, err)
	projectCfg, err := tapper.ParseConfig([]byte(projectRaw))
	require.NoError(t, err)

	merged := tapper.MergeConfig(userCfg, projectCfg)
	require.Equal(t, "work", merged.DefaultKeg())
	require.Equal(t, "work", merged.FallbackKeg())
	// Project defaults and user fallbacks both survive the merge.
	require.Equal(t, "atlas", merged.DefaultHub())
	require.Equal(t, "atlas", merged.FallbackHub())
	require.Equal(t, "work", merged.DefaultNamespace())
	require.Equal(t, "pub", merged.FallbackNamespace())
}

func TestConfigToYAML_PrependsSchemaModeline(t *testing.T) {
	t.Parallel()

	cfg := tapper.DefaultUserConfig("pub", "~/Documents/kegs")
	out, err := cfg.ToYAML()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(out), "# yaml-language-server: $schema="+tapper.TapConfigSchemaURL+"\n"))
}
