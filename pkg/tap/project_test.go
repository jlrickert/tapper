package tap_test

import (
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

	req.Equal("/repo/root", p.Root())

	cfgRoot, err := p.ConfigRoot(ctx)
	req.NoError(err)
	req.Equal("/cfg", cfgRoot)

	dataRoot, err := p.DataRoot(ctx)
	req.NoError(err)
	req.Equal("/data", dataRoot)

	stateRoot, err := p.StateRoot(ctx)
	req.NoError(err)
	req.Equal("/state", stateRoot)

	cacheRoot, err := p.CacheRoot(ctx)
	req.NoError(err)
	req.Equal("/cache", cacheRoot)
}

// Tests for updating the user config for a project.

func TestProject_UserConfigUpdate_SetsDefaultKeg(t *testing.T) {
	req := require.New(t)

	fx := NewFixture(t)
	ctx := fx.Context()

	// Ensure a stable home so user config roots resolve predictably.
	// env := std.EnvFromContext(ctx)
	// req.NoError(env.SetHome(fx.AbsPath("home")))
	// req.NoError(env.SetUser("testuser"))

	// Create a project rooted at an explicit path so other roots are stable.
	wantRoot := "/repo/root"
	p, err := tap.NewProject(ctx, tap.WithRoot(wantRoot))
	req.NoError(err)

	// Update the user config to set DefaultKeg.
	err = p.UserConfigUpdate(ctx, func(cfg *tap.Config) {
		cfg.DefaultKeg = "mykeg"
	}, false)
	req.NoError(err)

	fx.MustReadJailFile("~/.config/tapper/config.yaml")

	// Read back the user config and verify the change is visible.
	got, err := p.UserConfig(ctx, false)
	req.NoError(err)
	req.Equal("mykeg", got.DefaultKeg)

	// Create a new Project instance to ensure the persisted config is re-read.
	p2, err := tap.NewProject(ctx, tap.WithRoot(wantRoot))
	req.NoError(err)
	got2, err := p2.UserConfig(ctx, false)
	req.NoError(err)
	req.Equal("mykeg", got2.DefaultKeg)
}

func TestProject_UserConfigUpdate_AppendsKegMapEntry(t *testing.T) {
	req := require.New(t)

	fx := NewFixture(t)
	ctx := fx.Context()

	// Use deterministic env values so config paths are stable.
	env := std.EnvFromContext(ctx)
	req.NoError(env.SetHome(fx.AbsPath("home")))
	req.NoError(env.SetUser("testuser"))

	p, err := tap.NewProject(ctx, tap.WithRoot("/repo/root"))
	req.NoError(err)

	// Append a KegMap entry via the update helper.
	entry := tap.KegMapEntry{
		Alias:      "alias-x",
		PathPrefix: "/projects/x",
	}
	err = p.UserConfigUpdate(ctx, func(cfg *tap.Config) {
		cfg.KegMap = append(cfg.KegMap, entry)
	}, false)
	req.NoError(err)

	// Verify the entry is present when reading the user config.
	cfg, err := p.UserConfig(ctx, false)
	req.NoError(err)

	found := false
	for _, e := range cfg.KegMap {
		if e.Alias == entry.Alias && e.PathPrefix == entry.PathPrefix {
			found = true
			break
		}
	}
	req.True(found, "expected appended KegMap entry to be present")
}
