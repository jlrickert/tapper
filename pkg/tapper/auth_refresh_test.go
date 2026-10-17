package tapper_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// rotatingTokenHub returns an httptest server that answers the refresh grant
// with a fixed rotated pair, and captures the last form it received so tests
// can assert the request shape.
func rotatingTokenHub(t *testing.T, gotForm *url.Values) *httptest.Server {
	t.Helper()
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

// TestAuthStoreTokenResolver_ConcurrentResolvesDuringRefresh resolves tokens
// from many goroutines while one of them refreshes an expired entry, so the
// race detector sees the store's reads and the refresh's write overlap.
func TestAuthStoreTokenResolver_ConcurrentResolvesDuringRefresh(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	path := authStorePath(t, fx)
	srv := rotatingTokenHub(t, nil)

	expiredKey := tapper.CanonicalHubURL(srv.URL)
	const freshHub = "https://fresh.example.com"
	store := &tapper.AuthStore{}
	store.Set(expiredKey, tapper.AuthEntry{
		AccessToken:   "thub_expiredtok00",
		TokenType:     "Bearer",
		ExpiresAt:     fx.Runtime().Clock().Now().Add(-time.Hour),
		RefreshToken:  "rt-old",
		ClientID:      "tapper-cli",
		TokenEndpoint: srv.URL,
	})
	store.Set(tapper.CanonicalHubURL(freshHub), tapper.AuthEntry{AccessToken: "thub_freshtok000"})
	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), path))

	resolver := tapper.NewAuthStoreTokenResolver(store, fx.Runtime(), path)
	expiredTarget := keg.Target{Url: srv.URL + "/api/v1/@me/kegs/@demo/nodes"}
	freshTarget := keg.Target{Url: freshHub + "/api/v1/@me/kegs/@demo/nodes"}

	const workers = 16
	tokens := make([]string, workers)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				tokens[i] = resolver.ResolveToken(&expiredTarget)
				return
			}
			tokens[i] = resolver.ResolveToken(&freshTarget)
			_ = store.Hubs()
		}(i)
	}
	wg.Wait()

	for i, tok := range tokens {
		if i%2 == 0 {
			require.Equal(t, "thub_refreshednew99", tok, "worker %d", i)
			continue
		}
		require.Equal(t, "thub_freshtok000", tok, "worker %d", i)
	}
}

func TestTap_AuthRefreshAll_RefreshesOnlyRenewableExpiringEntries(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	tap := newTestTap(t, sb)

	var calls atomic.Int64
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"thub_refreshednew99","token_type":"Bearer",` +
			`"expires_in":900,"refresh_token":"rt-rotated"}`))
	}))
	t.Cleanup(srv.Close)

	now := sb.Runtime().Clock().Now()
	seedStore(t, sb, tap, map[string]tapper.AuthEntry{
		"https://api.example.com": {
			AccessToken: "thub_apitoken000",
			ExpiresAt:   now.Add(-time.Hour),
		},
		"https://fresh.example.com": {
			AccessToken:   "thub_fresh00000",
			ExpiresAt:     now.Add(time.Hour),
			RefreshToken:  "rt-fresh",
			ClientID:      "tapper-cli",
			TokenEndpoint: srv.URL,
		},
		"https://no-expiry.example.com": {
			AccessToken:   "thub_noexpiry00",
			RefreshToken:  "rt-no-expiry",
			ClientID:      "tapper-cli",
			TokenEndpoint: srv.URL,
		},
		"https://soon.example.com": {
			AccessToken:   "thub_soon000000",
			ExpiresAt:     now.Add(10 * time.Second),
			RefreshToken:  "rt-soon",
			ClientID:      "tapper-cli",
			TokenEndpoint: srv.URL,
		},
	})

	tap.AuthRefreshAll(sb.Context())
	require.Equal(t, int64(1), calls.Load(), "only the renewable near-expiry entry should refresh")

	reloaded, err := tapper.LoadAuthStore(sb.Context(), sb.Runtime(), tap.PathService.AuthStorePath())
	require.NoError(t, err)
	api, ok := reloaded.Get("https://api.example.com")
	require.True(t, ok)
	require.Equal(t, "thub_apitoken000", api.AccessToken)
	fresh, ok := reloaded.Get("https://fresh.example.com")
	require.True(t, ok)
	require.Equal(t, "thub_fresh00000", fresh.AccessToken)
	noExpiry, ok := reloaded.Get("https://no-expiry.example.com")
	require.True(t, ok)
	require.Equal(t, "thub_noexpiry00", noExpiry.AccessToken)
	soon, ok := reloaded.Get("https://soon.example.com")
	require.True(t, ok)
	require.Equal(t, "thub_refreshednew99", soon.AccessToken)
	require.Equal(t, "rt-rotated", soon.RefreshToken)
}

func TestTap_AuthRefreshAll_AdoptsDiskAfterRefreshRejected(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	tap := newTestTap(t, sb)

	const hub = "https://hub.example.com"
	now := sb.Runtime().Clock().Now()
	var calls atomic.Int64
	var srv *httptest.Server
	srv = testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		winner := &tapper.AuthStore{}
		winner.Set(hub, tapper.AuthEntry{
			AccessToken:   "thub_siblingwin00",
			TokenType:     "Bearer",
			ExpiresAt:     now.Add(time.Hour),
			RefreshToken:  "rt-sibling",
			ClientID:      "tapper-cli",
			TokenEndpoint: srv.URL,
		})
		require.NoError(t, winner.Save(sb.Context(), sb.Runtime(), tap.PathService.AuthStorePath()))

		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	t.Cleanup(srv.Close)

	seedStore(t, sb, tap, map[string]tapper.AuthEntry{
		hub: {
			AccessToken:   "thub_expiredtok00",
			TokenType:     "Bearer",
			ExpiresAt:     now.Add(-time.Hour),
			RefreshToken:  "rt-consumed",
			ClientID:      "tapper-cli",
			TokenEndpoint: srv.URL,
		},
	})

	tap.AuthRefreshAll(sb.Context())
	require.Equal(t, int64(1), calls.Load(), "refresh should be attempted once before adopting disk")

	reloaded, err := tapper.LoadAuthStore(sb.Context(), sb.Runtime(), tap.PathService.AuthStorePath())
	require.NoError(t, err)
	entry, ok := reloaded.Get(hub)
	require.True(t, ok)
	require.Equal(t, "thub_siblingwin00", entry.AccessToken)
	require.Equal(t, "rt-sibling", entry.RefreshToken)
}

// singleUseTokenHub is a hub that rotates refresh tokens single-use, the way
// a real hub without a grace window does: each POST spends the presented
// token and mints a new pair, and a spent token is refused. It answers after
// delay, so concurrent refreshers overlap.
func singleUseTokenHub(t *testing.T, first string, delay time.Duration, posts *atomic.Int64) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	live := first
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		n := posts.Add(1)
		time.Sleep(delay)
		mu.Lock()
		defer mu.Unlock()
		if r.PostForm.Get("refresh_token") != live {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		live = fmt.Sprintf("rt-%d", n)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"thub_access%04d","token_type":"Bearer","expires_in":900,"refresh_token":%q}`, n, live)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Two processes sharing one auth store, each with its own in-memory copy,
