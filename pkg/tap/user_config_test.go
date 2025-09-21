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

func TestAddOrUpdateKeg_PreservesCommentsAndAddsEntry(t *testing.T) {
	t.Parallel()
	fx := NewFixture(t)

	raw := `# header comment
kegs:
  main:
    url: "~/keg" # main url comment
`

	uc, err := tap.ParseUserConfig(fx.ctx, []byte(raw))
	require.NoError(t, err, "ParseUserConfig failed")

	// Add a new keg (this should update both the struct view and the underlying node)
	uc.AddOrUpdateKeg("new", "/tmp/newkeg")

	path := filepath.Join(fx.tempDir, "user_add_update.yaml")
	fx.WriteUserConfigFile(path, uc)

	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read written user config")
	out := string(data)

	// Original comments should still be present
	require.Contains(t, out, "# header comment", "expected header comment preserved in output")
	require.Contains(t, out, "# main url comment", "expected main url inline comment preserved in output")

	// New alias should be present
	require.Contains(t, out, "alias: new", "expected new keg alias present in output")
	require.Contains(t, out, "/tmp/newkeg", "expected new keg url present in output")
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
