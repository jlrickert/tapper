package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/toolkit"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// flightSection returns the "## Flight" block of an orientation payload, which
// is where the session declares its own mode.
func flightSection(t *testing.T, payload string) string {
	t.Helper()
	_, rest, ok := strings.Cut(payload, "## Flight\n")
	require.True(t, ok, "payload has no Flight section: %q", payload)
	section, _, _ := strings.Cut(rest, "## Guidance")
	return section
}

// newNoFlightSession builds the stdio surface over an authenticated remote Hub
// with no flights, so discovery legitimately reports zero flights.
func newNoFlightSession(t *testing.T) (*sdkmcp.ClientSession, context.Context, *toolkit.Runtime) {
	t.Helper()
	ctx := context.Background()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Setwd("/home/testuser"))
	rt := sb.Runtime()
	installOrientationTestHub(t, rt)
	writeUserFlight(t, rt, "")
	tap := newMemoryTap(t, ctx, rt)
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{})
	return connectFlightSession(t, ctx, srv, nil), ctx, rt
}

func TestMCP_NoFlightsAnywhereUsesIdentityFullAccess(t *testing.T) {
	t.Parallel()
	session, ctx, _ := newNoFlightSession(t)

	requireConnectionInstructions(t, session.InitializeResult().Instructions)
	payload := callOrient(t, ctx, session)
	flight := flightSection(t, payload)
	require.Contains(t, flight, "No flight was provided")
	require.Contains(t, flight, "identity-authorized full access")
	require.Contains(t, flight, "TAP_FLIGHT",
		"the stdio surface must nudge toward configuration, not a web UI")
	require.Contains(t, flight, "start a new one")
	require.Equal(t, payload, callOrient(t, ctx, session), "orient is read-only and idempotent")

	tools := listedToolNames(t, ctx, session)
	require.Contains(t, tools, "cat")
	require.Contains(t, tools, "create")
	require.Contains(t, tools, "flight_create")
	require.Contains(t, tools, "keg_create")
	search, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_search", Arguments: map[string]any{"query": "anything"}})
	require.NoError(t, err)
	require.False(t, search.IsError, extractText(t, search))

	created, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_create", Arguments: map[string]any{
		"keg": "first", "namespace": "local", "title": "First KEG",
	}})
	require.NoError(t, err)
	require.False(t, created.IsError, extractText(t, created))
	require.False(t, callCatKeg(t, ctx, session, "@local/first").IsError,
		"no-flight full access must expose a newly created KEG at the identity's real role")
}

// Two governed states have a nil session flight: failed-root recovery, which
// reaches nothing, and no-flight identity authority, which reaches everything
// the identity reaches. auth_info used to treat both as "no flight, no KEGs" and
// so reported an empty list in a session that could read them all — directly
// contradicting keg_list on the same connection.
func TestMCP_NoFlightAuthInfoReportsIdentityKegs(t *testing.T) {
	t.Parallel()
	session, ctx, _ := newNoFlightSession(t)

	created, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_create", Arguments: map[string]any{
		"keg": "first", "namespace": "local", "title": "First KEG",
	}})
	require.NoError(t, err)
	require.False(t, created.IsError, extractText(t, created))

	listed, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, listed.IsError, extractText(t, listed))
	require.Contains(t, extractText(t, listed), "@local/first")

	info, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "auth_info", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, info.IsError, extractText(t, info))
	require.Contains(t, extractText(t, info), "@local/first",
		"auth_info must not report zero KEGs while keg_list reports them on the same connection")
}

