package tapper_test

import (
	"context"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestConfigExplain_UserConfigSource(t *testing.T) {
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
		[]byte("defaultKeg: pub\n"),
		0o644,
	))

	results, err := tap.ConfigExplain(context.Background(), tapper.ConfigExplainOptions{
		Field: "defaultKeg",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "defaultKeg", results[0].Field)
	require.Equal(t, "pub", results[0].Value)
	require.Equal(t, "user config", results[0].Source)
}

func TestConfigExplain_ProjectConfigOverridesUser(t *testing.T) {
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
		[]byte("defaultKeg: userkeg\n"),
		0o644,
	))
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.ProjectConfig(),
		[]byte("defaultKeg: projectkeg\n"),
		0o644,
	))

	results, err := tap.ConfigExplain(context.Background(), tapper.ConfigExplainOptions{
		Field: "defaultKeg",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "projectkeg", results[0].Value)
	require.Equal(t, "project config", results[0].Source)
}

func TestConfigExplain_EnvVarOverridesAll(t *testing.T) {
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
		[]byte("defaultKeg: userkeg\n"),
		0o644,
	))
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.ProjectConfig(),
		[]byte("defaultKeg: projectkeg\n"),
		0o644,
	))
	require.NoError(t, fx.Runtime().Env().Set("TAP_DEFAULT_KEG", "envkeg"))

	results, err := tap.ConfigExplain(context.Background(), tapper.ConfigExplainOptions{
		Field: "defaultKeg",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "envkeg", results[0].Value)
	require.Equal(t, "env vars", results[0].Source)
}

func TestConfigExplain_DefaultWhenNoSourceSets(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	// No config files or env vars set.
	results, err := tap.ConfigExplain(context.Background(), tapper.ConfigExplainOptions{
		Field: "logLevel",
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "logLevel", results[0].Field)
	require.Equal(t, "", results[0].Value)
	require.Equal(t, "default", results[0].Source)
}

func TestConfigExplain_AllFields(t *testing.T) {
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
		[]byte("defaultKeg: pub\nlogLevel: info\n"),
		0o644,
	))
	require.NoError(t, fx.Runtime().Env().Set("TAP_LOG_FILE", "/tmp/tap.log"))

	results, err := tap.ConfigExplain(context.Background(), tapper.ConfigExplainOptions{})
	require.NoError(t, err)
	require.Len(t, results, len(tapper.ConfigExplainFields),
		"should return one entry per ConfigExplainFields")

	// Build a map for easier assertions.
	byField := map[string]tapper.ConfigExplainResult{}
	for _, r := range results {
		byField[r.Field] = r
	}

	require.Equal(t, "pub", byField["defaultKeg"].Value)
	require.Equal(t, "user config", byField["defaultKeg"].Source)

	require.Equal(t, "info", byField["logLevel"].Value)
	require.Equal(t, "user config", byField["logLevel"].Source)

	require.Equal(t, "/tmp/tap.log", byField["logFile"].Value)
	require.Equal(t, "env vars", byField["logFile"].Source)

	require.Equal(t, "", byField["fallbackKeg"].Value)
	require.Equal(t, "default", byField["fallbackKeg"].Source)
}

func TestConfigExplain_UnknownField(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("basic", "/home/testuser"))
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	_, err = tap.ConfigExplain(context.Background(), tapper.ConfigExplainOptions{
		Field: "nonexistent",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown config field")
}

// The ResolvedSources accessor was removed along with the state behind it: it
// had no production consumer. Which tier supplied a field is still reported by
// `tap config --explain`, which derives it from the tiers themselves
// (configFieldScope) rather than from cascade bookkeeping — see
// TestConfigCommand_ExplainFlag above.
