package tapper_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestWriteUserConfigPreservesCommentsAndUnknownBlocks(t *testing.T) {
	t.Parallel()

	raw := `# Top comment
# another top comment
keg: main

kegs:
  main: "~/keg" # inline url comment
  # kegs trailing comment

kegMap:
  - keg: main
    pathPrefix: "~/projects" # prefix comment
`

	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err, "ParseConfig failed")
	data, err := uc.ToYAML()
	require.NoError(t, err, "ToYAML failed")
	out := string(data)

	require.Contains(t, out, "# Top comment")
	require.Contains(t, out, "# inline url comment")
	require.Contains(t, out, "kegs:")
	require.Contains(t, out, "keg: main")
	require.Contains(t, out, "pathPrefix: ~/projects")
}

func TestClone_CopiesData(t *testing.T) {
	t.Parallel()

	raw := `# config header
keg: main
kegMap:
  - keg: main
    pathPrefix: "~/projects" # keep this inline
`

	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err, "ParseConfig failed")

	clone := uc.Clone()
	require.NotNil(t, clone, "expected clone to be non-nil")

	data, err := clone.ToYAML()
	require.NoError(t, err)
	out := string(data)

	require.Contains(t, out, "keg: main")
	require.Contains(t, out, "pathPrefix: ~/projects")
	require.Contains(t, out, "# config header")
}

func TestParseConfig_AcceptsUnknownFields(t *testing.T) {
	t.Parallel()

	raw := `keg: main
unknownKey: value
`

	cfg, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)
	require.Equal(t, "main", cfg.Keg())
	out, err := cfg.ToYAML()
	require.NoError(t, err)
	require.Contains(t, string(out), "unknownKey: value")
}

func TestConfigRewritePreservesUnknownTopLevelAndNestedFields(t *testing.T) {
	t.Parallel()

	raw := `keg: old
vendorFeature:
  enabled: true
hubs:
  work:
    kind: remote
    url: https://old.example.com
    tokenEnv: WORK_TOKEN
    retryPolicy:
      attempts: 7
namespaces:
  team:
    hub: work
    tenantId: tenant-42
agents:
  builder:
    model: openai/gpt-5
    providerOption: retained
kegMap:
  - keg: "@team/notes"
    pathPrefix: /workspace
    extensionRule: retained
`
	cfg, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)
	require.NoError(t, cfg.SetKeg("@team/new-default"))
	require.NoError(t, cfg.SetHub("work", tapper.HubEntry{
		URL: "https://new.example.com",
	}))

	out, err := cfg.ToYAML()
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(out, &doc))
	require.Equal(t, "@team/new-default", doc["keg"])
	require.Equal(t, map[string]any{"enabled": true}, doc["vendorFeature"])

	hubs := doc["hubs"].(map[string]any)
	work := hubs["work"].(map[string]any)
	require.Equal(t, "remote", work["kind"])
	require.Equal(t, "https://new.example.com", work["url"])
	require.NotContains(t, work, "tokenEnv")
	require.Equal(t, map[string]any{"attempts": 7}, work["retryPolicy"])

	namespaces := doc["namespaces"].(map[string]any)
	team := namespaces["team"].(map[string]any)
	require.Equal(t, "work", team["hub"])
	require.Equal(t, "tenant-42", team["tenantId"])

	agents := doc["agents"].(map[string]any)
	builder := agents["builder"].(map[string]any)
	require.Equal(t, "retained", builder["providerOption"])
	kegMap := doc["kegMap"].([]any)
	require.Equal(t, "retained", kegMap[0].(map[string]any)["extensionRule"])
}

