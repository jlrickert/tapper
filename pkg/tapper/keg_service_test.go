package tapper_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// kegsBlock is a kegs: section registering each alias as an explicit local
// path under ~/Documents/kegs/<alias>. Tests create only the directories they
// resolve; unreferenced entries are harmless.
const kegsBlock = `kegs:
  pub: { path: ~/Documents/kegs/pub }
  work: { path: ~/Documents/kegs/work }
  fallback: { path: ~/Documents/kegs/fallback }
  explicit: { path: ~/Documents/kegs/explicit }
  dev: { path: ~/Documents/kegs/dev }
`

func TestResolve_KegMapOverridesDefaultKeg(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("example", "/home/testuser"))
	root := "/home/testuser/repos/github.com/jlrickert/tapper"
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	userCfg := []byte(`fallbackKeg: fallback
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
` + kegsBlock)
	projectCfg := []byte(`defaultKeg: work
kegMap: []
kegs: {}
`)

	require.NoError(t, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.UserConfig()), 0o755, true))
	require.NoError(t, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.ProjectConfig()), 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), userCfg, 0o644))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.ProjectConfig(), projectCfg, 0o644))

	require.NoError(t, fx.Runtime().Mkdir("/home/testuser/Documents/kegs/pub", 0o755, true))
	require.NoError(t, fx.Runtime().Mkdir("/home/testuser/Documents/kegs/work", 0o755, true))
	require.NoError(t, fx.Runtime().Mkdir("/home/testuser/Documents/kegs/fallback", 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile("/home/testuser/Documents/kegs/pub/keg", []byte(""), 0o644))
	require.NoError(t, fx.Runtime().AtomicWriteFile("/home/testuser/Documents/kegs/work/keg", []byte(""), 0o644))
	require.NoError(t, fx.Runtime().AtomicWriteFile("/home/testuser/Documents/kegs/fallback/keg", []byte(""), 0o644))

	// kegMap matches the working directory path, so it should win over defaultKeg.
	k, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
		Root: root,
	})
	require.NoError(t, err)
	require.NotNil(t, k)
	require.NotNil(t, k.Target)
	require.Equal(t, filepath.Clean("/home/testuser/Documents/kegs/pub"), filepath.Clean(k.Target.Path()))
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

	mkKegs := func(innerT *testing.T, fx *sandbox.Sandbox, aliases ...string) {
		innerT.Helper()
		for _, alias := range aliases {
			require.NoError(innerT, fx.Runtime().Mkdir(filepath.Join("/home/testuser/Documents/kegs", alias), 0o755, true))
			require.NoError(innerT, fx.Runtime().AtomicWriteFile(filepath.Join("/home/testuser/Documents/kegs", alias, "keg"), []byte(""), 0o644))
		}
	}

	t.Run("explicit_alias_wins", func(innerT *testing.T) {
		innerT.Parallel()
		fx, tap, root := newTap(innerT)

		writeCfg(innerT, fx, tap, `fallbackKeg: pub
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
`+kegsBlock, `defaultKeg: work
kegMap: []
kegs: {}
`)

		mkKegs(innerT, fx, "pub", "work", "explicit")

		k, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
			Root: root,
			Keg:  "explicit",
		})
		require.NoError(innerT, err)
		require.Equal(innerT, filepath.Clean("/home/testuser/Documents/kegs/explicit"), filepath.Clean(k.Target.Path()))
	})

	t.Run("map_wins_over_default_when_path_matches", func(innerT *testing.T) {
		innerT.Parallel()
		fx, tap, root := newTap(innerT)

		writeCfg(innerT, fx, tap, `fallbackKeg: fallback
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
`+kegsBlock, `defaultKeg: work
kegMap: []
kegs: {}
`)

		mkKegs(innerT, fx, "pub", "work", "fallback")

		// kegMap matches the path, so it wins over defaultKeg.
		k, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
			Root: root,
		})
		require.NoError(innerT, err)
		require.Equal(innerT, filepath.Clean("/home/testuser/Documents/kegs/pub"), filepath.Clean(k.Target.Path()))
	})

	t.Run("default_used_when_map_does_not_match", func(innerT *testing.T) {
		innerT.Parallel()
		fx, tap, _ := newTap(innerT)

		writeCfg(innerT, fx, tap, `fallbackKeg: fallback
kegMap:
  - alias: pub
    pathPrefix: ~/repos/gitlab.com
`+kegsBlock, `defaultKeg: work
kegMap: []
kegs: {}
`)

		mkKegs(innerT, fx, "pub", "work", "fallback")

		// kegMap does NOT match (gitlab.com vs github.com), so defaultKeg wins.
		k, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
			Root: "/home/testuser/repos/github.com/jlrickert/tapper",
		})
		require.NoError(innerT, err)
		require.Equal(innerT, filepath.Clean("/home/testuser/Documents/kegs/work"), filepath.Clean(k.Target.Path()))
	})

	t.Run("map_used_when_default_empty", func(innerT *testing.T) {
		innerT.Parallel()
		fx, tap, root := newTap(innerT)

		writeCfg(innerT, fx, tap, `fallbackKeg: fallback
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
`+kegsBlock, `kegMap: []
kegs: {}
`)

		mkKegs(innerT, fx, "pub", "fallback")

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
`+kegsBlock, `kegMap: []
kegs: {}
`)

		mkKegs(innerT, fx, "fallback")

		k, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
			Root: "/home/testuser/unmapped/workspace",
		})
		require.NoError(innerT, err)
		require.Equal(innerT, filepath.Clean("/home/testuser/Documents/kegs/fallback"), filepath.Clean(k.Target.Path()))
	})
}

func TestResolve_KegMapMissFallsToDefaultThenFallback(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("example", "/home/testuser"))
	root := "/home/testuser/repos/github.com/work-devel/project.202602"
	require.NoError(t, fx.Setwd(root))
	require.NoError(t, fx.Runtime().Mkdir(root, 0o755, true))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.UserConfig()), 0o755, true))
	require.NoError(t, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.ProjectConfig()), 0o755, true))

	// kegMap points to a prefix that does NOT match the working directory.
	// defaultKeg is set, so it should be used when kegMap misses.
	userCfg := `fallbackKeg: pub
defaultKeg: dev
kegMap:
  - alias: work
    pathPrefix: ~/sandbox/work/
` + kegsBlock
	projectCfg := `kegMap: []
kegs: {}
`
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.ProjectConfig(), []byte(projectCfg), 0o644))

	for _, alias := range []string{"pub", "work", "dev"} {
		require.NoError(t, fx.Runtime().Mkdir(filepath.Join("/home/testuser/Documents/kegs", alias), 0o755, true))
		require.NoError(t, fx.Runtime().AtomicWriteFile(filepath.Join("/home/testuser/Documents/kegs", alias, "keg"), []byte(""), 0o644))
	}

	k, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
		Root: root,
	})
	require.NoError(t, err)
	// kegMap misses, so defaultKeg ("dev") should be used, NOT fallbackKeg ("pub").
	require.Equal(t, filepath.Clean("/home/testuser/Documents/kegs/dev"), filepath.Clean(k.Target.Path()))
}
