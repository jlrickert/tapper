package mcp_test

import (
	"context"
	"testing"

	"github.com/jlrickert/cli-toolkit/toolkit"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/mcp"
)

func TestMCP_LaunchIsNamedInTheOrientationPayload(t *testing.T) {
	ctx, srv, _ := newLaunchOrientationServer(t, map[string]string{
		"TAP_HARNESS": "codex",
		"TAP_MODEL":   "laptop/ollama/qwen3:8b",
	})
	session := connectFlightSession(t, ctx, srv, nil)

	oriented := callOrient(t, ctx, session)
	require.Contains(t, oriented, "`codex on laptop/ollama/qwen3:8b`")
	require.Contains(t, oriented, "telemetry only")
	require.Contains(t, oriented, "cannot select or replace")
	require.Contains(t, oriented, "+baseline", "the launch identity selects no flight")
}

func TestMCP_HumanSessionNamesNoLaunch(t *testing.T) {
	ctx, srv, _ := newLaunchOrientationServer(t, nil)
	session := connectFlightSession(t, ctx, srv, nil)

	require.NotContains(t, callOrient(t, ctx, session), "Session launched by")
}

// A direct TAP_FLIGHT pins the launch root whatever else the launch says.
func TestMCP_TapFlightPinsTheLaunchRoot(t *testing.T) {
	ctx, srv, _ := newLaunchOrientationServer(t, map[string]string{
		"TAP_HARNESS": "claude",
		"TAP_FLIGHT":  "+alpha",
	})
	session := connectFlightSession(t, ctx, srv, nil)

	oriented := callOrient(t, ctx, session)
	require.Contains(t, oriented, "Alpha instructions")
	require.NotContains(t, oriented, "Baseline instructions")
}

// A config left over from before agents were retired, including a legacy
// per-agent flight, changes nothing about the session's root.
func TestMCP_RetiredAgentFlightIsIgnored(t *testing.T) {
	ctx, srv, rt := newLaunchOrientationServer(t, map[string]string{"TAP_HARNESS": "codex"})
	hub := orientationTestHubFor(t, rt)
	body := "keg: '@local/personal'\nhub: home\ndisableAtlasHub: true\n" +
		"hubs:\n  home:\n    url: " + hub.server.URL + "\n    tokenEnv: TAPPER_TEST_HUB_TOKEN\n" +
		"flight: +baseline\nagent: qwen\n" +
		"agents:\n  qwen:\n    model: ollama/qwen3.6:35b\n    flight: +beta\n"
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(body), 0o644))
	session := connectFlightSession(t, ctx, srv, nil)

	oriented := callOrient(t, ctx, session)
	require.Contains(t, oriented, "Baseline instructions")
	require.NotContains(t, oriented, "Beta instructions")
	require.False(t, callCat(t, ctx, session).IsError)
}

// newLaunchOrientationServer builds a config-driven session rooted at
// +baseline, with env applied on top.
func newLaunchOrientationServer(t *testing.T, env map[string]string) (context.Context, *sdkmcp.Server, *toolkit.Runtime) {
	t.Helper()
	ctx := context.Background()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Setwd("/home/testuser/project"))
	rt := sb.Runtime()
	installOrientationTestHub(t, rt)
	for k, v := range env {
		require.NoError(t, rt.Env().Set(k, v))
	}
	writeFlight(t, rt, "baseline", "Baseline instructions")
	writeFlight(t, rt, "alpha", "Alpha instructions")
	writeFlight(t, rt, "beta", "Beta instructions")
	hub := orientationTestHubFor(t, rt)
	body := "keg: '@local/personal'\nhub: home\ndisableAtlasHub: true\n" +
		"hubs:\n  home:\n    url: " + hub.server.URL + "\n    tokenEnv: TAPPER_TEST_HUB_TOKEN\n" +
		"flight: +baseline\n"
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(body), 0o644))

	tap := newMemoryTap(t, ctx, rt)
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{})
	return ctx, srv, rt
}
