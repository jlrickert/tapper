package mcp_test

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// The tests in this file validate the four session-authority contracts that
// `tap mcp` (stdio, config-driven) and the hosted HTTP endpoint
// (provider-driven) must both honour:
//
//  1. Both transports gate KEG work through the same session flight gate.
//  2. orient reads the current flight and refreshes it on every call.
//  3. Any denied KEG operation reports a permission error that sends the agent
//     back to orient, because the usual cause is a flight edited mid-session.
//  4. A flight holding manage_flights can edit itself, and the edit governs the
//     very next call without a reconnect.

// restrictionNudge is the recovery instruction every cover and role-cap denial
// must carry. recoveryNudge is its counterpart for a session with no flight at
// all, which is worded for a reader who has nothing to refresh yet.
const (
	restrictionNudge = "Call `orient` to refresh this session's flight authority"
	recoveryNudge    = "then orient again"
)

// newValidationSession builds a provider-driven session (the hosted shape) over
// a sandbox holding two real local kegs, so widening a cover mid-session can be
// observed against a keg that actually exists.
func newValidationSession(t *testing.T) (*sdkmcp.ClientSession, context.Context, *fakeSessionBackend) {
	t.Helper()
	ctx := context.Background()
	sb := newTestSandbox(t)
	rt := sb.Runtime()

	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: rt})
	require.NoError(t, err)
	_, err = tap.InitKeg(ctx, tapper.InitOptions{Keg: "other", Namespace: "local"})
	require.NoError(t, err)

	provider := newFakeSessionBackend()
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{}, mcp.ServerOptions{
		OrientationProvider: provider, FlightProvider: provider,
		KegProvider: provider, IdentityProvider: provider,
	})
	return connectFlightSession(t, ctx, srv, nil), ctx, provider
}

func callCatKeg(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession, keg string) *sdkmcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"keg":          keg,
			"node_ids":     []string{"0"},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	return result
}

// --- requirement 3: denials send the agent back to orient ------------------

func TestMCP_UncoveredKegDenialNudgesReorientation_LocalSurface(t *testing.T) {
	t.Parallel()
	session, ctx, privateID := newFlightLockedSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"keg": "private", "node_ids": []string{privateID}, "content_only": true,
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	text := extractText(t, res)
	require.Contains(t, text, `keg "@local/private" is not available in flight`)
	require.Contains(t, text, restrictionNudge,
		"a cover denial must send the agent back to orient; the flight may have changed mid-session")
}

func TestMCP_UncoveredKegDenialNudgesReorientation_ProviderSurface(t *testing.T) {
	t.Parallel()
	session, ctx, _ := newValidationSession(t)

	res := callCatKeg(t, ctx, session, "@local/other")
	require.True(t, res.IsError)
	text := extractText(t, res)
	require.Contains(t, text, `keg "@local/other" is not available in flight`)
	require.Contains(t, text, restrictionNudge,
		"the hosted surface must nudge re-orientation exactly like the stdio surface")
}

func TestMCP_RoleCapDenialNudgesReorientation(t *testing.T) {
	t.Parallel()
	// +focused covers @local/personal at viewer, so a write is refused on the
	// role cap rather than on cover membership.
	session, ctx, _ := newFlightLockedSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "create",
		Arguments: map[string]any{"keg": "personal", "nodes": []any{map[string]any{"key": "node", "title": "Blocked by role cap"}}},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	text := extractText(t, res)
	require.Contains(t, text, "viewer-only")
	require.Contains(t, text, restrictionNudge, "a role-cap denial must nudge re-orientation too")
}

