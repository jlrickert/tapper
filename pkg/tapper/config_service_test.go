package tapper_test

import (
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestConfigService_Config_MissingFilesReturnDefaults(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	cfg, err := tap.ConfigService.Config(false)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Empty(t, tap.ConfigService.LoadWarnings, "missing files should not produce warnings")
}

func TestConfigService_Config_CorruptUserConfig(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	// Write corrupt YAML to the user config path.
	userCfgPath := tap.PathService.UserConfig()
	require.NoError(t, fx.Runtime().AtomicWriteFile(userCfgPath, []byte(":::invalid yaml{{{"), 0o644))

	cfg, err := tap.ConfigService.Config(false)
	require.NoError(t, err, "corrupt config should not return error (graceful degradation)")
	require.NotNil(t, cfg)
	require.Len(t, tap.ConfigService.LoadWarnings, 1)
	require.Contains(t, tap.ConfigService.LoadWarnings[0].Message, "failed to load user config")
	require.Equal(t, "user config", tap.ConfigService.LoadWarnings[0].Source)
}

func TestConfigService_Config_CorruptProjectConfig(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	// Write corrupt YAML to the project config path.
	projCfgPath := tap.PathService.ProjectConfig()
	require.NoError(t, fx.Runtime().AtomicWriteFile(projCfgPath, []byte("not: [valid: yaml"), 0o644))

	cfg, err := tap.ConfigService.Config(false)
	require.NoError(t, err, "corrupt config should not return error (graceful degradation)")
	require.NotNil(t, cfg)
	require.Len(t, tap.ConfigService.LoadWarnings, 1)
	require.Contains(t, tap.ConfigService.LoadWarnings[0].Message, "failed to load project config")
	require.Equal(t, "project config", tap.ConfigService.LoadWarnings[0].Source)
}

func TestConfigService_Config_BothCorrupt(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(":::bad"), 0o644))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.ProjectConfig(), []byte(":::bad"), 0o644))

	cfg, err := tap.ConfigService.Config(false)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, tap.ConfigService.LoadWarnings, 2)
}

func TestConfigService_Config_ValidUserCorruptProject(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte("defaultKeg: pub\n"), 0o644))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.ProjectConfig(), []byte(":::bad"), 0o644))

	cfg, err := tap.ConfigService.Config(false)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, tap.ConfigService.LoadWarnings, 1)
	require.Equal(t, "project config", tap.ConfigService.LoadWarnings[0].Source)
	// Valid user config should still be used.
	require.Equal(t, "pub", cfg.DefaultKeg())
}

func TestConfigService_ResetCache_ClearsWarnings(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(":::bad"), 0o644))

	_, _ = tap.ConfigService.Config(false)
	require.Len(t, tap.ConfigService.LoadWarnings, 1)

	tap.ConfigService.ResetCache()
	require.Empty(t, tap.ConfigService.LoadWarnings)
}
