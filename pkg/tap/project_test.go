package tap_test

import (
	"path/filepath"
	"testing"

	std "github.com/jlrickert/go-std/pkg"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/tap"
)

func TestNewProject_WithOptions(t *testing.T) {
	req := require.New(t)

	fx := NewFixture(t)
	ctx := fx.Context()

	p, err := tap.NewProject(
		ctx,
		tap.WithRoot("/repo/root"),
		tap.WithConfigRoot("/cfg"),
		tap.WithDataRoot("/data"),
		tap.WithStateRoot("/state"),
		tap.WithCacheRoot("/cache"),
	)
	req.NoError(err)

	req.Equal("/repo/root", p.Root)
	req.Equal("/cfg", p.ConfigRoot)
	req.Equal("/data", p.DataRoot)
	req.Equal("/state", p.StateRoot)
	req.Equal("/cache", p.CacheRoot)
}

func TestNewProject_DefaultsFromEnv(t *testing.T) {
	req := require.New(t)

	fx := NewFixture(t)

	// Make the fixture use a deterministic home and username.
	env := std.EnvFromContext(fx.Context())
	req.NoError(env.SetHome("/home/testuser"))
	req.NoError(env.SetUser("testuser"))

	// Provide a Root so NewProject skips env.GetWd error paths but still
	// computes Config/Data/State/Cache roots from the injected env.
	wantRoot := "/repo/root"
	p, err := tap.NewProject(fx.Context(), tap.WithRoot(wantRoot))
	req.NoError(err)

	cfgBase, err := std.UserConfigPath(fx.Context())
	req.NoError(err)
	dataBase, err := std.UserDataPath(fx.Context())
	req.NoError(err)
	stateBase, err := std.UserStatePath(fx.Context())
	req.NoError(err)
	cacheBase, err := std.UserCachePath(fx.Context())
	req.NoError(err)

	req.Equal(wantRoot, p.Root)
	req.Equal(filepath.Join(cfgBase, tap.DefaultAppName), p.ConfigRoot)
	req.Equal(filepath.Join(dataBase, tap.DefaultAppName), p.DataRoot)
	req.Equal(filepath.Join(stateBase, tap.DefaultAppName), p.StateRoot)
	req.Equal(filepath.Join(cacheBase, tap.DefaultAppName), p.CacheRoot)
}

func TestWithCurrentProjectSetsRoot(t *testing.T) {
	req := require.New(t)

	fx := NewFixture(t)

	// Set an explicit working directory so the option can discover it via
	// EnvFromContext.
	env := std.EnvFromContext(fx.Context())
	env.Setwd("/some/repo/path")

	p := &tap.Project{}
	opt := tap.WithAutoRootDetect(fx.Context())
	opt(p)

	wd, err := env.Getwd()
	req.NoError(err)
	want := std.FindGitRoot(fx.Context(), wd)
	req.Equal(want, p.Root)
}
