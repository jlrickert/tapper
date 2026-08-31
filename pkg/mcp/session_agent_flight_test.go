package mcp_test

import (
	"context"
	"testing"

	"github.com/jlrickert/cli-toolkit/toolkit"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/mcp"
)

func TestMCP_AgentFlightDoesNotMoveConnectionPinnedRoot(t *testing.T) {
	ctx, srv, rt := newAgentOrientationServer(t, "qwen")
	session := connectFlightSession(t, ctx, srv, nil)

	requireConnectionInstructions(t, session.InitializeResult().Instructions)

	writeAgentFlight(t, rt, "qwen", "beta")

	oriented := callOrient(t, ctx, session)
	require.Contains(t, oriented, "+baseline")
	require.Contains(t, oriented, "Baseline instructions")
	require.NotContains(t, oriented, "Beta instructions")
}

func TestMCP_AgentIsNamedInTheOrientationPayload(t *testing.T) {
	ctx, srv, _ := newAgentOrientationServer(t, "qwen")
	session := connectFlightSession(t, ctx, srv, nil)

	oriented := callOrient(t, ctx, session)
	require.Contains(t, oriented, "agent `qwen`")
	require.Contains(t, oriented, "model and telemetry identity")
	require.Contains(t, oriented, "cannot select or replace")
}

// A direct TAP_FLIGHT pins the launch root independently of TAP_AGENT.
func TestMCP_TapFlightOverridesTheAgentInSession(t *testing.T) {
	ctx, srv, _ := newAgentOrientationServerWithEnv(t, map[string]string{
		"TAP_AGENT":  "qwen",
		"TAP_FLIGHT": "+baseline",
	})
	session := connectFlightSession(t, ctx, srv, nil)

	require.Contains(t, callOrient(t, ctx, session), "+baseline")
}

func TestMCP_UnknownAgentDoesNotAffectFlightAuthority(t *testing.T) {
	ctx, srv, _ := newAgentOrientationServer(t, "ghost")
	session := connectFlightSession(t, ctx, srv, nil)

	oriented := callOrient(t, ctx, session)
	require.Contains(t, oriented, "agent `ghost`")
	require.NotContains(t, oriented, "not configured")
	require.Contains(t, oriented, "+baseline")
	require.False(t, callCat(t, ctx, session).IsError)
}

func newAgentOrientationServer(t *testing.T, agent string) (context.Context, *sdkmcp.Server, *toolkit.Runtime) {
	t.Helper()
	return newAgentOrientationServerWithEnv(t, map[string]string{"TAP_AGENT": agent})
}

// newAgentOrientationServer builds a config-driven session where TAP_AGENT is
// independent of the user-configured root.
func newAgentOrientationServerWithEnv(t *testing.T, env map[string]string) (context.Context, *sdkmcp.Server, *toolkit.Runtime) {
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
	// Legacy per-agent flight values are intentionally ignored.
	writeAgentFlight(t, rt, "qwen", "alpha")

	tap := newMemoryTap(t, ctx, rt)
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{})
	return ctx, srv, rt
}

// writeAgentFlight rewrites a legacy per-agent flight while retaining the
// independent baseline root.
func writeAgentFlight(t *testing.T, rt *toolkit.Runtime, name, slug string) {
	t.Helper()
	hub := orientationTestHubFor(t, rt)
	body := "defaultKeg: personal\nfallbackHub: home\nfallbackNamespace: local\ndisableAtlasHub: true\n" +
		"namespaces:\n  local:\n    hub: home\n" +
		"hubs:\n  home:\n    kind: remote\n    url: " + hub.server.URL + "\n    tokenEnv: TAPPER_TEST_HUB_TOKEN\n" +
		"flight: +baseline\n" +
		"agents:\n  " + name + ":\n    model: ollama/qwen3.6:35b\n    flight: +" + slug + "\n"
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(body), 0o644))
}
