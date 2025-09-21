package tap_test

import (
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
  main:
    url: "~/keg" # inline url comment
  # kegs trailing comment

kegMap:
  - alias: main
    pathPrefix: "~/projects" # prefix comment
`

	uc, err := tap.ParseUserConfig(fx.ctx, []byte(raw))
	require.NoError(t, err, "ParseUserConfig failed")

	path := filepath.Join(fx.tempDir, "user.yaml")
	fx.WriteUserConfigFile(path, uc)

	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read written user config")
	out := string(data)

	// Ensure top comments are preserved
	require.Contains(t, out, "# Top comment", "expected top comment preserved in output")
	require.Contains(t, out, "# another top comment", "expected second top comment preserved in output")

	// Ensure inline comment on url preserved
	require.Contains(t, out, "# inline url comment", "expected inline url comment preserved in output")

	// Ensure sequence trailing comment preserved
	require.Contains(t, out, "# kegs trailing comment", "expected kegs trailing comment preserved in output")

	// Ensure prefix comment preserved
	require.Contains(t, out, "# prefix comment", "expected pathPrefix inline comment preserved in output")
}

func TestClone_PreservesComments(t *testing.T) {
	t.Parallel()
	fx := NewFixture(t)

	raw := `# config header
defaultKeg: main
kegs:
  main:
    url: "~/keg" # keep this inline
`

	uc, err := tap.ParseUserConfig(fx.ctx, []byte(raw))
	require.NoError(t, err, "ParseUserConfig failed")

	clone := uc.Clone(fx.ctx)
	require.NotNil(t, clone, "expected clone to be non-nil")

	path := filepath.Join(fx.tempDir, "user_clone.yaml")
	fx.WriteUserConfigFile(path, clone)

	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read written cloned user config")
	out := string(data)

	require.Contains(t, out, "# config header", "expected header comment preserved in cloned output")
	require.Contains(t, out, "# keep this inline", "expected inline comment preserved in cloned output")
}

// New tests for mutation helpers

func TestResolveKegMap_RegexPrecedenceAndLongestPrefix(t *testing.T) {
	t.Parallel()
	fx := NewFixture(t)

	// Prepare Kegs map (aliases point to file URIs for easy ParseKegTarget)
	uc := &tap.UserConfig{
		Kegs: map[string]tap.KegUrl{
			"p": {URL: "file:///p"},
			"r": {URL: "file:///r"},
		},
		KegMap: []tap.KegMapEntry{
			// prefix that will match; a shorter one
			{Alias: "p", PathPrefix: filepath.Join(fx.tempDir, "projects")},
			// longer prefix to test longest-prefix selection
			{Alias: "p_long", PathPrefix: filepath.Join(fx.tempDir, "projects", "sub")},
			// regex entry that should win over prefixes when matched
			{Alias: "r", PathRegex: ".*special.*"},
		},
	}

	// ensure Kegs has entries for p_long as well
	uc.Kegs["p_long"] = tap.KegUrl{URL: "file:///p_long"}

	// Path that matches both a prefix and the regex; regex should take precedence
	testPath := filepath.Join(fx.tempDir, "projects", "sub", "special", "repo")
	kt, err := uc.ResolveKegMap(fx.ctx, testPath)
	require.NoError(t, err, "ResolveKegMap should succeed when regex matches")
	require.NotNil(t, kt)
	// regex alias 'r' points to file:///r
	require.Equal(t, "file", kt.Schema)
	require.Equal(t, "r", filepath.Base(kt.Path), "expected resolved target path base to be 'r' due to regex match")

	// Path that matches multiple prefixes; longest prefix (p_long) should win
	prefixPath := filepath.Join(fx.tempDir, "projects", "sub", "other")
	kt2, err := uc.ResolveKegMap(fx.ctx, prefixPath)
	require.NoError(t, err, "ResolveKegMap should succeed for prefix match")
	require.NotNil(t, kt2)
	require.Equal(t, "file", kt2.Schema)
	require.Equal(t, "p_long", filepath.Base(kt2.Path), "expected p_long to be selected for longest prefix")
}
