package tapper_test

// Tests for Tap.AuthLogout — the CLI-only logout business logic
// promoted from pkg/cli/cmd_auth.go. Covers the four resolution paths:
// empty store, explicit hub (found + missing), auto-resolve (single +
// multiple), and pins the three byte-exact Formatted strings that both
// this method and the CLI shell emit.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// okWhoAmI returns a stub AuthValidateFn that resolves to the given identity,
// standing in for a successful live whoami probe with no network.
func okWhoAmI(username, displayName string) func(context.Context, *toolkit.Runtime, string, string) (*tapper.WhoAmI, error) {
	return func(context.Context, *toolkit.Runtime, string, string) (*tapper.WhoAmI, error) {
		return &tapper.WhoAmI{Username: username, DisplayName: displayName}, nil
	}
}

// seedStore is a tap-package sibling of the cli-test seedAuthStore:
// it writes an auth.yaml fixture using LoadAuthStore/Set/Save so tests
// don't duplicate the file layout. Returns the first canonical key
// written, convenient for single-hub tests.
func seedStore(t *testing.T, sb *sandbox.Sandbox, tap *tapper.Tap, entries map[string]tapper.AuthEntry) string {
	t.Helper()
	rt := sb.Runtime()
	ctx := sb.Context()
	storePath := tap.PathService.AuthStorePath()

	store, err := tapper.LoadAuthStore(ctx, rt, storePath)
	require.NoError(t, err)
	var first string
	for k, v := range entries {
		canon := tapper.CanonicalHubURL(k)
		if first == "" {
			first = canon
		}
		store.Set(canon, v)
	}
	require.NoError(t, store.Save(ctx, rt, storePath))
	return first
}

func newTestTap(t *testing.T, sb *sandbox.Sandbox) *tapper.Tap {
	t.Helper()
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	return tap
}

// storeFileExists reports whether the on-disk store file exists under
// the sandbox's state root. Used to assert the empty-store post-
// condition after the last logout.
func storeFileExists(t *testing.T, rt *toolkit.Runtime, tap *tapper.Tap) bool {
	t.Helper()
	_, err := rt.Stat(tap.PathService.AuthStorePath(), false)
	return err == nil
}

func TestTap_AuthLogout(t *testing.T) {
	t.Parallel()

	t.Run("empty store returns soft-success with directed string", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)

		res, err := tap.AuthLogout(sb.Context(), tapper.AuthLogoutOptions{})
		require.NoError(t, err)
		require.NotNil(t, res)
		require.False(t, res.Removed)
		require.Equal(t, "", res.HubURL)
		// Byte-for-byte; trailing newline is load-bearing.
		require.Equal(t, "No hub logins stored.\n", res.Formatted)
	})

	t.Run("explicit hub found is removed and file is cleaned up", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {AccessToken: "xyz", TokenType: "Bearer"},
		})

		res, err := tap.AuthLogout(sb.Context(), tapper.AuthLogoutOptions{
			Hub: "HTTPS://Hub.Example.COM/", // canonicalization path
		})
		require.NoError(t, err)
		require.True(t, res.Removed)
		require.Equal(t, "https://hub.example.com", res.HubURL)
		require.Equal(t, "Logged out of https://hub.example.com\n", res.Formatted)

		// Last entry removed → Save deletes the file.
		require.False(t, storeFileExists(t, sb.Runtime(), tap),
			"empty store should leave no auth.yaml on disk")
	})

	t.Run("explicit hub missing is soft-success", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {AccessToken: "xyz"},
		})

		res, err := tap.AuthLogout(sb.Context(), tapper.AuthLogoutOptions{
			Hub: "https://ghost.example.com",
		})
		require.NoError(t, err)
		require.False(t, res.Removed)
		require.Equal(t, "https://ghost.example.com", res.HubURL)
		require.Equal(t, "No login stored for https://ghost.example.com\n", res.Formatted)

		// Store file is untouched because nothing was removed.
		require.True(t, storeFileExists(t, sb.Runtime(), tap),
			"unchanged store should remain on disk")
	})

	t.Run("auto-resolve with single stored hub removes it", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		hub := seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {AccessToken: "xyz"},
		})

		res, err := tap.AuthLogout(sb.Context(), tapper.AuthLogoutOptions{})
		require.NoError(t, err)
		require.True(t, res.Removed)
		require.Equal(t, hub, res.HubURL)
		require.Equal(t, "Logged out of "+hub+"\n", res.Formatted)
	})

	t.Run("auto-resolve with multiple hubs errors", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub-a.example.com": {AccessToken: "a"},
			"https://hub-b.example.com": {AccessToken: "b"},
		})

		res, err := tap.AuthLogout(sb.Context(), tapper.AuthLogoutOptions{})
		require.Error(t, err)
		require.Nil(t, res)
		// Multi-hub error shape is "(found: [...])", shared with
		// AuthStatus — callers grepping this string in both error
		// paths would otherwise drift.
		require.Contains(t, err.Error(), "--hub is required when multiple hubs are stored")
		require.Contains(t, err.Error(), "hub-a.example.com")
		require.Contains(t, err.Error(), "hub-b.example.com")
	})

	t.Run("context cancellation threads through LoadAuthStore", func(t *testing.T) {
		t.Parallel()
		// Defensive: confirm the method respects ctx at the seam. Not
		// every step inside LoadAuthStore honors cancellation (see the
		// _ = ctx note there), but wiring a canceled context at the
		// outer call should not panic or hang. We cancel immediately
		// and just require a deterministic completion.
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)

		ctx, cancel := context.WithCancel(sb.Context())
		cancel()
		_, _ = tap.AuthLogout(ctx, tapper.AuthLogoutOptions{})
	})
}

