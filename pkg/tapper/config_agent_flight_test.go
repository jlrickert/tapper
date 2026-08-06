package tapper_test

import (
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"

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

func TestAgentFlight_TapAgentSelectsTheAgentsFlight(t *testing.T) {
	t.Parallel()
	tap, _ := newAgentFlightTap(t, "", map[string]string{"TAP_AGENT": "qwen"})

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "qwen", cfg.AgentName())
	require.Equal(t, "+test", cfg.Flight(),
		"the agent's flight must win over the user baseline")
}

// TAP_FLIGHT is a direct value and the agent only a reference to one, so the
// direct value wins. This is the escape hatch that lets a human override a
// launched session without editing config.
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

// Naming an agent at launch is deliberate, so it outranks an ambient project
// default. This preserves what `tap launch` did when it exported TAP_FLIGHT.
func TestAgentFlight_AgentOutranksProjectConfig(t *testing.T) {
	t.Parallel()
	tap, _ := newAgentFlightTap(t, "flight: +proj\n", map[string]string{"TAP_AGENT": "qwen"})

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "+test", cfg.Flight())
}

func TestAgentFlight_NoAgentLeavesTheCascadeAlone(t *testing.T) {
	t.Parallel()
	tap, _ := newAgentFlightTap(t, "flight: +proj\n", nil)

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "+proj", cfg.Flight())
	require.Empty(t, cfg.AgentName())
}

// An agent with no flight contributes nothing rather than clearing the
// selection, matching a launch that had no flight to export.
func TestAgentFlight_AgentWithoutFlightFallsThrough(t *testing.T) {
	t.Parallel()
	tap, _ := newAgentFlightTap(t, "flight: +proj\n", map[string]string{"TAP_AGENT": "flightless"})

	cfg, warnings, err := tap.ConfigService.Load()
	require.NoError(t, err)
	require.Equal(t, "+proj", cfg.Flight())
	require.Empty(t, warnings, "an agent may legitimately carry no flight")
}

// A stale TAP_AGENT is reported, not fatal: the session cannot fix its own
// environment, and failing hard would brick a harness over a typo.
func TestAgentFlight_UnknownAgentWarnsAndFallsThrough(t *testing.T) {
	t.Parallel()
	tap, _ := newAgentFlightTap(t, "flight: +proj\n", map[string]string{"TAP_AGENT": "ghost"})

	cfg, warnings, err := tap.ConfigService.Load()
	require.NoError(t, err)
	require.Equal(t, "+proj", cfg.Flight())

	require.Len(t, warnings, 1)
	require.Equal(t, "agent", warnings[0].Source)
	require.Contains(t, warnings[0].Message, `"ghost"`)
}

// The regression this whole mechanism exists for: a running process must see an
// edited agent flight after a reload. Exporting a resolved TAP_FLIGHT could not
// do this, because a process cannot change its own environment.
func TestAgentFlight_ReloadPicksUpAnEditedAgentFlight(t *testing.T) {
	t.Parallel()
	tap, sb := newAgentFlightTap(t, "", map[string]string{"TAP_AGENT": "qwen"})

	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "+test", cfg.Flight())

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
	require.Equal(t, "+test", cfg.Flight())

	tap.ConfigService.Reload()
	cfg, err = tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "+admin", cfg.Flight(),
		"a reload must re-resolve the agent's flight, not reuse the launch-time value")
}

func TestAgentFlight_ExplainCreditsTheAgent(t *testing.T) {
	t.Parallel()
	tap, _ := newAgentFlightTap(t, "flight: +proj\n", map[string]string{"TAP_AGENT": "qwen"})

	results, err := tap.ConfigExplain(t.Context(), tapper.ConfigExplainOptions{Field: "flight"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "+test", results[0].Value)
	require.Equal(t, `agent "qwen"`, results[0].Source,
		"explain must name the agent rather than the project config it overrode")
}
