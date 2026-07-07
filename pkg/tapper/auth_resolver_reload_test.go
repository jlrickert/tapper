package tapper_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// Refresh tokens are single-use: when another process (a parallel tap
// command, `tap auth login`) has already rotated the pair and persisted it,
// the resolver must adopt the on-disk entry instead of spending a refresh
// token that is already consumed. These tests pin both adoption points:
// before contacting the hub, and after the hub rejects a spent token.

// TestAuthStoreTokenResolver_AdoptsRotatedPairFromDisk seeds a stale entry
// in the resolver's in-memory store while the on-disk store already holds a
// fresh rotated pair. The resolver must return the disk token without any
// hub round trip.
func TestAuthStoreTokenResolver_AdoptsRotatedPairFromDisk(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	path := authStorePath(t, fx)

	var hubCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hubCalls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	t.Cleanup(srv.Close)

	hubKey := tapper.CanonicalHubURL(srv.URL)
	now := fx.Runtime().Clock().Now()

	// In-memory view: expired access token, refresh token already consumed
	// by a sibling process (the hub would reject it).
	memStore := &tapper.AuthStore{}
	memStore.Set(hubKey, tapper.AuthEntry{
		AccessToken:   "thub_staletoken00",
		ExpiresAt:     now.Add(-time.Hour),
		RefreshToken:  "rt-consumed",
		ClientID:      "tapper-cli",
		TokenEndpoint: srv.URL,
	})

	// On disk: the sibling's rotated pair, still fresh.
	diskStore := &tapper.AuthStore{}
	diskStore.Set(hubKey, tapper.AuthEntry{
		AccessToken:   "thub_diskfresh00",
		ExpiresAt:     now.Add(time.Hour),
		RefreshToken:  "rt-disk",
		ClientID:      "tapper-cli",
		TokenEndpoint: srv.URL,
	})
	require.NoError(t, diskStore.Save(fx.Context(), fx.Runtime(), path))

	resolver := tapper.NewAuthStoreTokenResolver(memStore, fx.Runtime(), path)
	target := keg.Target{Url: srv.URL + "/api/v1/@me/kegs/demo/nodes"}

	tok := resolver.ResolveToken(&target)
	require.Equal(t, "thub_diskfresh00", tok, "resolver should adopt the rotated pair from disk")
	require.Equal(t, int64(0), hubCalls.Load(), "no hub call when disk already holds a fresh pair")

	// The adopted entry must land in the in-memory store so the next
	// resolve is a pure cache hit.
	adopted, ok := memStore.Get(hubKey)
	require.True(t, ok)
	require.Equal(t, "thub_diskfresh00", adopted.AccessToken)
}

// TestAuthStoreTokenResolver_AdoptsDiskAfterRefreshRejected simulates losing
// the rotation race mid-flight: disk is as stale as memory when the refresh
// starts, the hub rejects our (already consumed) refresh token, and by then
// the winning process has persisted its rotated pair. The resolver must pick
// that pair up instead of failing.
func TestAuthStoreTokenResolver_AdoptsDiskAfterRefreshRejected(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	path := authStorePath(t, fx)

	now := fx.Runtime().Clock().Now()
	staleEntry := func(endpoint string) tapper.AuthEntry {
		return tapper.AuthEntry{
			AccessToken:   "thub_staletoken00",
			ExpiresAt:     now.Add(-time.Hour),
			RefreshToken:  "rt-consumed",
			ClientID:      "tapper-cli",
			TokenEndpoint: endpoint,
		}
	}

	var hubKeySlot atomic.Value // set once the server URL is known
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The sibling process wins the race while our request is in flight:
		// its rotated pair lands on disk before our rejection arrives.
		hubKey := hubKeySlot.Load().(string)
		winner := &tapper.AuthStore{}
		winner.Set(hubKey, tapper.AuthEntry{
			AccessToken:  "thub_siblingwin00",
			ExpiresAt:    now.Add(time.Hour),
			RefreshToken: "rt-sibling",
			ClientID:     "tapper-cli",
		})
		require.NoError(t, winner.Save(fx.Context(), fx.Runtime(), path))

		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	t.Cleanup(srv.Close)

	hubKey := tapper.CanonicalHubURL(srv.URL)
	hubKeySlot.Store(hubKey)

	// Memory and disk agree on the stale pair at the outset, so the
	// before-refresh reload finds nothing fresher and the hub is contacted.
	memStore := &tapper.AuthStore{}
	memStore.Set(hubKey, staleEntry(srv.URL))
	diskStore := &tapper.AuthStore{}
	diskStore.Set(hubKey, staleEntry(srv.URL))
	require.NoError(t, diskStore.Save(fx.Context(), fx.Runtime(), path))

	resolver := tapper.NewAuthStoreTokenResolver(memStore, fx.Runtime(), path)
	target := keg.Target{Url: srv.URL + "/api/v1/@me/kegs/demo/nodes"}

	tok := resolver.ResolveToken(&target)
	require.Equal(t, "thub_siblingwin00", tok,
		"a rejected refresh must fall back to the pair the winning process persisted")
}
