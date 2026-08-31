package tapper_test

import (
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// agentFlightConfig is a user config carrying two agents: one with a flight and
// one without, so the fall-through case is expressible without a second fixture.
const agentFlightConfig = `fallbackNamespace: local
flight: +user
agents:
  qwen:
    model: ollama/qwen3.6:35b
    flight: +test
  flightless:
    model: openai/gpt-5
`

// newAgentFlightTap builds a Tap whose user config selects flights and agents,
// with an optional project config and environment overlay on top.
func newAgentFlightTap(t *testing.T, projectConfig string, env map[string]string) (*tapper.Tap, *sandbox.Sandbox) {
	t.Helper()
	opts := []sandbox.Option{}
	for k, v := range env {
		opts = append(opts, sandbox.WithEnv(k, v))
	}
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"}, opts...)
	require.NoError(t, sb.Setwd("/home/testuser/work/project"))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/.config/tapper/config.yaml", []byte(agentFlightConfig), 0o644))
	if projectConfig != "" {
		require.NoError(t, sb.Runtime().AtomicWriteFile(
			"/home/testuser/work/project/.tapper/config.yaml", []byte(projectConfig), 0o644))
	}
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	return tap, sb
}

func TestAgentFlight_TapAgentDoesNotSelectAFlight(t *testing.T) {
	t.Parallel()
	tap, _ := newAgentFlightTap(t, "", map[string]string{"TAP_AGENT": "qwen"})

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "qwen", cfg.AgentName())
	require.Equal(t, "+user", cfg.Flight(),
		"TAP_AGENT is model selection and telemetry only")
}

// TAP_FLIGHT is the direct immutable launch-root reference.
func TestAgentFlight_TapFlightOutranksTheAgent(t *testing.T) {
	t.Parallel()
	tap, _ := newAgentFlightTap(t, "", map[string]string{
		"TAP_AGENT":  "qwen",
		"TAP_FLIGHT": "+debug",
	})

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "+debug", cfg.Flight())
}

func TestAgentFlight_ProjectFlightIsIndependentOfAgent(t *testing.T) {
	t.Parallel()
	tap, _ := newAgentFlightTap(t, "flight: +proj\n", map[string]string{"TAP_AGENT": "qwen"})

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "+proj", cfg.Flight())
}

func TestAgentFlight_NoAgentLeavesTheCascadeAlone(t *testing.T) {
	t.Parallel()
	tap, _ := newAgentFlightTap(t, "flight: +proj\n", nil)

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "+proj", cfg.Flight())
	require.Empty(t, cfg.AgentName())
}

// An agent's legacy flight field is ignored whether it is present or absent.
func TestAgentFlight_AgentWithoutFlightFallsThrough(t *testing.T) {
	t.Parallel()
	tap, _ := newAgentFlightTap(t, "flight: +proj\n", map[string]string{"TAP_AGENT": "flightless"})

	cfg, warnings, err := tap.ConfigService.Load()
	require.NoError(t, err)
	require.Equal(t, "+proj", cfg.Flight())
	require.Empty(t, warnings, "an agent may legitimately carry no flight")
}

func TestAgentFlight_UnknownAgentDoesNotAffectFlight(t *testing.T) {
	t.Parallel()
	tap, _ := newAgentFlightTap(t, "flight: +proj\n", map[string]string{"TAP_AGENT": "ghost"})

	cfg, warnings, err := tap.ConfigService.Load()
	require.NoError(t, err)
	require.Equal(t, "+proj", cfg.Flight())

	require.Empty(t, warnings)
}

func TestAgentFlight_ReloadDoesNotAdoptEditedAgentFlight(t *testing.T) {
	t.Parallel()
	tap, sb := newAgentFlightTap(t, "", map[string]string{"TAP_AGENT": "qwen"})

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "+user", cfg.Flight())

	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/.config/tapper/config.yaml",
		[]byte(`fallbackNamespace: local
flight: +user
agents:
  qwen:
    model: ollama/qwen3.6:35b
    flight: +admin
`), 0o644))

	// Still the old value: configuration is fixed until something reloads.
	cfg, err = tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "+user", cfg.Flight())

	tap.ConfigService.Reload()
	cfg, err = tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "+user", cfg.Flight(),
		"reorientation refreshes the root manifest, not the root selection")
}

func TestAgentFlight_ExplainCreditsTheProject(t *testing.T) {
	t.Parallel()
	tap, _ := newAgentFlightTap(t, "flight: +proj\n", map[string]string{"TAP_AGENT": "qwen"})

	results, err := tap.ConfigExplain(t.Context(), tapper.ConfigExplainOptions{Field: "flight"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "+proj", results[0].Value)
	require.Equal(t, "project config", results[0].Source)
}

func TestAgentFlight_LegacyFieldIsIgnoredAndPreserved(t *testing.T) {
	t.Parallel()

	cfg, err := tapper.ParseConfig([]byte("flight: +top-level\n" +
		"agents:\n" +
		"  qwen:\n" +
		"    model: ollama/qwen3.6:35b\n" +
		"    flight: +legacy-agent\n"))
	require.NoError(t, err)
	require.Equal(t, "+top-level", cfg.Flight())
	require.NoError(t, cfg.SetFlight("+rewritten-top-level"))

	out, err := cfg.ToYAML()
	require.NoError(t, err)
	var doc map[string]any
	require.NoError(t, yaml.Unmarshal(out, &doc))
	agents := doc["agents"].(map[string]any)
	qwen := agents["qwen"].(map[string]any)
	require.Equal(t, "+legacy-agent", qwen["flight"])
	require.Equal(t, "+rewritten-top-level", doc["flight"])
}
