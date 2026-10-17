package mcp_test

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func readFlightForTest(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession, name string) map[string]any {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_read", Arguments: map[string]any{"name": name}})
	require.NoError(t, err)
	require.False(t, result.IsError, extractText(t, result))
	out, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok, "flight_read returns structured content")
	return out
}

// A long-running MCP server reads a flight as the Hub holds it now: an edit
// made elsewhere (another client, the web) shows on the next flight_read,
// with the hash a guarded edit needs, for the session's own flight and for
// any other.
func TestMCP_FlightReadSeesOutOfProcessEdits(t *testing.T) {
	ctx, srv, rt := newOrientationServer(t, "+alpha")
	session := connectFlightSession(t, ctx, srv, nil)
	callOrient(t, ctx, session)
	callCat(t, ctx, session)

	for _, slug := range []string{"beta", "alpha"} {
		before := readFlightForTest(t, ctx, session, "+"+slug)
		writeFlightCover(t, rt, slug, slug+" edited elsewhere", "personal")
		after := readFlightForTest(t, ctx, session, "+"+slug)
		require.Equal(t, slug+" edited elsewhere", after["instructions"], "+%s is read fresh", slug)
		require.NotEqual(t, before["hash"], after["hash"], "+%s carries the current hash", slug)
	}
}
