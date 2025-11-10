package tap_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jlrickert/tapper/pkg/tap"
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

	uc, err := tap.ParseUserConfig(fx.Context(), []byte(raw))
	require.NoError(t, err, "ParseUserConfig failed")
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

	uc, err := tap.ParseUserConfig(fx.Context(), []byte(raw))
	require.NoError(t, err, "ParseUserConfig failed")

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

	uc, err := tap.ParseUserConfig(fx.Context(), []byte(raw))
	require.NoError(t, err, "ParseUserConfig failed")

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
	uc, err := tap.ParseUserConfig(fx.Context(), []byte(raw))
	require.NoError(t, err)

	// Successful resolve
	kt, err := uc.ResolveAlias(fx.Context(), "main")
	require.NoError(t, err, "expected ResolveAlias to succeed for existing alias")
	require.NotNil(t, kt)
	require.Contains(t, kt.String(), "https://example.com/main")

	// Nested mapping should also be present as a keg entry
	kt2, err := uc.ResolveAlias(fx.Context(), "nested")
	require.NoError(t, err, "expected ResolveAlias to succeed for nested mapping")
	require.NotNil(t, kt2)
	// The Parse behavior may normalize to an api scheme or similar; ensure non-empty.
	require.NotZero(t, kt2.String())

	// Missing alias yields error
	_, err = uc.ResolveAlias(fx.Context(), "missing")
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

	uc, err := tap.ParseUserConfig(fx.Context(), []byte(raw))
	require.NoError(t, err, "ParseUserConfig failed")

	// Path matching the regex should prefer the regex alias
	pathRegexMatch := filepath.Join(fx.GetJail(), "x", "special")
	kt, err := uc.ResolveProjectKeg(fx.Context(), pathRegexMatch)
	require.NoError(t, err, "expected ResolveProjectKeg to match regex")
	require.Contains(t, kt.String(), "https://example.com/regex")

	// Path that matches both proj and projfoo should choose the longest prefix
	pathLongPrefix := filepath.Join(fx.GetJail(), "projects", "foo", "bar")
	kt2, err := uc.ResolveProjectKeg(fx.Context(), pathLongPrefix)
	require.NoError(t, err, "expected ResolveProjectKeg to match a prefix")
	require.Contains(t, kt2.String(), "https://example.com/projfoo")

	// Path that only matches proj prefix
	pathProj := filepath.Join(fx.GetJail(), "projects", "other")
	kt3, err := uc.ResolveProjectKeg(fx.Context(), pathProj)
	require.NoError(t, err, "expected ResolveProjectKeg to match proj prefix")
	require.Contains(t, kt3.String(), "https://example.com/proj")

	// Path that matches nothing falls back to defaultKeg
	pathNone := filepath.Join(fx.GetJail(), "unmatched")
	kt4, err := uc.ResolveProjectKeg(fx.Context(), pathNone)
	require.NoError(t, err, "expected ResolveProjectKeg to fallback to defaultKeg")
	require.Contains(t, kt4.String(), "https://example.com/default")

	// If no default and no match, expect an error.
	rawNoDefault := fmt.Sprintf(`kegs:
  proj: "https://example.com/proj"
kegMap:
  - alias: proj
    pathPrefix: "%s/projects"
`, fx.GetJail())
	uc2, err := tap.ParseUserConfig(fx.Context(), []byte(rawNoDefault))
	require.NoError(t, err)

	_, err = uc2.ResolveProjectKeg(fx.Context(), filepath.Join(fx.GetJail(), "nope"))
	require.Error(t, err, "expected ResolveProjectKeg to error when no match and no default")
}
