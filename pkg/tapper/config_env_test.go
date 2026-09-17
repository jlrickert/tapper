package tapper_test

import (
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestConfigService_FlightPrecedence(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	project := "/home/testuser/project"
	require.NoError(t, fx.Runtime().Mkdir(project, 0o755, true))
	require.NoError(t, fx.Setwd(project))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: project, Runtime: fx.Runtime()})
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.UserConfig(), []byte("flight: '@local/+baseline'\n"), 0o644))

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "@local/+baseline", cfg.Flight())

	require.NoError(t, fx.Runtime().AtomicWriteFile(
		filepath.Join(project, ".tapper", "config.yaml"),
		[]byte("flight: '@local/+project'\n"), 0o644))
	tap.ConfigService.Reload()
	cfg, err = tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "@local/+project", cfg.Flight(), "project config should override the user baseline")

	require.NoError(t, fx.Runtime().Env().Set("TAP_FLIGHT", "@local/+environment"))
	tap.ConfigService.Reload()
	cfg, err = tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "@local/+environment", cfg.Flight(), "TAP_FLIGHT should override project config")
}

func TestConfigService_EnvOverridesKeg(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	// Write a user config with keg = "blog".
	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.UserConfig(),
		[]byte("keg: blog\n"),
		0o644,
	))

	// Set TAP_KEG env var to override.
	require.NoError(t, fx.Runtime().Env().Set("TAP_KEG", "personal"))

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "personal", cfg.Keg(), "TAP_KEG should override user config")
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

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "debug", cfg.LogLevel(), "TAP_LOG_LEVEL should override user config")
}

func TestConfigService_RetiredNamespaceEnvIgnored(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().Env().Set("TAP_DEFAULT_NAMESPACE", "envteam"))

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	_, err = cfg.ResolveAlias(fx.Runtime(), "notes")
	require.ErrorContains(t, err, "namespace is required")
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
		[]byte("keg: blog\nlogLevel: warn\n"),
		0o644,
	))

	// No env vars set -- config file values should be used.
	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "blog", cfg.Keg(), "without env override, config file value should be used")
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
		[]byte("keg: blog\nlogLevel: info\nlogFile: /old/path.log\n"),
		0o644,
	))

	require.NoError(t, fx.Runtime().Env().Set("TAP_KEG", "work"))
	require.NoError(t, fx.Runtime().Env().Set("TAP_LOG_LEVEL", "debug"))
	require.NoError(t, fx.Runtime().Env().Set("TAP_LOG_FILE", "/new/path.log"))
	require.NoError(t, fx.Runtime().Env().Set("TAP_KEG", "personal"))
	require.NoError(t, fx.Runtime().Env().Set("TAP_HUB", "custom"))

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "personal", cfg.Keg())
	require.Equal(t, "debug", cfg.LogLevel())
	require.Equal(t, "/new/path.log", cfg.LogFile())
	require.Equal(t, "personal", cfg.Keg())
	require.Equal(t, "custom", cfg.HubName())
}

// TestConfigService_DisableAtlasHubViaEnv covers TAP_DISABLE_ATLAS_HUB
// across the truthy values parseEnvBool accepts. The env tier wins over
// file tiers (per the cascade), so a 1/true/yes/on value flips the bool
// even if the file config is silent. Empty / unset / "0" leave it false.
func TestConfigService_DisableAtlasHubViaEnv(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"1", "true", "yes", "on", "TRUE"} {
		raw := raw
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
			require.NoError(t, fx.Setwd("/home/testuser"))

			tap, err := tapper.NewTap(tapper.TapOptions{
				Root:    "/home/testuser",
				Runtime: fx.Runtime(),
			})
			require.NoError(t, err)

			require.NoError(t, fx.Runtime().Env().Set("TAP_DISABLE_ATLAS_HUB", raw))

			cfg, err := tap.ConfigService.Config()
			require.NoError(t, err)
			require.True(t, cfg.DisableAtlasHub(),
				"TAP_DISABLE_ATLAS_HUB=%q should set DisableAtlasHub", raw)
		})
	}

	t.Run("falsey value leaves bool unset", func(t *testing.T) {
		t.Parallel()
		fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
		require.NoError(t, fx.Setwd("/home/testuser"))

		tap, err := tapper.NewTap(tapper.TapOptions{
			Root:    "/home/testuser",
			Runtime: fx.Runtime(),
		})
		require.NoError(t, err)

		require.NoError(t, fx.Runtime().Env().Set("TAP_DISABLE_ATLAS_HUB", "0"))

		cfg, err := tap.ConfigService.Config()
		require.NoError(t, err)
		require.False(t, cfg.DisableAtlasHub())
	})
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
	require.NoError(t, fx.Runtime().Env().Set("TAP_KEG", "envkeg"))

	cfg, err := tap.ConfigService.Config()
	require.Error(t, err)
	require.Nil(t, cfg)
}

func TestConfigService_ConfigPathBypassesCascade(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	// Write a standalone config file.
	explicitPath := "/home/testuser/explicit-config.yaml"
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		explicitPath,
		[]byte("keg: explicit\n"),
		0o644,
	))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:       "/home/testuser",
		ConfigPath: explicitPath,
		Runtime:    fx.Runtime(),
	})
	require.NoError(t, err)

	// Set env var that should be ignored when ConfigPath is set.
	require.NoError(t, fx.Runtime().Env().Set("TAP_KEG", "envkeg"))

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, "envkeg", cfg.Keg(), "ConfigPath should bypass cascade including env vars")
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

	require.NoError(t, fx.Runtime().Env().Set("TAP_KEG", "first"))

	cfg1, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "first", cfg1.Keg())

	// Change env var, but use cache=true -- should return cached value.
	require.NoError(t, fx.Runtime().Env().Set("TAP_KEG", "second"))
	cfg2, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "first", cfg2.Keg(), "cache=true should return cached config")

	// With cache=false, should pick up new env value.
	tap.ConfigService.Reload()
	cfg3, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "second", cfg3.Keg(), "after ResetCache, should read new env value")
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

	// Write user config with keg.
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.UserConfig(),
		[]byte("keg: userkeg\n"),
		0o644,
	))

	// Write project config that overrides user config.
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.ProjectConfig(),
		[]byte("keg: projectkeg\n"),
		0o644,
	))

	// Without env, project should override user.
	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "projectkeg", cfg.Keg())

	// With env, env should override project.
	tap.ConfigService.Reload()
	require.NoError(t, fx.Runtime().Env().Set("TAP_KEG", "envkeg"))
	cfg, err = tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "envkeg", cfg.Keg(), "env should override both user and project config")
}
