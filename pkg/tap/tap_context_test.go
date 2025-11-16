package tap_test

import (
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/tap"
)

func TestNewProject_WithOptions(t *testing.T) {
	req := require.New(t)

	fx := NewSandbox(t)
	ctx := fx.Context()

	p, err := tap.NewTapContext(ctx, "/repo/root")
	req.NoError(err)

	req.Equal("/repo/root", p.Root)
	req.Equal(fx.ResolvePath(filepath.Join(".config", tap.DefaultAppName)), p.ConfigRoot)
	req.Equal(fx.ResolvePath(filepath.Join(".local", "share", tap.DefaultAppName)), p.DataRoot)
	req.Equal(fx.ResolvePath(filepath.Join(".local", "state", tap.DefaultAppName)), p.StateRoot)
	req.Equal(fx.ResolvePath(filepath.Join(".cache", tap.DefaultAppName)), p.CacheRoot)
}

// Tests for updating the user config for a project.

func TestProject_UserConfigUpdate_SetsDefaultKeg(t *testing.T) {
	req := require.New(t)

	fx := NewSandbox(t)
	ctx := fx.Context()

	// Ensure a stable home so user config roots resolve predictably.
	// env := std.EnvFromContext(ctx)
	// req.NoError(env.SetHome(fx.AbsPath("home")))
	// req.NoError(env.SetUser("testuser"))

	// Create a project rooted at an explicit path so other roots are stable.
	wantRoot := "/repo/root"
	p, err := tap.NewTapContext(ctx, wantRoot)
	req.NoError(err)

	// Update the user config to set DefaultKeg.
	err = p.UserConfigUpdate(ctx, func(cfg *tap.Config) {
		cfg.DefaultKeg = "mykeg"
	}, false)
	req.NoError(err)

	fx.MustReadFile("~/.config/tapper/config.yaml")

	// Read back the user config and verify the change is visible.
	got, err := p.UserConfig(ctx, false)
	req.NoError(err)
	req.Equal("mykeg", got.DefaultKeg)

	// Create a new Project instance to ensure the persisted config is re-read.
	p2, err := tap.NewTapContext(ctx, wantRoot)
	req.NoError(err)
	got2, err := p2.UserConfig(ctx, false)
	req.NoError(err)
	req.Equal("mykeg", got2.DefaultKeg)
}

func TestProject_UserConfigUpdate_AppendsKegMapEntry(t *testing.T) {
	req := require.New(t)

	fx := NewSandbox(t)
	ctx := fx.Context()

	// Use deterministic env values so config paths are stable.
	env := toolkit.EnvFromContext(ctx)
	req.NoError(env.SetHome(fx.AbsPath("home")))
	req.NoError(env.SetUser("testuser"))

	p, err := tap.NewTapContext(ctx, "/repo/root")
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
