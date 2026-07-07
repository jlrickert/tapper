package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// These tests pin the startup credential refresh: every CLI invocation
// (which includes `tap mcp` server startup) renews stored hub tokens that
// are expired or about to expire, and leaves fresh ones alone.

// countingRefreshHub answers the OAuth2 refresh grant with a fixed rotated
// pair and counts how many requests it saw.
func countingRefreshHub(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"thub_startupfresh1","token_type":"Bearer",` +
			`"expires_in":900,"refresh_token":"rt-rotated"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func authStorePathCLI(t *testing.T, rt *toolkit.Runtime) string {
	t.Helper()
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: rt})
	require.NoError(t, err)
	return tap.PathService.AuthStorePath()
}

// TestStartup_RefreshesExpiredHubToken runs an ordinary command against a
// store holding an expired OAuth2 credential and expects the rotated pair
// on disk afterwards.
func TestStartup_RefreshesExpiredHubToken(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	srv, calls := countingRefreshHub(t)

	now := sb.Runtime().Clock().Now()
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		srv.URL: {
			AccessToken:   "thub_expiredseed0",
			TokenType:     "Bearer",
			ExpiresAt:     now.Add(-time.Hour),
			RefreshToken:  "rt-old",
			ClientID:      "tapper-cli",
			TokenEndpoint: srv.URL,
		},
	})

	proc := newAuthProcess(t, nil, "version")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	require.Equal(t, int64(1), calls.Load(), "expired token should trigger exactly one refresh")

	store, err := tapper.LoadAuthStore(context.Background(), sb.Runtime(), authStorePathCLI(t, sb.Runtime()))
	require.NoError(t, err)
	entry, ok := store.Get(tapper.CanonicalHubURL(srv.URL))
	require.True(t, ok)
	require.Equal(t, "thub_startupfresh1", entry.AccessToken, "rotated pair must be persisted")
	require.Equal(t, "rt-rotated", entry.RefreshToken)
}

// TestStartup_FreshTokenSkipsNetwork guards the cost contract: when the
// stored token is nowhere near expiry, startup makes no token-endpoint call.
func TestStartup_FreshTokenSkipsNetwork(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	srv, calls := countingRefreshHub(t)

	now := sb.Runtime().Clock().Now()
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		srv.URL: {
			AccessToken:   "thub_stillfresh00",
			TokenType:     "Bearer",
			ExpiresAt:     now.Add(time.Hour),
			RefreshToken:  "rt-old",
			ClientID:      "tapper-cli",
			TokenEndpoint: srv.URL,
		},
	})

	proc := newAuthProcess(t, nil, "version")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	require.Equal(t, int64(0), calls.Load(), "a fresh token must not hit the network at startup")
}

// TestStartup_APITokenNeverRefreshed guards the pasted-token path: entries
// without a refresh token are ignored by the startup refresh even when the
// hub is reachable.
func TestStartup_APITokenNeverRefreshed(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	srv, calls := countingRefreshHub(t)

	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		srv.URL: {AccessToken: "thub_pastedtoken0", TokenType: "Bearer"},
	})

	proc := newAuthProcess(t, nil, "version")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	require.Equal(t, int64(0), calls.Load())

	store, err := tapper.LoadAuthStore(context.Background(), sb.Runtime(), authStorePathCLI(t, sb.Runtime()))
	require.NoError(t, err)
	entry, ok := store.Get(tapper.CanonicalHubURL(srv.URL))
	require.True(t, ok)
	require.Equal(t, "thub_pastedtoken0", entry.AccessToken, "pasted API token must be untouched")
}
