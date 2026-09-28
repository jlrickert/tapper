package mcp_test

import (
	"context"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
)

func newCachedFlightSession(t *testing.T, backend *perCallFlightBackend, ttl time.Duration) (*sdkmcp.ClientSession, context.Context, func(time.Duration)) {
	t.Helper()
	ctx := context.Background()
	sb := newTestSandbox(t)
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	server := mcp.NewServer(tap, "test", mcp.KegDefaults{}, mcp.ServerOptions{
		OrientationProvider: backend, FlightProvider: backend, KegProvider: backend,
		KegSearchProvider: backend, IdentityProvider: backend,
		OrientationCacheTTL: ttl,
	})
	return connectFlightSession(t, ctx, server, nil), ctx, func(d time.Duration) { sb.Advance(d) }
}

func (p *perCallFlightBackend) resolveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resolves
}

func callKegList(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession) *sdkmcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{}})
	require.NoError(t, err)
	return result
}

// A burst of tool calls reuses one resolved orientation; it is resolved again
// once the window passes.
func TestOrientationCacheReusesResolutionWithinWindow(t *testing.T) {
	backend := newPerCallFlightBackend()
	session, ctx, advance := newCachedFlightSession(t, backend, 10*time.Second)

	require.False(t, callKegList(t, ctx, session).IsError)
	before := backend.resolveCount()
	for range 5 {
		require.False(t, callKegList(t, ctx, session).IsError)
	}
	require.Equal(t, before, backend.resolveCount(), "calls inside the window must not resolve again")

	advance(11 * time.Second)
	require.False(t, callKegList(t, ctx, session).IsError)
	require.Equal(t, before+1, backend.resolveCount(), "an expired view must be resolved again")
}

// orient always answers with current authority, and the calls after it see
// what it saw.
func TestOrientationCacheNeverServesOrient(t *testing.T) {
	backend := newPerCallFlightBackend()
	session, ctx, _ := newCachedFlightSession(t, backend, time.Hour)

	require.False(t, callKegList(t, ctx, session).IsError)
	before := backend.resolveCount()
	oriented, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "orient", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, oriented.IsError, extractText(t, oriented))
	require.False(t, callKegList(t, ctx, session).IsError)
	require.Greater(t, backend.resolveCount(), before, "orient must discard the cached view")
}

// A failed call may mean authority changed, so the next call resolves again.
func TestOrientationCacheDropsViewAfterFailure(t *testing.T) {
	backend := newPerCallFlightBackend()
	session, ctx, _ := newCachedFlightSession(t, backend, time.Hour)

	require.False(t, callKegList(t, ctx, session).IsError)
	failed := callCatKeg(t, ctx, session, "@elsewhere/uncovered")
	require.True(t, failed.IsError, extractText(t, failed))
	before := backend.resolveCount()
	require.False(t, callKegList(t, ctx, session).IsError)
	require.Equal(t, before+1, backend.resolveCount())
}

// Without a window every call resolves current authority, which is what a
// server resolving in process (a Hub's own endpoint) wants.
func TestOrientationCacheIsOffByDefault(t *testing.T) {
	backend := newPerCallFlightBackend()
	session, ctx, _ := newCachedFlightSession(t, backend, 0)

	require.False(t, callKegList(t, ctx, session).IsError)
	before := backend.resolveCount()
	require.False(t, callKegList(t, ctx, session).IsError)
	require.Equal(t, before+1, backend.resolveCount())
}
