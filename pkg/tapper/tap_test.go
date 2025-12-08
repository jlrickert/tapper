package tapper_test

import (
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// Integration tests for Tap configuration and keg resolution

func TestTap_Context_CreatesValidTapContext(t *testing.T) {
	req := require.New(t)

	fx := NewSandbox(t)
	ctx := fx.Context()

	tap, err := tapper.NewTap(ctx)
	req.NoError(err)

	tCtx := tap.Context()

	req.Equal(tap.Root, tCtx.Root)
	req.Equal(fx.ResolvePath(filepath.Join(".config", tapper.DefaultAppName)), tCtx.ConfigRoot)
	req.Equal(fx.ResolvePath(filepath.Join(".local", "share", tapper.DefaultAppName)), tCtx.DataRoot)
	req.Equal(fx.ResolvePath(filepath.Join(".local", "state", tapper.DefaultAppName)), tCtx.StateRoot)
	req.Equal(fx.ResolvePath(filepath.Join(".cache", tapper.DefaultAppName)), tCtx.CacheRoot)
}

// Integration tests for updating the user config for a project.

func TestTap_UserConfigUpdate_SetsDefaultKeg(t *testing.T) {
	req := require.New(t)

	fx := NewSandbox(t)
	ctx := fx.Context()

	tap, err := tapper.NewTap(ctx)
	req.NoError(err)

	tCtx := tap.Context()

	// Update the user config to set DefaultKeg.
	err = tCtx.UserConfigUpdate(ctx, func(cfg *tapper.Config) {
		cfg.SetDefaultKeg("mykeg")
	}, false)
	req.NoError(err)

	fx.MustReadFile("~/.config/tapper/config.yaml")

	// Read back the user config and verify the change is visible.
	got, err := tCtx.UserConfig(ctx, false)
	req.NoError(err)
	req.Equal("mykeg", got.DefaultKeg())

	// Create a new Tap instance to ensure the persisted config is re-read.
	tap2, err := tapper.NewTap(ctx)
	req.NoError(err)
	p2 := tap2.Context()
	got2, err := p2.UserConfig(ctx, false)
	req.NoError(err)
	req.Equal("mykeg", got2.DefaultKeg())
}

func TestTap_UserConfigUpdate_AppendsKegMapEntry(t *testing.T) {
	req := require.New(t)

	fx := NewSandbox(t)
	ctx := fx.Context()

	// Use deterministic env values so config paths are stable.
	env := toolkit.EnvFromContext(ctx)
	req.NoError(env.SetHome(fx.AbsPath("home")))
	req.NoError(env.SetUser("testuser"))

	tap, err := tapper.NewTap(ctx)
	req.NoError(err)

	p := tap.Context()

	// Append a KegMap entry via the update helper.
	entry := tapper.KegMapEntry{
		Alias:      "alias-x",
		PathPrefix: "/projects/x",
	}
	err = p.UserConfigUpdate(ctx, func(cfg *tapper.Config) {
		cfg.AddKegMap(entry)
	}, false)
	req.NoError(err)

	// Verify the entry is present when reading the user config.
	cfg, err := p.UserConfig(ctx, false)
	req.NoError(err)

	found := false
	for _, e := range cfg.KegMap() {
		if e.Alias == entry.Alias && e.PathPrefix == entry.PathPrefix {
			found = true
			break
		}
	}
	req.True(found, "expected appended KegMap entry to be present")
}
