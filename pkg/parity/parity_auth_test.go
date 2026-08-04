package parity_test

// Rendering tests for the auth surface, which is CLI-only: `login` requires
// browser + loopback, `logout` is an intentional CLI-only local-state mutation,
// and `status` lost its MCP peer when auth_status gave way to auth_info.
//
// `status` validates the stored token against the hub's whoami probe, so these
// run against a shared httptest hub rather than stubbing the call. The seeding
// helper talks to AuthStore directly rather than going through `tap auth login`
// — a full PKCE handshake would require a far heavier hub per test case and add
// no coverage over pkg/tapper/auth_flow_test.go.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// seedAuthStoreForEnv writes an auth store into the parity sandbox so
// both CLI and MCP invocations see the same on-disk state. Returns the
// canonical key that was stored (handy when the test wants to assert
// against the output without re-deriving canonical form).
func seedAuthStoreForEnv(t *testing.T, env *parityEnv, hubURL string, entry tapper.AuthEntry) string {
	t.Helper()
	storePath := env.tap.PathService.AuthStorePath()
	store, err := tapper.LoadAuthStore(env.ctx, env.tap.Runtime, storePath)
	require.NoError(t, err)
	canon := tapper.CanonicalHubURL(hubURL)
	store.Set(canon, entry)
	require.NoError(t, store.Save(env.ctx, env.tap.Runtime, storePath))
	return canon
}

// startWhoamiHub stands up a hub that answers GET /api/v1/whoami with the
// given status. On 200 it returns the supplied identity; otherwise it returns
// the hub's standard unauthorized body. Both CLI and MCP validate live against
// the returned URL, so their formatted output must match byte-for-byte.
func startWhoamiHub(t *testing.T, status int, username, displayName string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/whoami" {
			http.NotFound(w, r)
			return
		}
		if status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"invalid or expired token","code":"UNAUTHORIZED"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user_id":1,"username":"` + username +
			`","display_name":"` + displayName + `","email":"","created_at":"2025-01-01T00:00:00Z"}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestAuthStatusRendering covers `tap auth status` output against a seeded
// store. This was a parity test until auth_status left MCP: the agent-facing
// replacement is auth_info, which is deliberately a different shape (structured
// and credential-free) rather than a byte-identical peer, so there is nothing
// left to compare against. The rendering assertions — above all that a raw
// access token never reaches stdout — still earn their keep on the CLI alone.
func TestAuthStatusRendering(t *testing.T) {
	t.Parallel()

	t.Run("single_hub_auto_resolves_and_validates", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)
		hub := startWhoamiHub(t, http.StatusOK, "alice", "Alice Liddell")
		seedAuthStoreForEnv(t, env, hub, tapper.AuthEntry{
			AccessToken: "thub_paritytoken9999",
			TokenType:   "Bearer",
		})

		cliOut, err := env.runCLI("auth", "status")
		require.NoError(t, err)

		require.Contains(t, cliOut, "Logged in as alice (Alice Liddell)")
		// Token rendered by its leading prefix (matches the hub UI), not suffix.
		require.Contains(t, cliOut, "- Token: thub_parityt... (Bearer)")
		// The raw access token must never leak.
		require.NotContains(t, cliOut, "thub_paritytoken9999")
	})

	t.Run("rejected_token_reported", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)
		hub := startWhoamiHub(t, http.StatusUnauthorized, "", "")
		seedAuthStoreForEnv(t, env, hub, tapper.AuthEntry{
			AccessToken: "thub_rejectedtoken00",
			TokenType:   "Bearer",
		})

		cliOut, err := env.runCLI("auth", "status")
		require.NoError(t, err)

		require.Contains(t, cliOut, "Failed to validate token")
		require.Contains(t, cliOut, "- Token: thub_rejecte... (Bearer)")
		require.NotContains(t, cliOut, "thub_rejectedtoken00")
	})

	t.Run("multiple_hubs_reported", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)
		hubA := startWhoamiHub(t, http.StatusOK, "alice", "")
		hubB := startWhoamiHub(t, http.StatusOK, "bob", "")
		seedAuthStoreForEnv(t, env, hubB, tapper.AuthEntry{
			AccessToken: "thub_paritybeta000",
			TokenType:   "Bearer",
		})
		seedAuthStoreForEnv(t, env, hubA, tapper.AuthEntry{
			AccessToken: "thub_parityalpha00",
			TokenType:   "Bearer",
		})

		cliOut, err := env.runCLI("auth", "status")
		require.NoError(t, err)

		require.Contains(t, cliOut, "Logged in as alice")
		require.Contains(t, cliOut, "Logged in as bob")
		require.Equal(t, 2, strings.Count(cliOut, "Logged in as "))
		require.Contains(t, cliOut, "\n\n")
	})

	t.Run("explicit_hub_canonicalizes", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)
		seedAuthStoreForEnv(t, env, "https://hub.example.com", tapper.AuthEntry{
			AccessToken: "abcd-5678-token",
			TokenType:   "Bearer",
		})

		// --offline keeps the test hermetic (no call to the unreachable
		// example.com) while still exercising hub-URL canonicalization.
		cliOut, err := env.runCLI("auth", "status", "--hub", "HTTPS://Hub.Example.COM/", "--offline")
		require.NoError(t, err)
		require.True(t, strings.Contains(cliOut, "hub.example.com"),
			"hub URL should be canonicalized in output; got:\n%s", cliOut)
	})

	t.Run("empty_store_emits_directed_message", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)
		cliOut, err := env.runCLI("auth", "status")
		require.NoError(t, err)
		require.Contains(t, cliOut, "No hub logins stored")
	})

	t.Run("unknown_hub_not_present", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)
		seedAuthStoreForEnv(t, env, "https://hub.example.com", tapper.AuthEntry{
			AccessToken: "zzzz",
		})
		cliOut, err := env.runCLI("auth", "status", "--hub", "https://ghost.example.com")
		require.NoError(t, err)
		require.Contains(t, cliOut, "No login stored for https://ghost.example.com")
	})
}
