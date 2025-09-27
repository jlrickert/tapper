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
  main: "~/keg" # inline url comment
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

	path := filepath.Join(fx.tempDir, "user_clone.yaml")
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

	path := filepath.Join(fx.tempDir, "user_kegs.yaml")
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