func TestConfigExplicitObjectRemovalRemovesUnknownNestedFields(t *testing.T) {
	t.Parallel()

	cfg, err := tapper.ParseConfig([]byte(`hubs:
  keep: {url: https://keep.example.com, vendor: keep}
  remove: {url: https://remove.example.com, vendor: remove}
namespaces:
  keep: {hub: keep, vendor: keep}
  remove: {hub: remove, vendor: remove}
`))
	require.NoError(t, err)
	removed, err := cfg.DeleteHub("remove")
	require.NoError(t, err)
	require.True(t, removed)

	out, err := cfg.ToYAML()
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(out, &doc))
	hubs := doc["hubs"].(map[string]any)
	require.NotContains(t, hubs, "remove")
	require.Equal(t, "keep", hubs["keep"].(map[string]any)["vendor"])
	namespaces := doc["namespaces"].(map[string]any)
	require.Contains(t, namespaces, "remove")
	require.Equal(t, "keep", namespaces["keep"].(map[string]any)["vendor"])
}

func TestParseUserConfigPreservesUnknownKeys(t *testing.T) {
	t.Parallel()

	// Unknown blocks load and survive Tapper-driven serialization.
	raw := `keg: notes
fallbackNamespace: alice
kegs:
  notes: { hub: work, namespace: alice, name: notes }
  short: "keg:@bob/blog"
`

	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)
	require.Equal(t, "notes", uc.Keg())

	data, err := uc.ToYAML()
	require.NoError(t, err)
	require.Contains(t, string(data), "kegs:")
	require.Contains(t, string(data), "short: \"keg:@bob/blog\"")
}

func TestResolveAlias_Behavior(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	// ResolveAlias parses its argument as a keg reference (there is no alias
	// table) and resolves it through the namespace-centric ResolveRef chain.
	raw := `hub: remote
hubs:
  remote:
    kind: remote
    url: https://example.com
`
	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)

	// A @namespace/name reference resolves namespace-centrically.
	kt, err := uc.ResolveAlias(fx.Runtime(), "@alice/main")
	require.NoError(t, err, "expected ResolveAlias to succeed for a qualified reference")
	require.NotNil(t, kt)
	require.Equal(t, "keg:@alice/main", kt.String())

	// The keg: scheme is accepted too.
	kt2, err := uc.ResolveAlias(fx.Runtime(), "keg:@bob/notes")
	require.NoError(t, err)
	require.NotNil(t, kt2)
	require.Equal(t, "keg:@bob/notes", kt2.String())

	// An empty selector errors.
	_, err = uc.ResolveAlias(fx.Runtime(), "")
	require.Error(t, err, "expected ResolveAlias to error for an empty selector")

	// A bare name with no resolvable namespace errors (remote hub, no namespace).
	_, err = uc.ResolveAlias(fx.Runtime(), "missing")
	require.Error(t, err, "expected ResolveAlias to error when no namespace resolves")
}

func TestResolveRef_NamespacePrecedence(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	raw := `defaultNamespace: defns
fallbackNamespace: fbns
hubs:
  cloud:
    kind: remote
    url: https://example.com
    defaultNamespace: hubns
  bare:
    kind: remote
    url: https://bare.example.com
`
	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)

	// Explicit namespace on the ref wins over everything.
	kt, err := uc.ResolveRef(fx.Runtime(), tapper.KegRef{Hub: "cloud", Namespace: "own", Name: "k"})
	require.NoError(t, err)
	require.Equal(t, "keg:@own/k", kt.String())

	// Retired defaults never resolve a bare name.
	for _, hub := range []string{"cloud", "bare"} {
		_, err = uc.ResolveRef(fx.Runtime(), tapper.KegRef{Hub: hub, Name: "k"})
		require.ErrorContains(t, err, "namespace is required")
	}

	// A remote hub with no namespace anywhere is an error.
	rawErr := `hubs:
  bare: { kind: remote, url: https://bare.example.com }
`
	ucErr, err := tapper.ParseConfig([]byte(rawErr))
	require.NoError(t, err)
	_, err = ucErr.ResolveRef(fx.Runtime(), tapper.KegRef{Hub: "bare", Name: "k"})
	require.Error(t, err, "remote ref with no namespace must error")

	// An invalid namespace (flights.d) is rejected at resolve time.
	rawBad := `hubs:
  bare: { url: https://x.example.com }
`
	ucBad, err := tapper.ParseConfig([]byte(rawBad))
	require.NoError(t, err)
	_, err = ucBad.ResolveRef(fx.Runtime(), tapper.KegRef{Hub: "bare", Namespace: "flights.d", Name: "k"})
	require.Error(t, err, "flights.d namespace must be rejected")
}

