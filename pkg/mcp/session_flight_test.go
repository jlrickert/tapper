package mcp_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
)

func TestMCP_ConfigDrivenOrientationAdoptsFlightWithoutReconnect(t *testing.T) {
	ctx, srv, rt := newOrientationServer(t, "")
	var notifications atomic.Int64
	session := connectFlightSession(t, ctx, srv, &sdkmcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *sdkmcp.ToolListChangedRequest) {
			notifications.Add(1)
		},
	})

	require.Contains(t, session.InitializeResult().Instructions, "+alpha")
	require.Contains(t, session.InitializeResult().Instructions, "Alpha instructions")

	writeProjectFlight(t, rt, "beta")
	before := callCat(t, ctx, session)
	require.False(t, before.IsError, extractText(t, before), "config changes do not adopt authority before orient")

	oriented := callOrient(t, ctx, session)
	require.Contains(t, oriented, "+beta")
	require.Contains(t, oriented, "Beta instructions")
	require.NotContains(t, oriented, "Active KEG")
	require.True(t, callCat(t, ctx, session).IsError, "calls after orientation use beta authority")

	writeProjectFlight(t, rt, "alpha")
	oriented = callOrient(t, ctx, session)
	require.Contains(t, oriented, "+alpha")
	require.False(t, callCat(t, ctx, session).IsError)
	require.Eventually(t, func() bool { return notifications.Load() > 0 }, time.Second, 10*time.Millisecond)
}

func TestMCP_StaticFlightIgnoresConfiguredSelectionAndRefreshesManifest(t *testing.T) {
	ctx, srv, rt := newOrientationServer(t, "+alpha")
	session := connectFlightSession(t, ctx, srv, nil)

	writeProjectFlight(t, rt, "beta")
	require.Contains(t, callOrient(t, ctx, session), "Alpha instructions")

	writeFlightCover(t, rt, "alpha", "Alpha refreshed", "other")
	require.False(t, callCat(t, ctx, session).IsError, "same-flight changes wait for orientation")
	oriented := callOrient(t, ctx, session)
	require.Contains(t, oriented, "Alpha refreshed")
	require.NotContains(t, oriented, "Beta instructions")
	require.True(t, callCat(t, ctx, session).IsError, "orientation publishes the refreshed cover")
}

func TestMCP_ParallelSessionsAdoptConfigurationIndependently(t *testing.T) {
	ctx, srv, rt := newOrientationServer(t, "")
	first := connectFlightSession(t, ctx, srv, nil)
	second := connectFlightSession(t, ctx, srv, nil)

	writeProjectFlight(t, rt, "beta")
	require.Contains(t, callOrient(t, ctx, first), "+beta")
	require.True(t, callCat(t, ctx, first).IsError)
	require.False(t, callCat(t, ctx, second).IsError, "unoriented session retains alpha")

	require.Contains(t, callOrient(t, ctx, second), "+beta")
	require.True(t, callCat(t, ctx, second).IsError)
}

func TestMCP_FailedRefreshRetainsAuthorityAndBlankEntersRecovery(t *testing.T) {
	ctx, srv, rt := newOrientationServer(t, "")
	session := connectFlightSession(t, ctx, srv, nil)

	writeProjectFlight(t, rt, "missing")
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "orient", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.False(t, callCat(t, ctx, session).IsError, "last valid authority survives failed refresh")

	writeProjectFlight(t, rt, "")
	require.Contains(t, callOrient(t, ctx, session), "No KEGs are currently available")
	names := listedToolNames(t, ctx, session)
	require.ElementsMatch(t, []string{"orient", "list_flights", "flight_show", "auth_status", "config"}, names)
}

func TestMCP_OrientRejectsKegInputAndInitializationMatchesOrient(t *testing.T) {
	ctx, srv, _ := newOrientationServer(t, "")
	session := connectFlightSession(t, ctx, srv, nil)
	initial := session.InitializeResult().Instructions
	require.Equal(t, initial, callOrient(t, ctx, session))

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "orient",
		Arguments: map[string]any{"keg": "personal"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

func newOrientationServer(t *testing.T, static string) (context.Context, *sdkmcp.Server, *toolkit.Runtime) {
	t.Helper()
	ctx := context.Background()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Setwd("/home/testuser/project"))
	rt := sb.Runtime()
	writeProjectFlight(t, rt, "alpha")
	writeFlight(t, rt, "alpha", "Alpha instructions")
	writeFlight(t, rt, "beta", "Beta instructions")

	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: rt})
	require.NoError(t, err)
	_, err = tap.FlightService.GetFlightFresh(ctx, "+alpha")
	require.NoError(t, err)
	require.Equal(t, "+alpha", tap.ActiveFlightName(""))
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{
		KegTargetOptions: tapper.KegTargetOptions{Flight: static},
	})
	return ctx, srv, rt
}

func writeProjectFlight(t *testing.T, rt *toolkit.Runtime, flight string) {
	t.Helper()
	body := "defaultKeg: personal\nfallbackNamespace: local\nhubs:\n  home:\n    kind: local\n    basePath: ~/kegs\n"
	if flight != "" {
		body += "flight: +" + flight + "\n"
	}
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(body), 0o644))
}

func writeFlight(t *testing.T, rt *toolkit.Runtime, slug, instructions string) {
	t.Helper()
	kegName := "personal"
	if slug == "beta" {
		kegName = "other"
	}
	writeFlightCover(t, rt, slug, instructions, kegName)
}

func writeFlightCover(t *testing.T, rt *toolkit.Runtime, slug, instructions, kegName string) {
	t.Helper()
	body := "title: " + strings.Title(slug) + "\nvisibility: private\ncover:\n  - namespace: local\n    keg: " + kegName + "\n    role: editor\ninstructions: " + instructions + "\n"
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/kegs/flights.d/"+slug+".yaml", []byte(body), 0o644))
}

func connectFlightSession(t *testing.T, ctx context.Context, srv *sdkmcp.Server, opts *sdkmcp.ClientOptions) *sdkmcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "flight-test", Version: "0.1"}, opts)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callOrient(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession) string {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "orient", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, result.IsError, extractText(t, result))
	return extractText(t, result)
}

func callCat(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession) *sdkmcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"keg":          "@local/personal",
			"node_ids":     []string{"0"},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	return result
}

func listedToolNames(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession) []string {
	t.Helper()
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	return names
}
