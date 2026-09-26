package tapper_test

import (
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// newLaunchIdentityTap builds a Tap with a user flight, an optional project
// config, and an environment overlay.
func newLaunchIdentityTap(t *testing.T, projectConfig string, env map[string]string) *tapper.Tap {
	t.Helper()
	opts := []sandbox.Option{}
	for k, v := range env {
		opts = append(opts, sandbox.WithEnv(k, v))
	}
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"}, opts...)
	require.NoError(t, sb.Setwd("/home/testuser/work/project"))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/.config/tapper/config.yaml", []byte("fallbackNamespace: local\nflight: +user\n"), 0o644))
	if projectConfig != "" {
		require.NoError(t, sb.Runtime().AtomicWriteFile(
			"/home/testuser/work/project/.tapper/config.yaml", []byte(projectConfig), 0o644))
	}
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	return tap
}

// TAP_HARNESS and TAP_MODEL name the launch session and select nothing.
func TestLaunchIdentity_ReportsWithoutSelecting(t *testing.T) {
	t.Parallel()
	tap := newLaunchIdentityTap(t, "", map[string]string{"TAP_HARNESS": "codex", "TAP_MODEL": "laptop/ollama/qwen3:8b"})

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "codex", cfg.LaunchHarness())
	require.Equal(t, "laptop/ollama/qwen3:8b", cfg.LaunchModel())
	require.Equal(t, "+user", cfg.Flight())
	require.Equal(t, "codex on laptop/ollama/qwen3:8b", tap.ActiveLaunch())
}

func TestLaunchIdentity_TapFlightStillPinsTheRoot(t *testing.T) {
	t.Parallel()
	tap := newLaunchIdentityTap(t, "flight: +proj\n", map[string]string{"TAP_HARNESS": "claude", "TAP_FLIGHT": "+debug"})
	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "+debug", cfg.Flight())
}

func TestLaunchIdentity_EmptyForAHuman(t *testing.T) {
	t.Parallel()
	tap := newLaunchIdentityTap(t, "flight: +proj\n", nil)
	require.Empty(t, tap.ActiveLaunch())
	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "+proj", cfg.Flight())
}

// The identity never lands in a config file, even when the process that
// rewrites one was launched.
func TestLaunchIdentity_NeverWrittenToConfig(t *testing.T) {
	t.Parallel()
	tap := newLaunchIdentityTap(t, "", map[string]string{"TAP_HARNESS": "pi", "TAP_MODEL": "m"})
	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	out, err := cfg.ToYAML()
	require.NoError(t, err)
	require.NotContains(t, string(out), "pi")
	require.NotContains(t, string(out), "launch")
}

// Configuration from before agents were retired is kept as unknown data when
// Tapper rewrites the file, so nothing a user wrote is lost.
func TestRetiredAgentsArePreserved(t *testing.T) {
	t.Parallel()
	cfg, err := tapper.ParseConfig([]byte("flight: +top-level\nagent: qwen\nagents:\n  qwen:\n    model: ollama/qwen3.6:35b\n"))
	require.NoError(t, err)
	require.NoError(t, cfg.SetFlight("+rewritten"))
	out, err := cfg.ToYAML()
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(out, &doc))
	require.Equal(t, "+rewritten", doc["flight"])
	require.Equal(t, "qwen", doc["agent"])
	require.Contains(t, doc["agents"], "qwen")
}
