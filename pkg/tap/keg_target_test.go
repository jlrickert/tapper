package tap_test

import (
	"net/url"
	"path/filepath"
	"testing"

	"github.com/jlrickert/tapper/pkg/tap"
	"github.com/stretchr/testify/require"
)

func TestParseKegTarget_HTTP_KegSuffix(t *testing.T) {
	fx := NewFixture(t)

	raw := "https://example.com/project"
	kt, err := tap.ParseKegTarget(fx.ctx, raw)
	require.NoError(t, err, "ParseKegTarget failed")
	require.Equal(t, "https", kt.Schema, "expected scheme https")
	// Path should be the URL path (e.g., "/project")
	require.Equal(t, "project", filepath.Base(kt.Path), "expected path base 'project'")
	// remote target should have a non-empty scheme
	require.NotEmpty(t, kt.Schema, "expected remote target, got local")

	// Ensure String produces a parseable URL and path base is correct.
	uStr := kt.String()
	u, err := url.Parse(uStr)
	require.NoError(t, err, "String produced invalid URL")
	require.Equal(t, "project", filepath.Base(u.Path), "expected URL path base 'project'")
}

func TestParseKegTarget_FileURIAndPath(t *testing.T) {
	fx := NewFixture(t)

	// file:// URI case
	raw := "file:///tmp/keg"
	kt, err := tap.ParseKegTarget(fx.ctx, raw)
	require.NoError(t, err, "ParseKegTarget failed")
	require.Equal(t, "file", kt.Schema, "expected type file")
	// Path base should be "keg"
	require.Equal(t, "keg", filepath.Base(kt.Path), "expected parsed file uri path base to be 'keg'")
	out := kt.String()
	u, err := url.Parse(out)
	require.NoError(t, err, "String produced invalid URL")
	require.Equal(t, "keg", filepath.Base(u.Path), "expected String() path base to be 'keg'")

	// plain filesystem path (no scheme)
	tmp := fx.tempDir
	rawPath := filepath.Join(tmp, "keg")
	kt2, err := tap.ParseKegTarget(fx.ctx, rawPath)
	require.NoError(t, err, "ParseKegTarget failed for path")
	// For plain paths ParseKegTarget produces an empty scheme and sets Path to the cleaned path.
	require.Empty(t, kt2.Schema, "expected empty scheme for plain path")
	abs, _ := filepath.Abs(rawPath)
	require.Equal(t, filepath.Clean(abs), filepath.Clean(kt2.Path), "expected Path to be absolute")
	out2 := kt2.String()
	u2, err := url.Parse(out2)
	require.NoError(t, err, "String produced invalid URL for path")
	require.Equal(t, "keg", filepath.Base(u2.Path), "expected String() path base to be 'keg'")
}

func TestParseKegTarget_EmptyError(t *testing.T) {
	fx := NewFixture(t)

	_, err := tap.ParseKegTarget(fx.ctx, "")
	require.Error(t, err, "expected error for empty target")
}

func TestNormalize_ExpandsEnvAndMakesAbsolute(t *testing.T) {
	fx := NewFixture(t)

	// set env var used in Uri in the fixture's Env
	err := fx.env.Set("KEG_TEST_DIR", fx.tempDir)
	require.NoError(t, err, "failed to set env in fixture")

	kt, err := tap.ParseKegTarget(fx.ctx, "${KEG_TEST_DIR}/keg")
	require.NoError(t, err, "ExpandPath failed")
	require.True(t, filepath.IsAbs(kt.Path), "expected normalized Uri to be absolute")
}
