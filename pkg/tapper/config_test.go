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
kegMap:
  - alias: main
    pathPrefix: "~/projects" # keep this inline
`

	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err, "ParseConfig failed")

	clone := uc.Clone()
	require.NotNil(t, clone, "expected clone to be non-nil")

	data, err := clone.ToYAML()
	require.NoError(t, err)
	out := string(data)

	require.Contains(t, out, "defaultKeg: main")
	require.Contains(t, out, "pathPrefix: ~/projects")
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

func TestParseUserConfig_IgnoresUnknownKeys(t *testing.T) {
	t.Parallel()

	// The config decoder ignores keys it does not recognize (here a `kegs`
	// block, which the schema does not define). Parsing succeeds and the
	// unknown block does not round-trip on re-serialize.
	raw := `defaultKeg: notes
fallbackNamespace: alice
kegs:
  notes: { hub: work, namespace: alice, name: notes }
  short: "keg:@bob/blog"
`

	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err, "ParseConfig must ignore unknown keys")
	require.Equal(t, "notes", uc.DefaultKeg())

	data, err := uc.ToYAML()
	require.NoError(t, err)
	require.NotContains(t, string(data), "kegs:", "the unknown kegs block must not round-trip")
}

func TestResolveAlias_Behavior(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	// ResolveAlias parses its argument as a keg reference (there is no alias
	// table) and resolves it through the namespace-centric ResolveRef chain.
	raw := `defaultHub: remote
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
    namespace: hubns
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

	// In the namespace-centric model the global defaultNamespace outranks the
	// per-hub default namespace (the per-hub default is now a last resort).
	kt, err = uc.ResolveRef(fx.Runtime(), tapper.KegRef{Hub: "cloud", Name: "k"})
	require.NoError(t, err)
	require.Equal(t, "keg:@defns/k", kt.String())

	// A hub without its own namespace falls back to defaultNamespace.
	kt, err = uc.ResolveRef(fx.Runtime(), tapper.KegRef{Hub: "bare", Name: "k"})
	require.NoError(t, err)
	require.Equal(t, "keg:@defns/k", kt.String())

	// The per-hub namespace applies only as a last resort: when no explicit,
	// defaultNamespace, or fallbackNamespace value exists.
	rawPerhubLast := `hubs:
  cloud:
    kind: remote
    url: https://example.com
    namespace: hubns
`
	ucPerhub, err := tapper.ParseConfig([]byte(rawPerhubLast))
	require.NoError(t, err)
	kt, err = ucPerhub.ResolveRef(fx.Runtime(), tapper.KegRef{Hub: "cloud", Name: "k"})
	require.NoError(t, err)
	require.Equal(t, "keg:@hubns/k", kt.String())

	// fallbackNamespace applies only when no default/per-hub namespace exists.
	rawFb := `fallbackNamespace: fbns
hubs:
  bare: { kind: remote, url: https://bare.example.com }
`
	ucFb, err := tapper.ParseConfig([]byte(rawFb))
	require.NoError(t, err)
	kt, err = ucFb.ResolveRef(fx.Runtime(), tapper.KegRef{Hub: "bare", Name: "k"})
	require.NoError(t, err)
	require.Equal(t, "keg:@fbns/k", kt.String())

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
  bare: { kind: remote, url: https://x.example.com }
`
	ucBad, err := tapper.ParseConfig([]byte(rawBad))
	require.NoError(t, err)
	_, err = ucBad.ResolveRef(fx.Runtime(), tapper.KegRef{Hub: "bare", Namespace: "flights.d", Name: "k"})
	require.Error(t, err, "flights.d namespace must be rejected")
}

func TestResolveRef_NamespaceCentric(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	// kegs[name].Namespace disambiguates the namespace; namespaces[ns].Hub
	// (struct form) and the scalar shorthand both pin the hosting hub.
	raw := `defaultHub: cloud
hubs:
  cloud:
    kind: remote
    url: https://cloud.example.com
  work:
    kind: remote
    url: https://work.example.com
namespaces:
  teamns: { hub: work }
  scalarns: cloud
`
	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)

	// An explicit namespace "teamns" routes to hub "work" via the namespaces
	// map. Assert on the resolved fields rather than String() so the test is
	// independent of the hub target's textual representation.
	kt, err := uc.ResolveRef(fx.Runtime(), tapper.KegRef{Namespace: "teamns", Name: "example"})
	require.NoError(t, err)
	require.Equal(t, "work", kt.Hub)
	require.Equal(t, "teamns", kt.Namespace)
	require.Equal(t, "example", kt.KegName)

	// An explicit namespace with a scalar-shorthand namespaces entry.
	kt, err = uc.ResolveRef(fx.Runtime(), tapper.KegRef{Namespace: "scalarns", Name: "k"})
	require.NoError(t, err)
	require.Equal(t, "cloud", kt.Hub)
	require.Equal(t, "scalarns", kt.Namespace)

	// A namespace with no namespaces[ns] entry falls back to defaultHub.
	kt, err = uc.ResolveRef(fx.Runtime(), tapper.KegRef{Namespace: "lone", Name: "k"})
	require.NoError(t, err)
	require.Equal(t, "cloud", kt.Hub)
	require.Equal(t, "lone", kt.Namespace)

	// The reserved @local namespace pins this machine's filesystem hub.
	kt, err = uc.ResolveRef(fx.Runtime(), tapper.KegRef{Namespace: tapper.LocalHubName, Name: "notes"})
	require.NoError(t, err)
	require.Contains(t, kt.String(), filepath.Join("@local", "notes"))
}

func TestResolveRef_LocalLayout(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	raw := `hubs:
  home:
    kind: local
    namespace: local
    basePath: ` + filepath.Join(fx.GetJail(), "data", "kegs") + `
`
	uc, err := tapper.ParseConfig([]byte(raw))
	require.NoError(t, err)

	// A bare name resolves through the sole local hub (namespace "local").
	kt, err := uc.ResolveAlias(fx.Runtime(), "notes")
	require.NoError(t, err)
	// Local kegs live under <basePath>/@<namespace>/<name>.
	require.Contains(t, kt.String(), filepath.Join("@local", "notes"))
}

func TestResolveProjectKeg_PrefixAndRegexPrecedence(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	// Build a config exercising regex precedence and longest-prefix selection.
	// kegMap aliases are keg references (@namespace/name) resolved via ResolveRef.
	raw := fmt.Sprintf(`defaultKeg: "@ns/default"
hubs:
  remote:
    kind: remote
    url: https://example.com
kegMap:
  - alias: "@ns/regex"
    pathRegex: "^%s/.*/special$"
  - alias: "@ns/projfoo"
    pathPrefix: "%s/projects/foo"
  - alias: "@ns/proj"
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
  - alias: "@ns/proj"
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

func TestParseConfig_UnknownKeysIgnored(t *testing.T) {
	t.Parallel()

	// The decoder ignores keys it does not recognize (e.g. kegSearchPaths,
	// userRepoPath, kegs). Parsing succeeds and those keys are dropped on
	// re-serialization.
	raw := `fallbackKeg: pub
kegSearchPaths:
  - ~/Documents/kegs
  - ~/repos/kegs
userRepoPath: ~/Documents/other
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
