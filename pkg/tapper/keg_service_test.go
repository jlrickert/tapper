package tapper_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestResolve_DefaultKegOverridesKegMap(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("example", "/home/testuser"))
	root := "/home/testuser/repos/github.com/jlrickert/tapper"
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	userCfg := []byte(`fallbackKeg: pub
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
kegs: {}
defaultRegistry: ""
kegSearchPaths:
  - ~/Documents/kegs
`)
	projectCfg := []byte(`defaultKeg: ecw
kegMap: []
kegs: {}
defaultRegistry: ""
`)

	require.NoError(t, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.UserConfig()), 0o755, true))
	require.NoError(t, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.ProjectConfig()), 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), userCfg, 0o644))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.ProjectConfig(), projectCfg, 0o644))

	require.NoError(t, fx.Runtime().Mkdir("/home/testuser/Documents/kegs/pub", 0o755, true))
	require.NoError(t, fx.Runtime().Mkdir("/home/testuser/Documents/kegs/ecw", 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile("/home/testuser/Documents/kegs/pub/keg", []byte(""), 0o644))
	require.NoError(t, fx.Runtime().AtomicWriteFile("/home/testuser/Documents/kegs/ecw/keg", []byte(""), 0o644))

	k, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
		Root: root,
	})
	require.NoError(t, err)
	require.NotNil(t, k)
	require.NotNil(t, k.Target)
	require.Equal(t, filepath.Clean("/home/testuser/Documents/kegs/ecw"), filepath.Clean(k.Target.Path()))
}
