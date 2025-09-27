package kegurl_test

import (
	"net/url"
	"path/filepath"
	"testing"

	std "github.com/jlrickert/go-std/pkg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/stretchr/testify/require"
)

func TestParseKegTarget_HTTP_KegSuffix(t *testing.T) {
	raw := "https://example.com/project"
	kt, err := kegurl.Parse(t.Context(), raw)
	require.NoError(t, err, "ParseKegTarget failed")
	require.Equal(t, "https", kt.Schema(), "expected scheme https")
	// Path should be the URL path (e.g., "/project")
	require.Equal(t, "project", filepath.Base(kt.Path()), "expected path base 'project'")
	// remote target should have a non-empty scheme
	require.NotEmpty(t, kt.Schema(), "expected remote target, got local")

	// Ensure String produces a parseable URL and path base is correct.
	uStr := kt.String()
	u, err := url.Parse(uStr)
	require.NoError(t, err, "String produced invalid URL")
	require.Equal(t, "project", filepath.Base(u.Path), "expected URL path base 'project'")
}

func TestParseKegTarget_FileURIAndPath(t *testing.T) {
	// file:// URI case
	raw := "file:///tmp/keg"
	kt, err := kegurl.Parse(t.Context(), raw)
	require.NoError(t, err, "ParseKegTarget failed")
	require.Equal(t, "file", kt.Schema(), "expected type file")
	// Path base should be "keg"
	require.Equal(t, "keg", filepath.Base(kt.Path()), "expected parsed file uri path base to be 'keg'")
	out := kt.String()
	u, err := url.Parse(out)
	require.NoError(t, err, "String produced invalid URL")
	require.Equal(t, "keg", filepath.Base(u.Path), "expected String() path base to be 'keg'")

	// plain filesystem path (no scheme)
	tmp := t.TempDir()
	rawPath := filepath.Join(tmp, "keg")
	kt2, err := kegurl.Parse(t.Context(), rawPath)
	require.NoError(t, err, "ParseKegTarget failed for path")
	// For plain paths ParseKegTarget produces an empty scheme and sets Path to the cleaned path.
	require.NotEmpty(t, kt2.Schema(), "expected default file scheme for plain path")
	abs, _ := filepath.Abs(rawPath)
	require.Equal(t, filepath.Clean(abs), filepath.Clean(kt2.Path()), "expected Path to be absolute")
	out2 := kt2.String()
	u2, err := url.Parse(out2)
	require.NoError(t, err, "String produced invalid URL for path")
	require.Equal(t, "keg", filepath.Base(u2.Path), "expected String() path base to be 'keg'")
}

func TestParseKegTarget_EmptyError(t *testing.T) {
	_, err := kegurl.Parse(t.Context(), "")
	require.Error(t, err, "expected error for empty target")
}

func TestNormalize_ExpandsEnvAndMakesAbsolute(t *testing.T) {
	env := std.NewTestEnv("test-user", "test-user")
	ctx := std.WithEnv(t.Context(), env)
	tempDir := t.TempDir()

	// set env var used in Uri in the fixture's Env
	err := env.Set("KEG_TEST_DIR", tempDir)
	require.NoError(t, err, "failed to set env in fixture")

	kt, err := kegurl.Parse(ctx, "${KEG_TEST_DIR}/keg")
	require.NoError(t, err, "ExpandPath failed")
	require.True(t, filepath.IsAbs(kt.Path()), "expected normalized Uri to be absolute")
}
