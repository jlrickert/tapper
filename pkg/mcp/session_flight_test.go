package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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

const flightSwitchControlTool = "flight_switch_control"

func TestMCP_FlightSessionIsolationAndHumanSwitch(t *testing.T) {
	ctx, srv, _, _ := newSessionGateServer(t, "+incapable")
	var notifications atomic.Int64
	clientOptions := &sdkmcp.ClientOptions{
		ElicitationHandler: acceptFlightSwitch,
		ToolListChangedHandler: func(context.Context, *sdkmcp.ToolListChangedRequest) {
			notifications.Add(1)
		},
	}
	first := connectFlightSession(t, ctx, srv, clientOptions)
	second := connectFlightSession(t, ctx, srv, clientOptions)

	require.NotContains(t, listedToolNames(t, ctx, first), "flight_create")
	require.NotContains(t, listedToolNames(t, ctx, second), "flight_create")

	unauthorized, err := first.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "flight_create",
		Arguments: map[string]any{"ref": "+new"},
	})
	require.NoError(t, err)
	require.True(t, unauthorized.IsError)
	require.Contains(t, extractText(t, unauthorized), "does not grant manage_flights")

	switched, err := first.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      flightSwitchControlTool,
		Arguments: map[string]any{"ref": "+capable"},
	})
	require.NoError(t, err)
	require.False(t, switched.IsError, extractText(t, switched))
	require.Contains(t, extractText(t, switched), "@local/+capable")
	require.Eventually(t, func() bool { return notifications.Load() > 0 }, time.Second, 10*time.Millisecond)

	require.Contains(t, listedToolNames(t, ctx, first), "flight_create")
	require.NotContains(t, listedToolNames(t, ctx, second), "flight_create")

	activeEdit, err := first.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "flight_edit",
		Arguments: map[string]any{"ref": "@local/+capable", "title": "changed"},
	})
	require.NoError(t, err)
	require.True(t, activeEdit.IsError)
	require.Contains(t, extractText(t, activeEdit), "cannot edit or delete its own active flight")
}

func TestMCP_EmptyCoverDeniesAllKegAccess(t *testing.T) {
	ctx, srv, _, _ := newSessionGateServer(t, "+empty")
	session := connectFlightSession(t, ctx, srv, nil)

	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{"0"},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, extractText(t, result), `keg "@local/personal" is not available in flight`)
}

func TestMCP_FlightManifestChangePermanentlyInvalidatesSession(t *testing.T) {
	ctx, srv, rt, _ := newSessionGateServer(t, "+capable")
	session := connectFlightSession(t, ctx, srv, &sdkmcp.ClientOptions{ElicitationHandler: acceptFlightSwitch})

	before, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{"0"},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	require.False(t, before.IsError, extractText(t, before))

	equivalent := `cover:
  - role: editor
    keg: personal
    namespace: local
capabilities:
  - manage_flights
visibility: private
title: Capable
`
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/kegs/flights.d/capable.yaml", []byte(equivalent), 0o644))
	unchanged, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{"0"},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	require.False(t, unchanged.IsError, extractText(t, unchanged))

	changed := `title: Capable changed
visibility: private
capabilities: [manage_flights]
cover:
  - namespace: local
    keg: personal
    role: editor
`
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/kegs/flights.d/capable.yaml", []byte(changed), 0o644))

	invalidated, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids": []string{"0"},
		},
	})
	require.NoError(t, err)
	require.True(t, invalidated.IsError)
	require.Contains(t, extractText(t, invalidated), "MCP flight session invalidated")
	require.Contains(t, extractText(t, invalidated), "changed after this MCP session connected")

	require.NoError(t, rt.AtomicWriteFile("/home/testuser/kegs/flights.d/capable.yaml", []byte(capableFlightManifest), 0o644))
	stillInvalid, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "cat", Arguments: map[string]any{"node_ids": []string{"0"}}})
	require.NoError(t, err)
	require.True(t, stillInvalid.IsError)
	require.Contains(t, extractText(t, stillInvalid), "MCP flight session invalidated")

	recovered, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      flightSwitchControlTool,
		Arguments: map[string]any{"ref": "+incapable"},
	})
	require.NoError(t, err)
	require.False(t, recovered.IsError, extractText(t, recovered))

	after, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "cat", Arguments: map[string]any{"node_ids": []string{"0"}}})
	require.NoError(t, err)
	require.False(t, after.IsError, extractText(t, after))
}

