package tapper_test

import (
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestConfigService_EnvOverridesDefaultKeg(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	// Write a user config with defaultKeg = "blog".
	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.UserConfig(),
		[]byte("defaultKeg: blog\n"),
		0o644,
	))

	// Set TAP_DEFAULT_KEG env var to override.
	require.NoError(t, fx.Runtime().Env().Set("TAP_DEFAULT_KEG", "personal"))

	cfg, err := tap.ConfigService.Config(false)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "personal", cfg.DefaultKeg(), "TAP_DEFAULT_KEG should override user config")
}

func TestConfigService_EnvOverridesLogLevel(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.UserConfig(),
		[]byte("logLevel: info\n"),
		0o644,
	))

	require.NoError(t, fx.Runtime().Env().Set("TAP_LOG_LEVEL", "debug"))

	cfg, err := tap.ConfigService.Config(false)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "debug", cfg.LogLevel(), "TAP_LOG_LEVEL should override user config")
}

func TestConfigService_EnvKegSearchPathsColonSeparated(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().Env().Set("TAP_KEG_SEARCH_PATHS", "/path/a:/path/b:/path/c"))

	cfg, err := tap.ConfigService.Config(false)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, []string{"/path/a", "/path/b", "/path/c"}, cfg.KegSearchPaths())
}

func TestConfigService_EnvAbsentFallsThrough(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.UserConfig(),
		[]byte("defaultKeg: blog\nlogLevel: warn\n"),
		0o644,
	))

	// No env vars set -- config file values should be used.
	cfg, err := tap.ConfigService.Config(false)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "blog", cfg.DefaultKeg(), "without env override, config file value should be used")
	require.Equal(t, "warn", cfg.LogLevel(), "without env override, config file value should be used")
}

func TestConfigService_MultipleEnvVarsSet(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.UserConfig(),
		[]byte("defaultKeg: blog\nlogLevel: info\nlogFile: /old/path.log\n"),
		0o644,
	))

	require.NoError(t, fx.Runtime().Env().Set("TAP_DEFAULT_KEG", "work"))
	require.NoError(t, fx.Runtime().Env().Set("TAP_LOG_LEVEL", "debug"))
	require.NoError(t, fx.Runtime().Env().Set("TAP_LOG_FILE", "/new/path.log"))
	require.NoError(t, fx.Runtime().Env().Set("TAP_FALLBACK_KEG", "personal"))
	require.NoError(t, fx.Runtime().Env().Set("TAP_DEFAULT_REGISTRY", "custom"))

	cfg, err := tap.ConfigService.Config(false)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "work", cfg.DefaultKeg())
	require.Equal(t, "debug", cfg.LogLevel())
	require.Equal(t, "/new/path.log", cfg.LogFile())
	require.Equal(t, "personal", cfg.FallbackKeg())
	require.Equal(t, "custom", cfg.DefaultRegistry())
}

func TestConfigService_EnvOverrideWithStrict(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	// Write corrupt user config.
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.UserConfig(),
		[]byte(":::invalid yaml{{{"),
		0o644,
	))

	// Set env var -- should still work even with corrupt config.
	require.NoError(t, fx.Runtime().Env().Set("TAP_DEFAULT_KEG", "envkeg"))

	cfg, err := tap.ConfigService.Config(false)
	require.NoError(t, err, "env overrides should still work with corrupt config")
	require.NotNil(t, cfg)

	// Env var value should be present.
	require.Equal(t, "envkeg", cfg.DefaultKeg())

	// The corrupt user config should produce a load warning.
	require.Len(t, tap.ConfigService.LoadWarnings, 1)
	require.Equal(t, "user config", tap.ConfigService.LoadWarnings[0].Source)
}

func TestConfigService_ConfigPathBypassesCascade(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	// Write a standalone config file.
	explicitPath := "/home/testuser/explicit-config.yaml"
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		explicitPath,
		[]byte("defaultKeg: explicit\n"),
		0o644,
	))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:       "/home/testuser",
		ConfigPath: explicitPath,
		Runtime:    fx.Runtime(),
	})
	require.NoError(t, err)

	// Set env var that should be ignored when ConfigPath is set.
	require.NoError(t, fx.Runtime().Env().Set("TAP_DEFAULT_KEG", "envkeg"))

	cfg, err := tap.ConfigService.Config(false)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "explicit", cfg.DefaultKeg(), "ConfigPath should bypass cascade including env vars")
}

func TestConfigService_CachingPreserved(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().Env().Set("TAP_DEFAULT_KEG", "first"))

	cfg1, err := tap.ConfigService.Config(false)
	require.NoError(t, err)
	require.Equal(t, "first", cfg1.DefaultKeg())

	// Change env var, but use cache=true -- should return cached value.
	require.NoError(t, fx.Runtime().Env().Set("TAP_DEFAULT_KEG", "second"))
	cfg2, err := tap.ConfigService.Config(true)
	require.NoError(t, err)
	require.Equal(t, "first", cfg2.DefaultKeg(), "cache=true should return cached config")

	// With cache=false, should pick up new env value.
	tap.ConfigService.ResetCache()
	cfg3, err := tap.ConfigService.Config(false)
	require.NoError(t, err)
	require.Equal(t, "second", cfg3.DefaultKeg(), "after ResetCache, should read new env value")
}

func TestConfigService_EnvOverridesProjectConfig(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	// Write user config with defaultKeg.
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.UserConfig(),
		[]byte("defaultKeg: userkeg\n"),
		0o644,
	))

	// Write project config that overrides user config.
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.ProjectConfig(),
		[]byte("defaultKeg: projectkeg\n"),
		0o644,
	))

	// Without env, project should override user.
	cfg, err := tap.ConfigService.Config(false)
	require.NoError(t, err)
	require.Equal(t, "projectkeg", cfg.DefaultKeg())

	// With env, env should override project.
	tap.ConfigService.ResetCache()
	require.NoError(t, fx.Runtime().Env().Set("TAP_DEFAULT_KEG", "envkeg"))
	cfg, err = tap.ConfigService.Config(false)
	require.NoError(t, err)
	require.Equal(t, "envkeg", cfg.DefaultKeg(), "env should override both user and project config")
}
