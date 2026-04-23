package parity_test

// Parity tests for the auth surface. Only `status` has both surfaces;
// `login` requires browser + loopback (CLI-only) and `logout` is an
// intentional CLI-only local-state mutation.
//
// The seeding helper talks to AuthStore directly rather than going
// through `tap auth login` — a full PKCE handshake would require an
// httptest hub per test case and add no coverage over
// pkg/tapper/auth_flow_test.go.

import (
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

// TestParity_AuthStatus exercises both surfaces against the same seeded
// auth store. The Formatted field is authoritative, so we assert byte-
// equality (after stripping trailing whitespace, which the CLI runner
// already trims on its side via runCLI → strings.TrimSpace).
func TestParity_AuthStatus(t *testing.T) {
	t.Parallel()

	t.Run("single_hub_auto_resolves", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)
		seedAuthStoreForEnv(t, env, "https://hub.example.com", tapper.AuthEntry{
			AccessToken: "redact-me-1234",
			TokenType:   "Bearer",
			Scope:       "read write",
		})

		cliOut, err := env.runCLI("auth", "status")
		require.NoError(t, err)
		mcpOut, err := env.runMCP("auth_status", nil)
		require.NoError(t, err)

		require.Equal(t, cliOut, mcpOut,
			"CLI and MCP auth status must be byte-identical")
		require.Contains(t, cliOut, "Logged in to https://hub.example.com")
		require.Contains(t, cliOut, "token: ...1234 (Bearer)")
		// The raw access token must never leak through either surface.
		require.NotContains(t, cliOut, "redact-me-1234")
	})

	t.Run("explicit_hub_canonicalizes", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)
		seedAuthStoreForEnv(t, env, "https://hub.example.com", tapper.AuthEntry{
			AccessToken: "abcd-5678-token",
			TokenType:   "Bearer",
		})

		cliOut, err := env.runCLI("auth", "status", "--hub", "HTTPS://Hub.Example.COM/")
		require.NoError(t, err)
		mcpOut, err := env.runMCP("auth_status", map[string]any{
			"hub": "HTTPS://Hub.Example.COM/",
		})
		require.NoError(t, err)
		require.Equal(t, cliOut, mcpOut)
		require.True(t, strings.Contains(cliOut, "Logged in to https://hub.example.com"),
			"hub URL should be canonicalized in output; got:\n%s", cliOut)
	})

	t.Run("empty_store_emits_directed_message", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)
		cliOut, err := env.runCLI("auth", "status")
		require.NoError(t, err)
		mcpOut, err := env.runMCP("auth_status", nil)
		require.NoError(t, err)
		require.Equal(t, cliOut, mcpOut)
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
		mcpOut, err := env.runMCP("auth_status", map[string]any{
			"hub": "https://ghost.example.com",
		})
		require.NoError(t, err)
		require.Equal(t, cliOut, mcpOut)
		require.Contains(t, cliOut, "No login stored for https://ghost.example.com")
	})
}
