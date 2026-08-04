package tapper_test

import (
	"strings"
	"sync"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// loadWarnings returns the non-fatal issues from a fresh read of the cascade.
// Warnings travel with the config they came from now, so tests ask for both
// together rather than reading a field left behind by the last call.
func loadWarnings(t *testing.T, tap *tapper.Tap) []tapper.ConfigLoadWarning {
	t.Helper()
	_, warnings, err := tap.ConfigService.Load()
	require.NoError(t, err)
	return warnings
}

func TestConfigService_Config_MissingFilesReturnDefaults(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Empty(t, loadWarnings(t, tap), "missing files should not produce warnings")
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

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err, "corrupt config should not return error (graceful degradation)")
	require.NotNil(t, cfg)
	require.Len(t, loadWarnings(t, tap), 1)
	require.Contains(t, loadWarnings(t, tap)[0].Message, "failed to load user config")
	require.Equal(t, "user config", loadWarnings(t, tap)[0].Source)
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

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err, "corrupt config should not return error (graceful degradation)")
	require.NotNil(t, cfg)
	require.Len(t, loadWarnings(t, tap), 1)
	require.Contains(t, loadWarnings(t, tap)[0].Message, "failed to load project config")
	require.Equal(t, "project config", loadWarnings(t, tap)[0].Source)
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

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, loadWarnings(t, tap), 2)
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

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, loadWarnings(t, tap), 1)
	require.Equal(t, "project config", loadWarnings(t, tap)[0].Source)
	// Valid user config should still be used.
	require.Equal(t, "pub", cfg.DefaultKeg())
}

func TestConfigService_ProjectConfig_WalksParents(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser/a/b"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser/a/b",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	// Shallow ancestor sets defaultKeg and fallbackKeg.
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		"/home/testuser/a/.tapper/config.yaml",
		[]byte("defaultKeg: shallow\nfallbackKeg: keep\n"), 0o644))
	// Deeper dir overrides defaultKeg only.
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		"/home/testuser/a/b/.tapper/config.yaml",
		[]byte("defaultKeg: deep\n"), 0o644))

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "deep", cfg.DefaultKeg(), "deeper dir overrides shallower")
	require.Equal(t, "keep", cfg.FallbackKeg(), "shallower value retained when not overridden")
}

func TestConfigService_ProjectConfig_StripsHubsAndWarns(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser/proj"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser/proj",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	// A project config that tries to define a hub with a token env must be
	// ignored (hubs/credentials are user-config only) while its other fields
	// still apply.
	proj := `defaultKeg: ok
hubs:
  evil:
    kind: remote
    url: https://evil.example.com
    tokenEnv: SECRET
`
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		"/home/testuser/proj/.tapper/config.yaml", []byte(proj), 0o644))

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "ok", cfg.DefaultKeg(), "non-hub project fields still apply")
	_, ok := cfg.Hubs()["evil"]
	require.False(t, ok, "project-defined hub must be stripped")

	found := false
	for _, w := range loadWarnings(t, tap) {
		if strings.Contains(w.Message, "evil") && strings.Contains(w.Message, "hubs") {
			found = true
		}
	}
	require.True(t, found, "expected a warning about the stripped hub, got %+v", loadWarnings(t, tap))
}

// TestConfigService_SnapshotIsFixedUntilReload pins the contract the whole
// design rests on: configuration is read once and stays put, and Reload is the
// only thing that adopts an edit. Orientation is its sole production caller, so
// this is what "edit the file, then reorient" means underneath.
func TestConfigService_SnapshotIsFixedUntilReload(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.UserConfig(), []byte("defaultKeg: before\n"), 0o644))

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "before", cfg.DefaultKeg())

	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.UserConfig(), []byte("defaultKeg: after\n"), 0o644))

	cfg, err = tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "before", cfg.DefaultKeg(), "an edit must not leak into a live snapshot")

	tap.ConfigService.Reload()
	cfg, err = tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "after", cfg.DefaultKeg(), "Reload adopts the edit")
}

// TestConfigService_TiersComeFromTheSameSnapshot guards against the tier
// accessors drifting from the merged config they were resolved with, and
// against ReadUserConfigFile quietly being wired to the snapshot — write paths
// depend on it reporting what is actually on disk.
func TestConfigService_TiersComeFromTheSameSnapshot(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.UserConfig(), []byte("defaultKeg: before\n"), 0o644))

	user, err := tap.ConfigService.UserConfig()
	require.NoError(t, err)
	require.Equal(t, "before", user.DefaultKeg())

	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.UserConfig(), []byte("defaultKeg: after\n"), 0o644))

	user, err = tap.ConfigService.UserConfig()
	require.NoError(t, err)
	require.Equal(t, "before", user.DefaultKeg(), "tier reads share the snapshot")

	onDisk, err := tap.ConfigService.ReadUserConfigFile()
	require.NoError(t, err)
	require.Equal(t, "after", onDisk.DefaultKeg(), "ReadUserConfigFile bypasses the snapshot")
}

// TestConfigService_ConcurrentAccessIsRaceFree covers the one place tapper
// really is concurrent: the MCP SDK dispatches every call except initialize
// asynchronously, so overlapping tool calls read this service while an orient
// may be reloading it. Meaningful only under -race.
func TestConfigService_ConcurrentAccessIsRaceFree(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				_, _ = tap.ConfigService.Config()
				_, _ = tap.ConfigService.UserConfig()
				_, _ = tap.ConfigService.ProjectConfig()
				_, _, _ = tap.ConfigService.Load()
				tap.ConfigService.Reload()
			}
		}()
	}
	wg.Wait()
}
