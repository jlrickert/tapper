package tap_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jlrickert/tapper/pkg/tap"
	"github.com/stretchr/testify/require"
)

func TestWriteUserConfig_PreservesComments(t *testing.T) {
	t.Parallel()
	fx := NewFixture(t)

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

	uc, err := tap.ParseUserConfig(fx.ctx, []byte(raw))
	require.NoError(t, err, "ParseUserConfig failed")

	path := filepath.Join(fx.Jail, "user.yaml")
	fx.WriteUserConfigFile(path, uc)

	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read written user config")
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
	fx := NewFixture(t)

	raw := `# config header
defaultKeg: main
kegs:
  main: "~/keg" # keep this inline
`

	uc, err := tap.ParseUserConfig(fx.ctx, []byte(raw))
	require.NoError(t, err, "ParseUserConfig failed")

	clone := uc.Clone(fx.ctx)
	require.NotNil(t, clone, "expected clone to be non-nil")

	path := filepath.Join(fx.Jail, "user_clone.yaml")
	fx.WriteUserConfigFile(path, clone)

	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read written cloned user config")
	out := string(data)

	require.Contains(t, out, "# config header",
		"expected header comment preserved in cloned output")
	require.Contains(t, out, "# keep this inline",
		"expected inline comment preserved in cloned output")
}

func TestParseUserConfig_KegExamples(t *testing.T) {
	t.Parallel()
	fx := NewFixture(t)

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

	uc, err := tap.ParseUserConfig(fx.ctx, []byte(raw))
	require.NoError(t, err, "ParseUserConfig failed")

	path := filepath.Join(fx.Jail, "user_kegs.yaml")
	fx.WriteUserConfigFile(path, uc)

	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read written user config with kegs")
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
	fx := NewFixture(t)

	// Create a small config with one alias and one complex form.
	raw := `kegs:
  main: "https://example.com/main"
  nested:
    url: "api.example.com/v1"
`
	uc, err := tap.ParseUserConfig(fx.ctx, []byte(raw))
	require.NoError(t, err)

	// Successful resolve
	kt, err := uc.ResolveAlias(fx.ctx, "main")
	require.NoError(t, err, "expected ResolveAlias to succeed for existing alias")
	require.NotNil(t, kt)
	require.Contains(t, kt.String(), "https://example.com/main")

	// Nested mapping should also be present as a keg entry
	kt2, err := uc.ResolveAlias(fx.ctx, "nested")
	require.NoError(t, err, "expected ResolveAlias to succeed for nested mapping")
	require.NotNil(t, kt2)
	// The Parse behavior may normalize to an api scheme or similar; ensure non-empty.
	require.NotZero(t, kt2.String())

	// Missing alias yields error
	_, err = uc.ResolveAlias(fx.ctx, "missing")
	require.Error(t, err, "expected ResolveAlias to error for unknown alias")
}

func TestResolveProjectKeg_PrefixAndRegexPrecedence(t *testing.T) {
	t.Parallel()
	fx := NewFixture(t)

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
`, fx.Jail, fx.Jail, fx.Jail)

	uc, err := tap.ParseUserConfig(fx.ctx, []byte(raw))
	require.NoError(t, err, "ParseUserConfig failed")

	// Path matching the regex should prefer the regex alias
	pathRegexMatch := filepath.Join(fx.Jail, "x", "special")
	kt, err := uc.ResolveProjectKeg(fx.ctx, pathRegexMatch)
	require.NoError(t, err, "expected ResolveProjectKeg to match regex")
	require.Contains(t, kt.String(), "https://example.com/regex")

	// Path that matches both proj and projfoo should choose the longest prefix
	pathLongPrefix := filepath.Join(fx.Jail, "projects", "foo", "bar")
	kt2, err := uc.ResolveProjectKeg(fx.ctx, pathLongPrefix)
	require.NoError(t, err, "expected ResolveProjectKeg to match a prefix")
	require.Contains(t, kt2.String(), "https://example.com/projfoo")

	// Path that only matches proj prefix
	pathProj := filepath.Join(fx.Jail, "projects", "other")
	kt3, err := uc.ResolveProjectKeg(fx.ctx, pathProj)
	require.NoError(t, err, "expected ResolveProjectKeg to match proj prefix")
	require.Contains(t, kt3.String(), "https://example.com/proj")

	// Path that matches nothing falls back to defaultKeg
	pathNone := filepath.Join(fx.Jail, "unmatched")
	kt4, err := uc.ResolveProjectKeg(fx.ctx, pathNone)
	require.NoError(t, err, "expected ResolveProjectKeg to fallback to defaultKeg")
	require.Contains(t, kt4.String(), "https://example.com/default")

	// If no default and no match, expect an error.
	rawNoDefault := fmt.Sprintf(`kegs:
  proj: "https://example.com/proj"
kegMap:
  - alias: proj
    pathPrefix: "%s/projects"
`, fx.Jail)
	uc2, err := tap.ParseUserConfig(fx.ctx, []byte(rawNoDefault))
	require.NoError(t, err)

	_, err = uc2.ResolveProjectKeg(fx.ctx, filepath.Join(fx.Jail, "nope"))
	require.Error(t, err, "expected ResolveProjectKeg to error when no match and no default")
}