func TestResolveProjectKeg_PrefixAndRegexPrecedence(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	// Build a config exercising regex precedence and longest-prefix selection.
	// kegMap KEG defaults are references (@namespace/name) resolved via ResolveRef.
	raw := fmt.Sprintf(`keg: "@ns/default"
hubs:
  remote:
    kind: remote
    url: https://example.com
kegMap:
  - keg: "@ns/regex"
    pathRegex: "^%s/.*/special$"
  - keg: "@ns/projfoo"
    pathPrefix: "%s/projects/foo"
  - keg: "@ns/proj"
    pathPrefix: "%s/projects"
`, fx.GetJail(), fx.GetJail(), fx.GetJail())

	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err, "ParseConfig failed")

	// Path matching the regex should prefer the regex alias
	pathRegexMatch := filepath.Join(fx.GetJail(), "x", "special")
	kt, err := uc.ResolveKegMap(fx.Runtime(), pathRegexMatch)
	require.NoError(t, err, "expected ResolveProjectKeg to match regex")
	require.Equal(t, "keg:@ns/regex", kt.String())

	// Path that matches both proj and projfoo should choose the longest prefix
	pathLongPrefix := filepath.Join(fx.GetJail(), "projects", "foo", "bar")
	kt2, err := uc.ResolveKegMap(fx.Runtime(), pathLongPrefix)
	require.NoError(t, err, "expected ResolveProjectKeg to match a prefix")
	require.Equal(t, "keg:@ns/projfoo", kt2.String())

	// Path that only matches proj prefix
	pathProj := filepath.Join(fx.GetJail(), "projects", "other")
	kt3, err := uc.ResolveKegMap(fx.Runtime(), pathProj)
	require.NoError(t, err, "expected ResolveProjectKeg to match proj prefix")
	require.Equal(t, "keg:@ns/proj", kt3.String())

	// Path that matches nothing yields an alias-not-found error
	pathNone := filepath.Join(fx.GetJail(), "unmatched")
	_, err = uc.ResolveKegMap(fx.Runtime(), pathNone)
	require.Error(t, err, "expected ResolveProjectKeg not return anything")

	// If no default and no match, expect an error.
	rawNoDefault := fmt.Sprintf(`hubs:
  remote: { kind: remote, url: https://example.com }
kegMap:
  - keg: "@ns/proj"
    pathPrefix: "%s/projects"
`, fx.GetJail())
	uc2, err := tapper.ParseConfig([]byte(rawNoDefault))
	require.NoError(t, err)

	_, err = uc2.ResolveKegMap(fx.Runtime(), filepath.Join(fx.GetJail(), "nope"))
	require.Error(t, err, "expected ResolveProjectKeg to error when no match and no default")
}

