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

// A session that can reach no flights at all runs on the synthetic bootstrap
// flight instead of the select-a-flight recovery mode: telling a user to pick
// from an empty list is a dead end, so the session instead carries the
// authority to create the first flight and the first KEG.

// bootstrapTools is every tool a bootstrap session may see. The bootstrap
// flight carries both manage_flights and manage_kegs, so nothing is filtered
// out by capability on top of the allowlist.
var bootstrapTools = []string{
	"orient", "list_flights", "flight_show", "auth_info",
	"flight_create", "flight_edit", "flight_delete", "keg_create",
}

// flightSection returns the "## Flight" block of an orientation payload, which
// is where the session declares its own mode. Assertions must scope to it: the
// canonical guidance appended to every payload also describes bootstrap and
// recovery, so a whole-payload substring check passes in every mode.
func flightSection(t *testing.T, payload string) string {
	t.Helper()
	_, rest, ok := strings.Cut(payload, "## Flight\n")
	require.True(t, ok, "payload has no Flight section: %q", payload)
	section, _, _ := strings.Cut(rest, "## Guidance")
	return section
}

// newBootstrapSession builds the stdio surface over a configured but
// flight-less machine: the hub's basePath points at a directory with no
// flights.d, so discovery legitimately reports zero flights.
func newBootstrapSession(t *testing.T) (*sdkmcp.ClientSession, context.Context, *toolkit.Runtime) {
	t.Helper()
	ctx := context.Background()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Setwd("/home/testuser"))
	rt := sb.Runtime()
	sb.MustWriteFile("~/.config/tapper/config.yaml", []byte(`defaultKeg: personal
fallbackNamespace: local
hubs:
  home:
    kind: local
    defaultNamespace: local
    basePath: ~/empty-kegs
`), 0o644)

	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: rt})
	require.NoError(t, err)
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{})
	return connectFlightSession(t, ctx, srv, nil), ctx, rt
}

func TestMCP_NoFlightsAnywhereEntersBootstrapMode(t *testing.T) {
	t.Parallel()
	session, ctx, _ := newBootstrapSession(t)

	payload := session.InitializeResult().Instructions
	flight := flightSection(t, payload)
	require.Contains(t, flight, "temporary bootstrap flight")
	require.NotContains(t, flight, "recovery mode",
		"bootstrap must not present itself as the select-a-flight recovery mode")
	require.Contains(t, flight, "tap bootstrap")
	require.Contains(t, flight, "TAP_FLIGHT",
		"the stdio surface must nudge toward configuration, not a web UI")
	require.Equal(t, payload, callOrient(t, ctx, session), "orient is idempotent in bootstrap")

	require.ElementsMatch(t, bootstrapTools, listedToolNames(t, ctx, session))

	denied := callCatKeg(t, ctx, session, "@local/personal")
	require.True(t, denied.IsError, "an empty cover still denies every KEG")
	require.Contains(t, extractText(t, denied), "bootstrap flight")
}

// TestMCP_FlightsExistButUnselectedStaysInSelectMode guards the boundary
// between the two no-flight modes: a machine that has flights must keep asking
// the user to pick one rather than handing the agent admin authority.
func TestMCP_FlightsExistButUnselectedStaysInSelectMode(t *testing.T) {
	ctx, srv, rt := newOrientationServer(t, "")
	session := connectFlightSession(t, ctx, srv, nil)
	writeProjectFlight(t, rt, "")
	writeUserFlight(t, rt, "")

	flight := flightSection(t, callOrient(t, ctx, session))
	require.Contains(t, flight, "recovery mode")
	require.NotContains(t, flight, "bootstrap flight")
	require.ElementsMatch(t,
		[]string{"orient", "list_flights", "flight_show", "auth_info"},
		listedToolNames(t, ctx, session))
}

// TestMCP_BootstrapCreatesFirstKegThenAdoptsItsFlight walks the whole recovery
// the bootstrap instructions describe: create the KEG over MCP, have the user
// write and select a flight covering it, then orient into a working session.
func TestMCP_BootstrapCreatesFirstKegThenAdoptsItsFlight(t *testing.T) {
	t.Parallel()
	session, ctx, rt := newBootstrapSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_create", Arguments: map[string]any{
		"keg": "first", "namespace": "local", "title": "First KEG",
	}})
	require.NoError(t, err)
	require.False(t, res.IsError, extractText(t, res))
	require.Contains(t, extractText(t, res), "@local/first")

	require.True(t, callCatKeg(t, ctx, session, "@local/first").IsError,
		"creating a KEG does not add it to the active flight's cover")

	// The user does the part MCP deliberately cannot: write a flight and select it.
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/empty-kegs/flights.d/first.yaml",
		[]byte("title: First\ncover:\n  - namespace: local\n    keg: first\n    role: editor\n"), 0o644))
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/.config/tapper/config.yaml",
		[]byte("defaultKeg: personal\nfallbackNamespace: local\nflight: +first\nhubs:\n  home:\n    kind: local\n    defaultNamespace: local\n    basePath: ~/empty-kegs\n"), 0o644))

	flight := flightSection(t, callOrient(t, ctx, session))
	require.Contains(t, flight, "+first")
	require.NotContains(t, flight, "temporary bootstrap flight")
	require.False(t, callCatKeg(t, ctx, session, "@local/first").IsError,
		"orient must adopt the flight the user just selected")
	require.NotContains(t, listedToolNames(t, ctx, session), "keg_create",
		"a real flight without manage_kegs does not inherit bootstrap's authority")
}

// TestMCP_LocalFlightCreateReportsNotImplemented pins the honest failure for
// the one thing bootstrap cannot do on a local-only machine.
func TestMCP_LocalFlightCreateReportsNotImplemented(t *testing.T) {
	t.Parallel()
	session, ctx, _ := newBootstrapSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_create", Arguments: map[string]any{
		"ref": "@local/+attempt", "cover": []string{"@local/first=editor"},
	}})
	require.NoError(t, err)
	require.True(t, res.IsError)
	text := extractText(t, res)
	require.Contains(t, text, "not implemented for local hubs")
	require.Contains(t, text, "flights.d/attempt.yaml",
		"the refusal must name the manifest the user should write instead")
}

func TestMCP_KegCreateRequiresManageKegs(t *testing.T) {
	t.Parallel()
	session, ctx, provider := newValidationSession(t)

	// +active grants manage_flights only.
	require.NotContains(t, listedToolNames(t, ctx, session), "keg_create")
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_create", Arguments: map[string]any{
		"keg": "blocked", "namespace": "local",
	}})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, extractText(t, res), "manage_kegs")

	provider.mu.Lock()
	require.Empty(t, provider.createdKegs, "a refused keg_create must not reach the provider")
	provider.mu.Unlock()

	capabilities := []tapper.FlightCapability{
		tapper.FlightCapabilityManageFlights, tapper.FlightCapabilityManageKegs,
	}
	_, err = provider.UpdateFlight(ctx, tapper.UpdateFlightOptions{Ref: "+active", Capabilities: &capabilities})
	require.NoError(t, err)
	callOrient(t, ctx, session)

	require.Contains(t, listedToolNames(t, ctx, session), "keg_create")
	res, err = session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_create", Arguments: map[string]any{
		"keg": "allowed", "namespace": "local",
	}})
	require.NoError(t, err)
	require.False(t, res.IsError, extractText(t, res))

	provider.mu.Lock()
	require.Equal(t, []string{"@local/allowed"}, provider.createdKegs)
	provider.mu.Unlock()
}