// TestTap_AuthStatus covers the live-validation rendering paths at the Tap
// layer, driving the AuthValidateFn seam so no network is touched. The
// Formatted string is what both CLI and MCP emit, so these assertions pin its
// shape; the parity test then proves the two surfaces stay byte-identical.
func TestTap_AuthStatus(t *testing.T) {
	t.Parallel()

	t.Run("valid token shows account and display name", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		tok := "thub_validtokenAAAA"
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {AccessToken: tok, TokenType: "Bearer"},
		})
		tap.AuthValidateFn = okWhoAmI("alice", "Alice Liddell")

		res, err := tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{})
		require.NoError(t, err)
		require.True(t, res.Valid)
		require.Equal(t, "alice", res.Account)
		require.Equal(t, "Alice Liddell", res.DisplayName)
		require.Equal(t, tok[:12]+"...", res.TokenPrefix)
		require.Contains(t, res.Formatted, "hub.example.com\n")
		require.Contains(t, res.Formatted, "✓ Logged in as alice (Alice Liddell)")
		// Host appears once (the header line), not repeated in the status line.
		require.Equal(t, 1, strings.Count(res.Formatted, "hub.example.com"))
		require.Contains(t, res.Formatted, "- Token: "+tok[:12]+"... (Bearer)")
		require.NotContains(t, res.Formatted, tok)
	})

	t.Run("valid token without display name omits parens", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {AccessToken: "thub_nodisplayname00", TokenType: "Bearer"},
		})
		tap.AuthValidateFn = okWhoAmI("bob", "")

		res, err := tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{})
		require.NoError(t, err)
		require.Contains(t, res.Formatted, "Logged in as bob")
		require.NotContains(t, res.Formatted, "bob (")
	})

	t.Run("rejected token renders x line without failing", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {AccessToken: "thub_rejected000000", TokenType: "Bearer"},
		})
		tap.AuthValidateFn = func(context.Context, *toolkit.Runtime, string, string) (*tapper.WhoAmI, error) {
			return nil, fmt.Errorf("auth: %w (401 Unauthorized); check it", tapper.ErrTokenRejected)
		}

		res, err := tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{})
		require.NoError(t, err)
		require.False(t, res.Valid)
		require.Contains(t, res.Formatted, "x Failed to validate token: hub rejected the token (401 Unauthorized)")
		// "auth: " package prefix is trimmed from the displayed reason.
		require.NotContains(t, res.Formatted, "auth: hub rejected")
		require.Contains(t, res.Formatted, "- Token: thub_rejecte... (Bearer)")
	})

	t.Run("unreachable hub degrades to neutral line", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {AccessToken: "thub_unreachable000", TokenType: "Bearer"},
		})
		tap.AuthValidateFn = func(context.Context, *toolkit.Runtime, string, string) (*tapper.WhoAmI, error) {
			return nil, fmt.Errorf("auth: contact hub: dial tcp: refused")
		}

		res, err := tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{})
		require.NoError(t, err)
		require.False(t, res.Valid)
		require.Contains(t, res.Formatted, "! Could not reach hub to validate token (offline?)")
		require.Contains(t, res.Formatted, "- Token: thub_unreach... (Bearer)")
	})

	t.Run("offline skips validation entirely", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {AccessToken: "thub_offline0000000", TokenType: "Bearer"},
		})
		tap.AuthValidateFn = func(context.Context, *toolkit.Runtime, string, string) (*tapper.WhoAmI, error) {
			t.Fatal("AuthValidateFn must not be called in --offline mode")
			return nil, nil
		}

		res, err := tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{Offline: true})
		require.NoError(t, err)
		require.False(t, res.Valid)
		require.Empty(t, res.Account)
		require.Contains(t, res.Formatted, "Logged in (offline; token not validated)")
		require.Contains(t, res.Formatted, "- Token: thub_offline... (Bearer)")
		require.Equal(t, "token", res.LoginMethod)
	})

	t.Run("multiple hubs without hub reports all sorted and validates each", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub-b.example.com": {AccessToken: "thub_betatoken0000", TokenType: "Bearer"},
			"https://hub-a.example.com": {AccessToken: "thub_alphatoken00", TokenType: "Bearer"},
		})

		var calls []string
		tap.AuthValidateFn = func(_ context.Context, _ *toolkit.Runtime, hub, token string) (*tapper.WhoAmI, error) {
			calls = append(calls, hub+" "+token)
			switch hub {
			case "https://hub-a.example.com":
				return &tapper.WhoAmI{Username: "alice"}, nil
			case "https://hub-b.example.com":
				return &tapper.WhoAmI{Username: "bob"}, nil
			default:
				return nil, fmt.Errorf("unexpected hub %s", hub)
			}
		}

		res, err := tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{})
		require.NoError(t, err)
		require.True(t, res.Present)
		require.Empty(t, res.HubURL, "multi-hub status has no single scalar hub")
		require.Len(t, res.Hubs, 2)
		require.Equal(t, "https://hub-a.example.com", res.Hubs[0].HubURL)
		require.Equal(t, "https://hub-b.example.com", res.Hubs[1].HubURL)
		require.True(t, res.Hubs[0].Valid)
		require.True(t, res.Hubs[1].Valid)
		require.Equal(t, []string{
			"https://hub-a.example.com thub_alphatoken00",
			"https://hub-b.example.com thub_betatoken0000",
		}, calls)
		require.Contains(t, res.Formatted, "hub-a.example.com")
		require.Contains(t, res.Formatted, "hub-b.example.com")
		require.Less(t,
			strings.Index(res.Formatted, "hub-a.example.com"),
			strings.Index(res.Formatted, "hub-b.example.com"),
			"store.Hubs sorted order should drive output order")
		require.Contains(t, res.Formatted, "Logged in as alice")
		require.Contains(t, res.Formatted, "Logged in as bob")
		require.Contains(t, res.Formatted, "\n\nhub-b.example.com\n")
	})

	t.Run("multiple hubs offline skips validation for all", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub-a.example.com": {AccessToken: "thub_alphatoken00", TokenType: "Bearer"},
			"https://hub-b.example.com": {AccessToken: "thub_betatoken0000", TokenType: "Bearer"},
		})
		tap.AuthValidateFn = func(context.Context, *toolkit.Runtime, string, string) (*tapper.WhoAmI, error) {
			t.Fatal("AuthValidateFn must not be called in --offline mode")
			return nil, nil
		}

		res, err := tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{Offline: true})
		require.NoError(t, err)
		require.Len(t, res.Hubs, 2)
		require.Equal(t, 2, strings.Count(res.Formatted, "offline; token not validated"))
		require.False(t, res.Hubs[0].Valid)
		require.False(t, res.Hubs[1].Valid)
	})

	t.Run("explicit hub still reports one hub from a multi-hub store", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub-a.example.com": {AccessToken: "thub_alphatoken00", TokenType: "Bearer"},
			"https://hub-b.example.com": {AccessToken: "thub_betatoken0000", TokenType: "Bearer"},
		})

		res, err := tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{
			Hub:     "HTTPS://Hub-B.Example.COM/",
			Offline: true,
		})
		require.NoError(t, err)
		require.True(t, res.Present)
		require.Equal(t, "https://hub-b.example.com", res.HubURL)
		require.Len(t, res.Hubs, 1)
		require.Equal(t, "https://hub-b.example.com", res.Hubs[0].HubURL)
		require.Contains(t, res.Formatted, "hub-b.example.com")
		require.NotContains(t, res.Formatted, "hub-a.example.com")
	})

	t.Run("scope shown only when present", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		tap.AuthValidateFn = okWhoAmI("alice", "")

		// No scope → no scope line.
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {AccessToken: "thub_noscope0000000", TokenType: "Bearer"},
		})
		res, err := tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{})
		require.NoError(t, err)
		require.NotContains(t, res.Formatted, "Scopes:")

		// Scope present (OAuth2 flow) → comma-joined line.
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {AccessToken: "oauth2_scoped000000", TokenType: "Bearer", Scope: "read write"},
		})
		res, err = tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{})
		require.NoError(t, err)
		require.Contains(t, res.Formatted, "- Scopes: read, write")
	})

	t.Run("expiry line shown only when known", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		tap.AuthValidateFn = okWhoAmI("alice", "")

		// Unknown expiry → no Expires line.
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {AccessToken: "thub_noexpiry000000", TokenType: "Bearer"},
		})
		res, err := tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{})
		require.NoError(t, err)
		require.NotContains(t, res.Formatted, "- Expires:")

		// Future expiry → Expires line with relative suffix.
		exp := sb.Runtime().Clock().Now().Add(24 * time.Hour)
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {AccessToken: "thub_hasexpiry00000", TokenType: "Bearer", ExpiresAt: exp},
		})
		res, err = tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{})
		require.NoError(t, err)
		require.Contains(t, res.Formatted, "- Expires:")
		require.Contains(t, res.Formatted, "(in 24h0m0s)")
	})

	t.Run("short token redacts to placeholder", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {AccessToken: "abc"},
		})
		tap.AuthValidateFn = okWhoAmI("alice", "")

		res, err := tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{})
		require.NoError(t, err)
		require.Equal(t, "[set]", res.TokenPrefix)
		require.Contains(t, res.Formatted, "- Token: [set]")
	})

	t.Run("expired oauth token is refreshed before reporting", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"thub_refreshednew99","token_type":"Bearer",` +
				`"expires_in":900,"refresh_token":"rt-rotated"}`))
		}))
		defer srv.Close()

		past := sb.Runtime().Clock().Now().Add(-time.Hour)
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {
				AccessToken:   "thub_expiredtok00",
				TokenType:     "Bearer",
				ExpiresAt:     past,
				RefreshToken:  "rt-old",
				ClientID:      "tapper-cli",
				TokenEndpoint: srv.URL,
			},
		})
		tap.AuthValidateFn = okWhoAmI("alice", "")

		res, err := tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{})
		require.NoError(t, err)
		require.True(t, res.Valid)
		// Reported token is the refreshed one ("thub_refreshednew99"[:12]); the
		// prefix lives in the structured field for consumers that want it...
		require.Equal(t, "thub_refresh...", res.TokenPrefix)
		require.Contains(t, res.Formatted, "✓ Logged in as alice")
		// A device-flow login (client + refresh + token endpoint set) renders
		// the browser method with the auto-renew note...
		require.Equal(t, "device", res.LoginMethod)
		require.True(t, res.Renewable)
		require.Contains(t, res.Formatted, "- Method: browser (device flow), renews automatically")
		// ...but the access-token prefix (never shown in the account portal) and
		// the silently-renewed expiry are both omitted from the display.
		require.NotContains(t, res.Formatted, "- Token:")
		require.NotContains(t, res.Formatted, "- Expires:")
	})

	t.Run("refresh rejection adopts rotated disk token before reporting", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)

		const hub = "https://hub.example.com"
		now := sb.Runtime().Clock().Now()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			winner := &tapper.AuthStore{}
			winner.Set(hub, tapper.AuthEntry{
				AccessToken:   "thub_siblingwin00",
				TokenType:     "Bearer",
				ExpiresAt:     now.Add(time.Hour),
				RefreshToken:  "rt-sibling",
				ClientID:      "tapper-cli",
				TokenEndpoint: "unused",
			})
			require.NoError(t, winner.Save(sb.Context(), sb.Runtime(), tap.PathService.AuthStorePath()))

			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		}))
		defer srv.Close()

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
		var validatedToken string
		tap.AuthValidateFn = func(_ context.Context, _ *toolkit.Runtime, _ string, token string) (*tapper.WhoAmI, error) {
			validatedToken = token
			return &tapper.WhoAmI{Username: "alice"}, nil
		}

		res, err := tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{})
		require.NoError(t, err)
		require.True(t, res.Valid)
		require.Equal(t, "thub_siblingwin00", validatedToken)
		require.Equal(t, "thub_sibling...", res.TokenPrefix)
	})

	t.Run("offline does not refresh an expired token", func(t *testing.T) {
		t.Parallel()
		sb := NewSandbox(t)
		tap := newTestTap(t, sb)
		// A token endpoint that fails the test if it is ever called.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("offline status must not contact the token endpoint")
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		past := sb.Runtime().Clock().Now().Add(-time.Hour)
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {
				AccessToken:   "thub_expiredtok00",
				TokenType:     "Bearer",
				ExpiresAt:     past,
				RefreshToken:  "rt-old",
				ClientID:      "tapper-cli",
				TokenEndpoint: srv.URL,
			},
		})

		res, err := tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{Offline: true})
		require.NoError(t, err)
		require.Contains(t, res.Formatted, "offline; token not validated")
	})
}
