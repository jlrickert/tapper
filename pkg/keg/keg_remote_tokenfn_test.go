package keg_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// authHeaderRecorder is an httptest server that answers every request with
// 204 (a valid NodeExists reply) and records the Authorization header of
// each request in order.
type authHeaderRecorder struct {
	mu      sync.Mutex
	headers []string
	srv     *httptest.Server
}

func newAuthHeaderRecorder(t *testing.T) *authHeaderRecorder {
	t.Helper()
	rec := &authHeaderRecorder{}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.headers = append(rec.headers, r.Header.Get("Authorization"))
		rec.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

func (rec *authHeaderRecorder) recorded() []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]string(nil), rec.headers...)
}

// TestRemoteKegTokenFnPerRequest proves that an installed token source is
// consulted on every request, so a keg held for a long time (e.g. cached by
// a long-running MCP server) picks up rotated credentials instead of
// pinning the token it was constructed with.
func TestRemoteKegTokenFnPerRequest(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	rec := newAuthHeaderRecorder(t)

	rk := kegpkg.NewRemoteKeg(rec.srv.URL, "static-token", fx.Runtime())

	current := "token-one"
	var mu sync.Mutex
	rk.SetTokenFn(func() string {
		mu.Lock()
		defer mu.Unlock()
		return current
	})

	ctx := context.Background()
	_, err := rk.NodeExists(ctx, kegpkg.NodeId{ID: 1})
	require.NoError(t, err)
	require.Equal(t, "token-one", rk.Token(), "Token() should report the per-request source")

	mu.Lock()
	current = "token-two"
	mu.Unlock()

	_, err = rk.NodeExists(ctx, kegpkg.NodeId{ID: 1})
	require.NoError(t, err)

	require.Equal(t, []string{"Bearer token-one", "Bearer token-two"}, rec.recorded(),
		"each request must carry the token source's value at call time")

	// Reverting to nil falls back to the construction-time static token.
	rk.SetTokenFn(nil)
	require.Equal(t, "static-token", rk.Token())
}

// changingResolver returns a different token on each ResolveToken call
// ("resolved-1", "resolved-2", ...), so tests can detect whether a caller
// re-resolves per request or pinned a single resolution.
type changingResolver struct {
	count atomic.Int64
}

func (r *changingResolver) ResolveToken(_ *kegpkg.Target) string {
	return fmt.Sprintf("resolved-%d", r.count.Add(1))
}

// TestNewKegFromTarget_ResolverKegReResolvesPerRequest proves the wiring:
// a keg built from a target with a resolver (and no inline/env token)
// re-runs the resolution chain per request.
func TestNewKegFromTarget_ResolverKegReResolvesPerRequest(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	rec := newAuthHeaderRecorder(t)

	resolver := &changingResolver{}
	target := kegpkg.Target{Url: rec.srv.URL}
	k, err := kegpkg.NewKegFromTarget(context.Background(), target, fx.Runtime(),
		kegpkg.WithTokenResolver(resolver))
	require.NoError(t, err)
	rk, ok := k.(*kegpkg.RemoteKeg)
	require.True(t, ok)

	ctx := context.Background()
	_, err = rk.NodeExists(ctx, kegpkg.NodeId{ID: 1})
	require.NoError(t, err)
	_, err = rk.NodeExists(ctx, kegpkg.NodeId{ID: 1})
	require.NoError(t, err)

	got := rec.recorded()
	require.Len(t, got, 2)
	// Construction itself resolves once for the static fallback, so the
	// per-request values start wherever the resolver is by the first call.
	// What matters is that the two requests saw *different* tokens.
	require.NotEqual(t, got[0], got[1], "requests must re-resolve, not pin the construction token")
}

// TestNewKegFromTarget_InlineTokenWinsOverResolver guards precedence: an
// inline target token beats the resolver on every request even with the
// per-request source installed.
func TestNewKegFromTarget_InlineTokenWinsOverResolver(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	rec := newAuthHeaderRecorder(t)

	resolver := &changingResolver{}
	target := kegpkg.Target{Url: rec.srv.URL, Token: "inline-token"}
	k, err := kegpkg.NewKegFromTarget(context.Background(), target, fx.Runtime(),
		kegpkg.WithTokenResolver(resolver))
	require.NoError(t, err)
	rk, ok := k.(*kegpkg.RemoteKeg)
	require.True(t, ok)

	ctx := context.Background()
	_, err = rk.NodeExists(ctx, kegpkg.NodeId{ID: 1})
	require.NoError(t, err)
	_, err = rk.NodeExists(ctx, kegpkg.NodeId{ID: 1})
	require.NoError(t, err)

	require.Equal(t, []string{"Bearer inline-token", "Bearer inline-token"}, rec.recorded())
}
