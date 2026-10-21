package mcp_test

import (
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// flight_create sets each switch when asked, and flight_read reports them.
func TestMCP_FlightCreateSetsSwitches(t *testing.T) {
	backend := newPerCallFlightBackend()
	session, ctx := newPerCallFlightSession(t, backend)

	created, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_create", Arguments: map[string]any{
		"ref": "@team/+desk", "manage_kegs": true,
	}})
	require.NoError(t, err)
	require.False(t, created.IsError, "%v", created.Content)
	out := created.StructuredContent.(map[string]any)
	require.Equal(t, true, out["manage_kegs"])
	require.Equal(t, false, out["manage_flights"], "only the switch asked for is set")

	read, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_read", Arguments: map[string]any{"name": "@team/+desk"}})
	require.NoError(t, err)
	require.Equal(t, true, read.StructuredContent.(map[string]any)["manage_kegs"])

	plain, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_read", Arguments: map[string]any{"name": "@team/+child"}})
	require.NoError(t, err)
	require.Equal(t, false, plain.StructuredContent.(map[string]any)["manage_kegs"], "a flight without switches reads false")
	require.Equal(t, false, plain.StructuredContent.(map[string]any)["manage_flights"])
}

// The deprecated manage argument sets both switches.
func TestMCP_FlightCreateManageAliasSetsBoth(t *testing.T) {
	backend := newPerCallFlightBackend()
	session, ctx := newPerCallFlightSession(t, backend)

	created, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_create", Arguments: map[string]any{
		"ref": "@team/+desk", "manage": true,
	}})
	require.NoError(t, err)
	require.False(t, created.IsError, "%v", created.Content)
	out := created.StructuredContent.(map[string]any)
	require.Equal(t, true, out["manage_kegs"])
	require.Equal(t, true, out["manage_flights"])
}

// flight_edit changes a switch only when the call names it, or names the
// deprecated manage alias and leaves the switch out; an edit of anything
// else leaves both to the Hub to keep.
func TestMCP_FlightEditChangesSwitchesOnlyWhenNamed(t *testing.T) {
	backend := newPerCallFlightBackend()
	session, ctx := newPerCallFlightSession(t, backend)

	for _, args := range []map[string]any{
		{"ref": "@team/+child", "manage_kegs": true, "expected_hash": "h"},
		{"ref": "@team/+child", "manage_flights": false, "expected_hash": "h"},
		{"ref": "@team/+child", "manage": true, "manage_kegs": false, "expected_hash": "h"},
		{"ref": "@team/+child", "title": "Retitled", "expected_hash": "h"},
	} {
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_edit", Arguments: args})
		require.NoError(t, err)
		require.False(t, res.IsError, "%v", res.Content)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	require.Len(t, backend.updates, 4)

	require.NotNil(t, backend.updates[0].ManageKegs)
	require.True(t, *backend.updates[0].ManageKegs)
	require.Nil(t, backend.updates[0].ManageFlights, "a switch not named is kept")

	require.Nil(t, backend.updates[1].ManageKegs)
	require.NotNil(t, backend.updates[1].ManageFlights)
	require.False(t, *backend.updates[1].ManageFlights)

	require.NotNil(t, backend.updates[2].ManageKegs)
	require.False(t, *backend.updates[2].ManageKegs, "an explicit switch wins over the alias")
	require.NotNil(t, backend.updates[2].ManageFlights)
	require.True(t, *backend.updates[2].ManageFlights, "the alias fills the switch left out")

	require.Nil(t, backend.updates[3].ManageKegs, "an edit that names no switch keeps them")
	require.Nil(t, backend.updates[3].ManageFlights)
}
