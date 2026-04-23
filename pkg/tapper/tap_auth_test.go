package tapper_test

// Tests for Tap.AuthLogout — the CLI-only logout business logic
// promoted from pkg/cli/cmd_auth.go. Covers the four resolution paths:
// empty store, explicit hub (found + missing), auto-resolve (single +
// multiple), and pins the three byte-exact Formatted strings that both
// this method and the CLI shell emit.

import (
	"context"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

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
