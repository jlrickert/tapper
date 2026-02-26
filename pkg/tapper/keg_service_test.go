package tapper_test

import (
	"context"
	"path/filepath"
	"strings"
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

func TestResolve_FullPrecedenceChain(t *testing.T) {
	t.Parallel()

	newTap := func(innerT *testing.T) (*sandbox.Sandbox, *tapper.Tap, string) {
		innerT.Helper()
		fx := NewSandbox(innerT, sandbox.WithFixture("example", "/home/testuser"))
		root := "/home/testuser/repos/github.com/jlrickert/tapper"
		require.NoError(innerT, fx.Setwd(root))
		tap, err := tapper.NewTap(tapper.TapOptions{
			Root:    root,
			Runtime: fx.Runtime(),
		})
		require.NoError(innerT, err)
		require.NoError(innerT, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.UserConfig()), 0o755, true))
		require.NoError(innerT, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.ProjectConfig()), 0o755, true))
		return fx, tap, root
	}

	writeCfg := func(innerT *testing.T, fx *sandbox.Sandbox, tap *tapper.Tap, userCfg string, projectCfg string) {
		innerT.Helper()
		require.NoError(innerT, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))
		require.NoError(innerT, fx.Runtime().AtomicWriteFile(tap.PathService.ProjectConfig(), []byte(projectCfg), 0o644))
	}

	t.Run("explicit_alias_wins", func(innerT *testing.T) {
		innerT.Parallel()
		fx, tap, root := newTap(innerT)

		writeCfg(innerT, fx, tap, `fallbackKeg: pub
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
kegs: {}
defaultRegistry: ""
kegSearchPaths:
  - ~/Documents/kegs
`, `defaultKeg: ecw
kegMap: []
kegs: {}
defaultRegistry: ""
`)

		for _, alias := range []string{"pub", "ecw", "explicit"} {
			require.NoError(innerT, fx.Runtime().Mkdir(filepath.Join("/home/testuser/Documents/kegs", alias), 0o755, true))
			require.NoError(innerT, fx.Runtime().AtomicWriteFile(filepath.Join("/home/testuser/Documents/kegs", alias, "keg"), []byte(""), 0o644))
		}

		k, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
			Root: root,
			Keg:  "explicit",
		})
		require.NoError(innerT, err)
		require.Equal(innerT, filepath.Clean("/home/testuser/Documents/kegs/explicit"), filepath.Clean(k.Target.Path()))
	})

	t.Run("default_used_before_map", func(innerT *testing.T) {
		innerT.Parallel()
		fx, tap, root := newTap(innerT)

		writeCfg(innerT, fx, tap, `fallbackKeg: pub
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
kegs: {}
defaultRegistry: ""
kegSearchPaths:
  - ~/Documents/kegs
`, `defaultKeg: ecw
kegMap: []
kegs: {}
defaultRegistry: ""
`)

		for _, alias := range []string{"pub", "ecw"} {
			require.NoError(innerT, fx.Runtime().Mkdir(filepath.Join("/home/testuser/Documents/kegs", alias), 0o755, true))
			require.NoError(innerT, fx.Runtime().AtomicWriteFile(filepath.Join("/home/testuser/Documents/kegs", alias, "keg"), []byte(""), 0o644))
		}

		k, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
			Root: root,
		})
		require.NoError(innerT, err)
		require.Equal(innerT, filepath.Clean("/home/testuser/Documents/kegs/ecw"), filepath.Clean(k.Target.Path()))
	})

	t.Run("map_used_when_default_empty", func(innerT *testing.T) {
		innerT.Parallel()
		fx, tap, root := newTap(innerT)

		writeCfg(innerT, fx, tap, `fallbackKeg: fallback
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
kegs: {}
defaultRegistry: ""
kegSearchPaths:
  - ~/Documents/kegs
`, `kegMap: []
kegs: {}
defaultRegistry: ""
`)

		for _, alias := range []string{"pub", "fallback"} {
			require.NoError(innerT, fx.Runtime().Mkdir(filepath.Join("/home/testuser/Documents/kegs", alias), 0o755, true))
			require.NoError(innerT, fx.Runtime().AtomicWriteFile(filepath.Join("/home/testuser/Documents/kegs", alias, "keg"), []byte(""), 0o644))
		}

		k, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
			Root: root,
		})
		require.NoError(innerT, err)
		require.Equal(innerT, filepath.Clean("/home/testuser/Documents/kegs/pub"), filepath.Clean(k.Target.Path()))
	})

	t.Run("fallback_used_when_default_and_map_missing", func(innerT *testing.T) {
		innerT.Parallel()
		fx, tap, _ := newTap(innerT)

		writeCfg(innerT, fx, tap, `fallbackKeg: fallback
kegMap: []
kegs: {}
defaultRegistry: ""
kegSearchPaths:
  - ~/Documents/kegs
`, `kegMap: []
kegs: {}
defaultRegistry: ""
`)

		require.NoError(innerT, fx.Runtime().Mkdir("/home/testuser/Documents/kegs/fallback", 0o755, true))
		require.NoError(innerT, fx.Runtime().AtomicWriteFile("/home/testuser/Documents/kegs/fallback/keg", []byte(""), 0o644))

		k, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
			Root: "/home/testuser/unmapped/workspace",
		})
		require.NoError(innerT, err)
		require.Equal(innerT, filepath.Clean("/home/testuser/Documents/kegs/fallback"), filepath.Clean(k.Target.Path()))
	})
}

func TestResolveTarget_DiscoveryPathCollisionLaterWins(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("example", "/home/testuser"))
	root := "/home/testuser/repos/github.com/jlrickert/tapper"
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.UserConfig()), 0o755, true))
	require.NoError(t, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.ProjectConfig()), 0o755, true))

	userCfg := `defaultKeg: pub
fallbackKeg: pub
kegMap: []
kegs: {}
defaultRegistry: ""
kegSearchPaths:
  - ~/Documents/kegs-a
  - ~/Documents/kegs-b
`
	projectCfg := `kegMap: []
kegs: {}
defaultRegistry: ""
`
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.ProjectConfig(), []byte(projectCfg), 0o644))

	for _, path := range []string{"/home/testuser/Documents/kegs-a/pub", "/home/testuser/Documents/kegs-b/pub"} {
		require.NoError(t, fx.Runtime().Mkdir(path, 0o755, true))
		require.NoError(t, fx.Runtime().AtomicWriteFile(filepath.Join(path, "keg"), []byte(""), 0o644))
	}

	target, err := tap.ConfigService.ResolveTarget("pub", false)
	require.NoError(t, err)
	require.NotNil(t, target)
	require.True(t, strings.HasSuffix(filepath.Clean(target.Path()), filepath.Clean("/home/testuser/Documents/kegs-b/pub")))
}