func TestAddKegMap_AddsAndUpdatesEntries(t *testing.T) {
	t.Parallel()

	raw := `kegMap:
  - keg: existing
    pathPrefix: "/existing"
`
	cfg, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)

	// Add a new keg map entry
	newEntry := tapper.KegMapEntry{
		Keg:        "newentry",
		PathPrefix: "/new/prefix",
	}
	err = cfg.AddKegMap(newEntry)
	require.NoError(t, err)

	// Verify it's in the kegMap
	kegMap := cfg.KegMap()
	found := false
	for _, e := range kegMap {
		if e.Keg == "newentry" && e.PathPrefix == "/new/prefix" {
			found = true
			break
		}
	}
	require.True(t, found, "expected newentry to be present in kegMap")

	// Verify the existing entry is still there
	found = false
	for _, e := range kegMap {
		if e.Keg == "existing" && e.PathPrefix == "/existing" {
			found = true
			break
		}
	}
	require.True(t, found, "expected existing entry to still be present")

	// Update an existing entry
	updatedEntry := tapper.KegMapEntry{
		Keg:        "existing",
		PathPrefix: "/updated/prefix",
		PathRegex:  "^/regex",
	}
	err = cfg.AddKegMap(updatedEntry)
	require.NoError(t, err)

	kegMap = cfg.KegMap()
	found = false
	for _, e := range kegMap {
		if e.Keg == "existing" && e.PathPrefix == "/updated/prefix" && e.PathRegex == "^/regex" {
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

func TestAddKegMap_ReturnsErrorOnNilOrEmptyDefaults(t *testing.T) {
	t.Parallel()
	cfg := tapper.DefaultUserConfig("testuser")

	// Test nil config
	var nilCfg *tapper.Config
	err := nilCfg.AddKegMap(tapper.KegMapEntry{Keg: "test"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "config is nil")

	// Test empty defaults
	err = cfg.AddKegMap(tapper.KegMapEntry{Keg: ""})
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least one of keg, hub, or flight is required")
}

func TestAddKegMap_PreservesMultipleEntriesWithSameKeg(t *testing.T) {
	t.Parallel()

	raw := `kegMap:
  - keg: work
    pathPrefix: ~/repos/github.com/work-devel/
  - keg: work
    pathPrefix: ~/repos/github.com/jared52/
`
	cfg, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)

	kegMap := cfg.KegMap()
	require.Len(t, kegMap, 2, "both work entries should be preserved after parse")

	// Verify both prefixes are present.
	var prefixes []string
	for _, e := range kegMap {
		if e.Keg == "work" {
			prefixes = append(prefixes, e.PathPrefix)
		}
	}
	require.ElementsMatch(t, []string{
		"~/repos/github.com/work-devel/",
		"~/repos/github.com/jared52/",
	}, prefixes)
}

func TestMergeConfig_PreservesMultipleEntriesWithSameKeg(t *testing.T) {
	t.Parallel()

	userRaw := `kegMap:
  - keg: work
    pathPrefix: ~/repos/github.com/work-devel/
  - keg: work
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

func TestParseConfigUnknownKeysSurviveRewrite(t *testing.T) {
	t.Parallel()

	// Arbitrary unknown keys remain semantically present on re-serialization.
	raw := `keg: pub
kegSearchPaths:
  - ~/Documents/kegs
  - ~/repos/kegs
userRepoPath: ~/Documents/other
kegMap: []
kegs: {}
`
	cfg, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)
	require.Equal(t, "pub", cfg.Keg())

	out, err := cfg.ToYAML()
	require.NoError(t, err)
	require.Contains(t, string(out), "kegSearchPaths")
	require.Contains(t, string(out), "userRepoPath")
	require.Contains(t, string(out), "kegs: {}")
}

func TestMergeConfig_DefaultFallbackPrecedence(t *testing.T) {
	t.Parallel()

	userRaw := `keg: pub
hub: atlas
fallbackNamespace: pub
kegMap: []
kegs: {}
`
	projectRaw := `keg: work
hub: atlas
defaultNamespace: work
kegMap: []
kegs: {}
`

	userCfg, err := tapper.ParseConfig([]byte(userRaw))
	require.NoError(t, err)
	projectCfg, err := tapper.ParseConfig([]byte(projectRaw))
	require.NoError(t, err)

	merged := tapper.MergeConfig(userCfg, projectCfg)
	require.Equal(t, "work", merged.Keg())
	require.Equal(t, "work", merged.Keg())
	// Project defaults and user fallbacks both survive the merge.
	require.Equal(t, "atlas", merged.HubName())
	require.Equal(t, "atlas", merged.HubName())

}

func TestConfigToYAML_PrependsSchemaModeline(t *testing.T) {
	t.Parallel()

	cfg := tapper.DefaultUserConfig("pub")
	out, err := cfg.ToYAML()
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(out), "# yaml-language-server: $schema="+tapper.TapConfigSchemaURL+"\n"))
}
