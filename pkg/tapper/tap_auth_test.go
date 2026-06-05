package tapper_test

// Tests for Tap.AuthLogout — the CLI-only logout business logic
// promoted from pkg/cli/cmd_auth.go. Covers the four resolution paths:
// empty store, explicit hub (found + missing), auto-resolve (single +
// multiple), and pins the three byte-exact Formatted strings that both
// this method and the CLI shell emit.

import (
	"context"
	"fmt"
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
		require.Contains(t, res.Formatted, "✓ Logged in to hub.example.com account alice (Alice Liddell)")
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
		require.Contains(t, res.Formatted, "account bob")
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
		require.Contains(t, res.Formatted, "Logged in to hub.example.com (offline; token not validated)")
		require.Contains(t, res.Formatted, "- Token: thub_offline... (Bearer)")
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
		require.NotContains(t, res.Formatted, "Token scopes:")

		// Scope present (OAuth2 flow) → comma-joined line.
		seedStore(t, sb, tap, map[string]tapper.AuthEntry{
			"https://hub.example.com": {AccessToken: "oauth2_scoped000000", TokenType: "Bearer", Scope: "read write"},
		})
		res, err = tap.AuthStatus(sb.Context(), tapper.AuthStatusOptions{})
		require.NoError(t, err)
		require.Contains(t, res.Formatted, "- Token scopes: read, write")
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
}
