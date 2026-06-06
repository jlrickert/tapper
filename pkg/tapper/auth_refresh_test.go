package tapper_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// rotatingTokenHub returns an httptest server that answers the refresh grant
// with a fixed rotated pair, and captures the last form it received so tests
// can assert the request shape.
func rotatingTokenHub(t *testing.T, gotForm *url.Values) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if gotForm != nil {
			*gotForm = r.PostForm
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"thub_refreshednew99","token_type":"Bearer",` +
			`"expires_in":900,"scope":"read","refresh_token":"rt-rotated"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRefreshHubToken_RotatesAndRenews(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	var got url.Values
	srv := rotatingTokenHub(t, &got)

	entry := &tapper.AuthEntry{
		AccessToken:   "thub_oldoldold00",
		TokenType:     "Bearer",
		RefreshToken:  "rt-old",
		ClientID:      "tapper-cli",
		TokenEndpoint: srv.URL,
	}
	next, err := tapper.RefreshHubToken(fx.Context(), fx.Runtime(), "https://hub.example.com", entry)
	require.NoError(t, err)
	require.Equal(t, "thub_refreshednew99", next.AccessToken)
	require.Equal(t, "rt-rotated", next.RefreshToken)
	require.Equal(t, "tapper-cli", next.ClientID)
	require.Equal(t, srv.URL, next.TokenEndpoint)
	require.False(t, next.ExpiresAt.IsZero(), "expiry should be recomputed from expires_in")

	// The CLI must present the refresh grant with the stored refresh token + client.
	require.Equal(t, "refresh_token", got.Get("grant_type"))
	require.Equal(t, "rt-old", got.Get("refresh_token"))
	require.Equal(t, "tapper-cli", got.Get("client_id"))
}

func TestRefreshHubToken_RejectedRefreshErrors(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	t.Cleanup(srv.Close)

	entry := &tapper.AuthEntry{RefreshToken: "spent", ClientID: "tapper-cli", TokenEndpoint: srv.URL}
	_, err := tapper.RefreshHubToken(fx.Context(), fx.Runtime(), "https://hub", entry)
	require.Error(t, err)
}

func TestRefreshHubToken_NoRefreshTokenErrors(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	_, err := tapper.RefreshHubToken(fx.Context(), fx.Runtime(), "https://hub", &tapper.AuthEntry{AccessToken: "x"})
	require.Error(t, err)
}

// TestAuthStoreTokenResolver_RefreshesExpiredToken proves the resolver renews
// an expired OAuth2 token in place and persists the rotated pair, so the next
// command reuses the refreshed token without touching the network again.
func TestAuthStoreTokenResolver_RefreshesExpiredToken(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	path := authStorePath(t, fx)

	var got url.Values
	srv := rotatingTokenHub(t, &got)

	// Seed an expired entry (1h in the past on the mock clock) carrying a
	// refresh token and the token endpoint, keyed by the hub host the target
	// will resolve to.
	hubKey := tapper.CanonicalHubURL(srv.URL)
	past := fx.Runtime().Clock().Now().Add(-time.Hour)
	store := &tapper.AuthStore{}
	store.Set(hubKey, tapper.AuthEntry{
		AccessToken:   "thub_expiredtok00",
		TokenType:     "Bearer",
		ExpiresAt:     past,
		RefreshToken:  "rt-old",
		ClientID:      "tapper-cli",
		TokenEndpoint: srv.URL,
	})
	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), path))

	resolver := tapper.NewAuthStoreTokenResolver(store, fx.Runtime(), path)
	target := keg.Target{Url: srv.URL + "/api/v1/@me/kegs/@demo/nodes"}
	tok := resolver.ResolveToken(&target)
	require.Equal(t, "thub_refreshednew99", tok, "resolver should return the refreshed access token")

	// The rotated pair must be persisted for the next invocation.
	reloaded, err := tapper.LoadAuthStore(fx.Context(), fx.Runtime(), path)
	require.NoError(t, err)
	got2, ok := reloaded.Get(hubKey)
	require.True(t, ok)
	require.Equal(t, "thub_refreshednew99", got2.AccessToken)
	require.Equal(t, "rt-rotated", got2.RefreshToken)
	require.False(t, got2.ExpiresAt.IsZero())
}
