package mcp_test

import (
	"context"
	"testing"

	"github.com/jlrickert/cli-toolkit/toolkit"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// TestMCP_AgentFlightMovesWithConfig is the regression this whole mechanism
// exists for. `tap launch` used to export the agent's flight as TAP_FLIGHT, so
// a running session could never leave it: env outranks project and user config,
// and a process cannot change its own environment. Editing the agent's flight
// and re-orienting silently did nothing, while the session reported success.
//
// Exporting TAP_AGENT instead makes the flight a reference resolved on every
// orientation, so the edit lands.
func TestMCP_AgentFlightMovesWithConfig(t *testing.T) {
	ctx, srv, rt := newAgentOrientationServer(t, "qwen")
	session := connectFlightSession(t, ctx, srv, nil)

	require.Contains(t, session.InitializeResult().Instructions, "+alpha")
	require.Contains(t, session.InitializeResult().Instructions, "Alpha instructions")

	writeAgentFlight(t, rt, "qwen", "beta")

	oriented := callOrient(t, ctx, session)
	require.Contains(t, oriented, "+beta")
	require.Contains(t, oriented, "Beta instructions")
	require.NotContains(t, oriented, "Alpha instructions")
}

// The payload names the agent, so a reader who wants a different flight is told
// where the current one came from instead of being pointed at a `flight:` key
// the agent silently outranks.
func TestMCP_AgentIsNamedInTheOrientationPayload(t *testing.T) {
	ctx, srv, _ := newAgentOrientationServer(t, "qwen")
	session := connectFlightSession(t, ctx, srv, nil)

	oriented := callOrient(t, ctx, session)
	require.Contains(t, oriented, "agent `qwen`")
	require.Contains(t, oriented, "call `orient` again")
}

// A direct TAP_FLIGHT still wins, which is the escape hatch for overriding a
// launched session without touching config.
func TestMCP_TapFlightOverridesTheAgentInSession(t *testing.T) {
	ctx, srv, _ := newAgentOrientationServerWithEnv(t, map[string]string{
		"TAP_AGENT":  "qwen",
		"TAP_FLIGHT": "+baseline",
	})
	session := connectFlightSession(t, ctx, srv, nil)

	require.Contains(t, callOrient(t, ctx, session), "+baseline")
}

// A stale agent name is reported in the payload rather than locking the
// session: the agent cannot edit its own environment to fix it.
func TestMCP_UnknownAgentWarnsButKeepsTheSessionUsable(t *testing.T) {
	ctx, srv, _ := newAgentOrientationServer(t, "ghost")
	session := connectFlightSession(t, ctx, srv, nil)

	oriented := callOrient(t, ctx, session)
	require.Contains(t, oriented, `agent "ghost"`)
	require.Contains(t, oriented, "not configured")
	// The user baseline still governs, so KEG tools stay available.
	require.Contains(t, oriented, "+baseline")
	require.False(t, callCat(t, ctx, session).IsError)
}

func newAgentOrientationServer(t *testing.T, agent string) (context.Context, *sdkmcp.Server, *toolkit.Runtime) {
	t.Helper()
	return newAgentOrientationServerWithEnv(t, map[string]string{"TAP_AGENT": agent})
}

// newAgentOrientationServer builds a config-driven session (no static flight)
// whose flight comes from an agent, mirroring what `tap launch` produces.
func newAgentOrientationServerWithEnv(t *testing.T, env map[string]string) (context.Context, *sdkmcp.Server, *toolkit.Runtime) {
	t.Helper()
	ctx := context.Background()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Setwd("/home/testuser/project"))
	rt := sb.Runtime()
	for k, v := range env {
		require.NoError(t, rt.Env().Set(k, v))
	}
	writeFlight(t, rt, "baseline", "Baseline instructions")
	writeFlight(t, rt, "alpha", "Alpha instructions")
	writeFlight(t, rt, "beta", "Beta instructions")
	// The user baseline is what an unknown or flightless agent falls back to.
	writeAgentFlight(t, rt, "qwen", "alpha")

	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: rt})
	require.NoError(t, err)
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{})
	return ctx, srv, rt
}

// writeAgentFlight rewrites the user config so agent `name` points at +slug,
// keeping the baseline `flight:` underneath it to prove the agent outranks it.
func writeAgentFlight(t *testing.T, rt *toolkit.Runtime, name, slug string) {
	t.Helper()
	body := "defaultKeg: personal\nfallbackNamespace: local\n" +
		"hubs:\n  home:\n    kind: local\n    basePath: ~/kegs\n" +
		"flight: +baseline\n" +
		"agents:\n  " + name + ":\n    model: ollama/qwen3.6:35b\n    flight: +" + slug + "\n"
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(body), 0o644))
}