func TestMCP_FlightsExistButUnselectedUsesFullAccessAndExactSelection(t *testing.T) {
	ctx, srv, rt := newOrientationServer(t, "")
	writeProjectFlight(t, rt, "")
	writeUserFlight(t, rt, "")
	session := connectFlightSession(t, ctx, srv, nil)

	flight := flightSection(t, callOrient(t, ctx, session))
	require.Contains(t, flight, "No flight was provided")
	require.Contains(t, flight, "@local/+alpha")
	require.Contains(t, listedToolNames(t, ctx, session), "cat")

	explicit := orientCall(t, session, ctx, map[string]any{"flight": "@local/+alpha"})
	require.Contains(t, explicit, "Alpha instructions")
	require.NotContains(t, explicit, "No flight was provided")
	require.Contains(t, explicit, "Launch root: (none; identity-authorized full access)")
	require.Contains(t, explicit, "Selected flight: `@local/+alpha`")

	denied, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "orient", Arguments: map[string]any{
		"flight": "@other/+missing",
	}})
	require.NoError(t, err)
	require.True(t, denied.IsError)
	require.Contains(t, extractText(t, denied), "ORIENTATION_DENIED")

	type outcome struct {
		flight string
		text   string
		err    error
	}
	results := make(chan outcome, 2)
	for _, selected := range []string{"@local/+alpha", "@local/+beta"} {
		selected := selected
		go func() {
			res, callErr := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "orient", Arguments: map[string]any{"flight": selected}})
			text := ""
			if res != nil {
				text = extractText(t, res)
			}
			results <- outcome{flight: selected, text: text, err: callErr}
		}()
	}
	for range 2 {
		got := <-results
		require.NoError(t, got.err)
		if strings.HasSuffix(got.flight, "+alpha") {
			require.Contains(t, got.text, "Alpha instructions")
			require.NotContains(t, got.text, "Beta instructions")
		} else {
			require.Contains(t, got.text, "Beta instructions")
			require.NotContains(t, got.text, "Alpha instructions")
		}
	}
	require.Contains(t, flightSection(t, callOrient(t, ctx, session)), "No flight was provided",
		"concurrent explicit selections must not replace no-flight authority")
}

func TestMCP_NoFlightStaysPinnedAndNewSessionAdoptsConfiguredFlight(t *testing.T) {
	t.Parallel()
	session, ctx, rt := newNoFlightSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_create", Arguments: map[string]any{
		"keg": "first", "namespace": "local", "title": "First KEG",
	}})
	require.NoError(t, err)
	require.False(t, res.IsError, extractText(t, res))
	require.Contains(t, extractText(t, res), "@local/first")

	require.False(t, callCatKeg(t, ctx, session, "@local/first").IsError)

	// The user does the part MCP deliberately cannot: select the created flight
	// in normal Tapper configuration.
	orientationTestHubFor(t, rt).putFlight(tapper.HubFlight{
		Namespace: "local", Slug: "first", Title: "First", Visibility: tapper.FlightVisibilityPrivate,
		Cover: []tapper.HubFlightCover{{Namespace: "local", Keg: "first", Role: "editor"}},
	})
	writeUserFlight(t, rt, "first")

	beforeRefresh := flightSection(t, callOrient(t, ctx, session))
	require.Contains(t, beforeRefresh, "No flight was provided",
		"orient must not replace the connection-pinned no-flight state")
	refreshed, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "session_refresh", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, refreshed.IsError, extractText(t, refreshed))
	require.Equal(t, "already_active", refreshed.StructuredContent.(map[string]any)["status"])
	require.Equal(t, false, refreshed.StructuredContent.(map[string]any)["toolsChanged"])
	require.Equal(t, "new_session", refreshed.StructuredContent.(map[string]any)["nextAction"])
	require.Contains(t, flightSection(t, callOrient(t, ctx, session)), "No flight was provided")

	tap := newMemoryTap(t, ctx, rt)
	newSession := connectFlightSession(t, ctx, mcp.NewServer(tap, "test", mcp.KegDefaults{}), nil)
	flight := flightSection(t, callOrient(t, ctx, newSession))
	require.Contains(t, flight, "@local/+first")
	require.NotContains(t, flight, "No flight was provided")
	require.False(t, callCatKeg(t, ctx, newSession, "@local/first").IsError)
}

func TestMCP_NoFlightCreatesFlightThroughRemoteHub(t *testing.T) {
	t.Parallel()
	session, ctx, _ := newNoFlightSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_create", Arguments: map[string]any{
		"ref": "@local/+attempt", "cover": []string{"@local/first=editor"},
	}})
	require.NoError(t, err)
	require.False(t, res.IsError, extractText(t, res))
	require.Contains(t, extractText(t, res), "@local/+attempt")
}