// find the stored token expired at once. Only one of them spends the refresh
// token; the other adopts the pair it persisted.
func TestRefreshAcrossStoresSpendsRefreshTokenOnce(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	path := authStorePath(t, fx)
	var posts atomic.Int64
	srv := singleUseTokenHub(t, "rt-old", 200*time.Millisecond, &posts)
	hubKey := tapper.CanonicalHubURL(srv.URL)
	seed := &tapper.AuthStore{}
	seed.Set(hubKey, tapper.AuthEntry{
		AccessToken:   "thub_expiredtok00",
		TokenType:     "Bearer",
		ExpiresAt:     fx.Runtime().Clock().Now().Add(-time.Hour),
		RefreshToken:  "rt-old",
		ClientID:      "tapper-cli",
		TokenEndpoint: srv.URL,
	})
	require.NoError(t, seed.Save(fx.Context(), fx.Runtime(), path))

	const processes = 2
	target := keg.Target{Url: srv.URL + "/api/v1/@me/kegs/@demo/nodes"}
	tokens := make([]string, processes)
	var wg sync.WaitGroup
	for i := range processes {
		store, err := tapper.LoadAuthStore(fx.Context(), fx.Runtime(), path)
		require.NoError(t, err)
		resolver := tapper.NewAuthStoreTokenResolver(store, fx.Runtime(), path)
		wg.Add(1)
		go func() {
			defer wg.Done()
			tokens[i] = resolver.ResolveToken(&target)
		}()
	}
	wg.Wait()

	require.Equal(t, int64(1), posts.Load(), "the refresh token is spent once")
	for i, tok := range tokens {
		require.Equal(t, "thub_access0001", tok, "process %d uses the one refreshed token", i)
	}
	stored, err := tapper.LoadAuthStore(fx.Context(), fx.Runtime(), path)
	require.NoError(t, err)
	entry, ok := stored.Get(hubKey)
	require.True(t, ok)
	require.Equal(t, "rt-1", entry.RefreshToken)
}

// A process whose token is still fresh never waits on another process's
// refresh of the same store.
func TestRefreshLockDoesNotBlockFreshTokens(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	path := authStorePath(t, fx)
	var posts atomic.Int64
	srv := singleUseTokenHub(t, "rt-old", 2*time.Second, &posts)
	expiredKey := tapper.CanonicalHubURL(srv.URL)
	const freshHub = "https://fresh.example.com"
	seed := &tapper.AuthStore{}
	seed.Set(expiredKey, tapper.AuthEntry{
		AccessToken:   "thub_expiredtok00",
		ExpiresAt:     fx.Runtime().Clock().Now().Add(-time.Hour),
		RefreshToken:  "rt-old",
		ClientID:      "tapper-cli",
		TokenEndpoint: srv.URL,
	})
	seed.Set(tapper.CanonicalHubURL(freshHub), tapper.AuthEntry{AccessToken: "thub_freshtok000"})
	require.NoError(t, seed.Save(fx.Context(), fx.Runtime(), path))

	slow, err := tapper.LoadAuthStore(fx.Context(), fx.Runtime(), path)
	require.NoError(t, err)
	refreshing := make(chan struct{})
	go func() {
		defer close(refreshing)
		tapper.NewAuthStoreTokenResolver(slow, fx.Runtime(), path).
			ResolveToken(&keg.Target{Url: srv.URL + "/api/v1/@me/kegs/@demo/nodes"})
	}()
	require.Eventually(t, func() bool { return posts.Load() == 1 }, 2*time.Second, 5*time.Millisecond)

	other, err := tapper.LoadAuthStore(fx.Context(), fx.Runtime(), path)
	require.NoError(t, err)
	start := time.Now()
	tok := tapper.NewAuthStoreTokenResolver(other, fx.Runtime(), path).
		ResolveToken(&keg.Target{Url: freshHub + "/api/v1/@me/kegs/@demo/nodes"})
	require.Equal(t, "thub_freshtok000", tok)
	require.Less(t, time.Since(start), time.Second, "a fresh token does not wait for the refresh lock")
	<-refreshing
}