func TestMCP_HubManifestChangeInvalidatesSession(t *testing.T) {
	var coverVisible atomic.Bool
	coverVisible.Store(true)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/@foldwise/+remote" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		flight := tapper.HubFlight{
			Namespace:    "foldwise",
			Slug:         "remote",
			Visibility:   tapper.FlightVisibilityPrivate,
			Capabilities: []tapper.FlightCapability{tapper.FlightCapabilityManageFlights},
		}
		if coverVisible.Load() {
			flight.Cover = []tapper.HubFlightCover{{Namespace: "foldwise", Keg: "docs", Role: "viewer"}}
		}
		_ = json.NewEncoder(w).Encode(flight)
	}))
	t.Cleanup(hub.Close)

	ctx := context.Background()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Setwd("/home/testuser"))
	config := fmt.Sprintf(`fallbackNamespace: foldwise
namespaces:
  foldwise: cloud
hubs:
  cloud:
    kind: remote
    url: %s
    token: test-token
`, hub.URL)
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(config), 0o644))
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{KegTargetOptions: tapper.KegTargetOptions{Flight: "@foldwise/+remote"}})
	session := connectFlightSession(t, ctx, srv, nil)
	require.Contains(t, listedToolNames(t, ctx, session), "flight_create")
	require.Contains(t, listedToolNames(t, ctx, session), "flight_create", "unchanged Hub manifests must keep sessions valid")

	// Model the Hub redacting a covered keg after its visibility drifts. The
	// effective manifest changes even though the stored flight did not.
	coverVisible.Store(false)
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "cat", Arguments: map[string]any{"node_ids": []string{"0"}}})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, extractText(t, result), "MCP flight session invalidated")
	require.Contains(t, extractText(t, result), "changed after this MCP session connected")
}

func TestMCP_NoFlightStartsInRecoveryModeAndHumanSwitchRestoresTools(t *testing.T) {
	ctx, srv, _, _ := newSessionGateServer(t, "")
	session := connectFlightSession(t, ctx, srv, &sdkmcp.ClientOptions{ElicitationHandler: acceptFlightSwitch})

	require.ElementsMatch(t,
		[]string{"orient", "list_flights", "flight_show", "auth_status", "config"},
		listedToolNames(t, ctx, session),
	)

	orient, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "orient", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.True(t, orient.IsError)
	for _, want := range []string{"no flight is selected", "KEG tools are locked", "list_flights", "flight_show", "ask the user", "select a flight in Tapper configuration", "reconnect"} {
		require.Contains(t, extractText(t, orient), want)
	}
	require.NotContains(t, extractText(t, orient), "`tap ")

	guessed, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "cat", Arguments: map[string]any{"node_ids": []string{"0"}}})
	require.NoError(t, err)
	require.True(t, guessed.IsError)
	require.Contains(t, extractText(t, guessed), "KEG tools are locked")

	flights, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "list_flights", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, flights.IsError, extractText(t, flights))
	require.Contains(t, extractText(t, flights), "@local/+capable")
	shown, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "flight_show",
		Arguments: map[string]any{"name": "+capable"},
	})
	require.NoError(t, err)
	require.False(t, shown.IsError, extractText(t, shown))
	require.Contains(t, extractText(t, shown), "Capable")

	switched, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      flightSwitchControlTool,
		Arguments: map[string]any{"ref": "+capable"},
	})
	require.NoError(t, err)
	require.False(t, switched.IsError, extractText(t, switched))
	require.Contains(t, listedToolNames(t, ctx, session), "cat")
}

func TestMCP_FullSurfaceFlightStartupValidation(t *testing.T) {
	ctx := context.Background()
	_, _, _, tap := newSessionGateServer(t, "+capable")

	active, err := mcp.ValidateFullSurfaceFlight(ctx, tap, "")
	require.NoError(t, err)
	require.Empty(t, active)

	active, err = mcp.ValidateFullSurfaceFlight(ctx, tap, "+capable")
	require.NoError(t, err)
	require.Equal(t, "@local/+capable", active)

	_, err = mcp.ValidateFullSurfaceFlight(ctx, tap, "+missing")
	require.Error(t, err)
	require.Contains(t, err.Error(), `load active MCP flight "+missing"`)
}

const capableFlightManifest = `title: Capable
visibility: private
capabilities: [manage_flights]
cover:
  - namespace: local
    keg: personal
    role: editor
`

func newSessionGateServer(t *testing.T, initial string) (context.Context, *sdkmcp.Server, *toolkit.Runtime, *tapper.Tap) {
	t.Helper()
	ctx := context.Background()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Setwd("/home/testuser"))
	rt := sb.Runtime()

	manifests := map[string]string{
		"capable.yaml": capableFlightManifest,
		"incapable.yaml": `title: Incapable
visibility: private
cover:
  - namespace: local
    keg: personal
    role: editor
`,
		"empty.yaml": `title: Empty
visibility: private
cover: []
`,
	}
	for name, body := range manifests {
		require.NoError(t, rt.AtomicWriteFile("/home/testuser/kegs/flights.d/"+name, []byte(body), 0o644))
	}
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: rt})
	require.NoError(t, err)
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{
		KegTargetOptions: tapper.KegTargetOptions{Flight: initial},
	})
	return ctx, srv, rt, tap
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

func acceptFlightSwitch(_ context.Context, request *sdkmcp.ElicitRequest) (*sdkmcp.ElicitResult, error) {
	if !strings.Contains(request.Params.Message, "Switch this Tapper MCP session") {
		return &sdkmcp.ElicitResult{Action: "decline"}, nil
	}
	return &sdkmcp.ElicitResult{Action: "accept", Content: map[string]any{"confirm": true}}, nil
}

func listedToolNames(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession) []string {
	t.Helper()
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	require.NotContains(t, names, flightSwitchControlTool)
	return names
}