// TestMCP_MidSessionFlightWideningDeniesUntilReorient is the scenario the nudge
// exists for: the flight gained a keg after this session pinned its authority,
// so the next call is denied even though the stored flight now permits it, and
// only orient repairs the session.
func TestMCP_MidSessionFlightWideningDeniesUntilReorient(t *testing.T) {
	t.Parallel()
	session, ctx, provider := newValidationSession(t)

	require.False(t, callCatKeg(t, ctx, session, "@local/personal").IsError)
	require.True(t, callCatKeg(t, ctx, session, "@local/other").IsError)

	// Someone else widens the flight while this session is live.
	cover := []tapper.FlightCover{
		{Namespace: "local", Keg: "personal", Role: tapper.FlightRoleEditor},
		{Namespace: "local", Keg: "other", Role: tapper.FlightRoleEditor},
	}
	_, err := provider.UpdateFlight(ctx, tapper.UpdateFlightOptions{Ref: "+active", Cover: &cover})
	require.NoError(t, err)

	denied := callCatKeg(t, ctx, session, "@local/other")
	require.True(t, denied.IsError, "the pinned snapshot still governs until orient")
	require.Contains(t, extractText(t, denied), restrictionNudge)

	callOrient(t, ctx, session)
	require.False(t, callCatKeg(t, ctx, session, "@local/other").IsError,
		"orient must adopt the widened cover on the same connection")
}

// --- requirement 4: manage_flights can modify its own flight ---------------

func TestMCP_SelfEditWideningOwnCoverTakesEffectImmediately(t *testing.T) {
	t.Parallel()
	session, ctx, _ := newValidationSession(t)
	require.True(t, callCatKeg(t, ctx, session, "@local/other").IsError)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_edit", Arguments: map[string]any{
		"ref":   "+active",
		"cover": []string{"@local/personal=editor", "@local/other=editor"},
	}})
	require.NoError(t, err)
	require.False(t, res.IsError, extractText(t, res))

	require.False(t, callCatKeg(t, ctx, session, "@local/other").IsError,
		"a manage_flights self-edit must govern the next call without orient")
	require.False(t, callCatKeg(t, ctx, session, "@local/personal").IsError)
}

func TestMCP_WithoutManageFlightsSelfEditIsHiddenAndRefused(t *testing.T) {
	t.Parallel()
	session, ctx, provider := newValidationSession(t)

	provider.mu.Lock()
	provider.active = "@local/+other" // +other carries no capabilities
	provider.mu.Unlock()
	callOrient(t, ctx, session)

	listed := listedToolNames(t, ctx, session)
	require.NotContains(t, listed, "flight_create")
	require.NotContains(t, listed, "flight_edit")
	require.NotContains(t, listed, "flight_delete")

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_edit", Arguments: map[string]any{
		"ref": "+other", "instructions": "should not apply",
	}})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, extractText(t, res), "manage_flights")

	stored, err := provider.GetFlight(ctx, "+other")
	require.NoError(t, err)
	require.Equal(t, "other", stored.Instructions, "a refused self-edit must not persist")
}

// --- requirements 1 + 2: both transports gate and recover identically ------

// TestMCP_BothSurfacesEnterTheSameRecoveryMode drives the config-driven and the
// provider-driven servers into a flight-less state by their own transport's
// mechanism — an emptied configuration versus a cleared account preference —
// and asserts they converge on one recovery contract.
func TestMCP_BothSurfacesEnterTheSameRecoveryMode(t *testing.T) {
	recoveryTools := []string{"orient", "list_flights", "flight_show", "auth_info"}

	providerSession, providerCtx, provider := newValidationSession(t)
	provider.mu.Lock()
	provider.active = ""
	provider.mu.Unlock()
	require.Contains(t, callOrient(t, providerCtx, providerSession), "No KEGs are currently available")
	require.ElementsMatch(t, recoveryTools, listedToolNames(t, providerCtx, providerSession))
	hosted := callCatKeg(t, providerCtx, providerSession, "@local/personal")

	localCtx, srv, rt := newOrientationServer(t, "")
	localSession := connectFlightSession(t, localCtx, srv, nil)
	writeProjectFlight(t, rt, "")
	writeUserFlight(t, rt, "")
	require.Contains(t, callOrient(t, localCtx, localSession), "No KEGs are currently available")
	require.ElementsMatch(t, recoveryTools, listedToolNames(t, localCtx, localSession))
	local := callCat(t, localCtx, localSession)

	require.True(t, hosted.IsError)
	require.True(t, local.IsError)
	require.Equal(t, extractText(t, local), extractText(t, hosted),
		"a flight-less session must read the same on both transports")
	require.Contains(t, extractText(t, local), "no flight is selected")
	require.Contains(t, extractText(t, local), recoveryNudge)
}
