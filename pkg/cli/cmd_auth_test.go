package cli

// Integration-ish tests for `tap auth login`. We exercise the wiring
// (flag parsing, hub requirement, store persistence, stdout discipline)
// but stub the PKCE handshake through the authLoginFn seam. The full
// browser/PKCE round trip is tested in pkg/tapper/auth_flow_test.go
// against an httptest hub — duplicating that here would only couple
// the CLI test to the transport.

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// withStubAuthLogin swaps authLoginFn for the duration of a test. We
// restore the original in a t.Cleanup so parallel tests that don't
// touch the seam are unaffected by leaked state if this helper ever
// returns early on failure.
func withStubAuthLogin(t *testing.T, fn func(context.Context, *toolkit.Runtime, tapper.AuthLoginOptions) (*tapper.AuthEntry, error)) {
	t.Helper()
	prev := authLoginFn
	authLoginFn = fn
	t.Cleanup(func() { authLoginFn = prev })
}

// newStubbedAuthProcess builds a Process running `tap auth login ...`.
// It's a thin wrapper around the production Run so we go through the
// same Cobra wiring real users hit.
func newStubbedAuthProcess(t *testing.T, isTTY bool, args ...string) *sandbox.Process {
	t.Helper()
	return sandbox.NewProcess(func(ctx context.Context, rt *toolkit.Runtime) (int, error) {
		return Run(ctx, rt, args)
	}, isTTY)
}

func newTestSandbox(t *testing.T) *sandbox.Sandbox {
	t.Helper()
	return sandbox.NewSandbox(t, &sandbox.Options{
		Home: "/home/testuser",
		User: "testuser",
	})
}

func TestAuthLoginCmd_MissingHub_Errors(t *testing.T) {
	// NOTE: no t.Parallel — this test suite mutates the package-level
	// authLoginFn seam, so concurrent test runs would race. The suite
	// is small enough that serial execution is not a bottleneck.
	withStubAuthLogin(t, func(context.Context, *toolkit.Runtime, tapper.AuthLoginOptions) (*tapper.AuthEntry, error) {
		t.Fatal("authLoginFn should not be called when --hub is missing")
		return nil, nil
	})

	sb := newTestSandbox(t)
	proc := newStubbedAuthProcess(t, false, "auth", "login")
	res := proc.Run(sb.Context(), sb.Runtime())

	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "--hub")
}

func TestAuthLoginCmd_PersistsStoreAndPrintsHub(t *testing.T) {
	// NOTE: no t.Parallel — this test suite mutates the package-level
	// authLoginFn seam, so concurrent test runs would race. The suite
	// is small enough that serial execution is not a bottleneck.
	sb := newTestSandbox(t)

	// Capture the options AuthLogin receives so we can assert the CLI
	// passed flag values through unchanged.
	var captured tapper.AuthLoginOptions
	withStubAuthLogin(t, func(_ context.Context, _ *toolkit.Runtime, opts tapper.AuthLoginOptions) (*tapper.AuthEntry, error) {
		captured = opts
		return &tapper.AuthEntry{
			AccessToken: "stub-access-token",
			TokenType:   "Bearer",
			Scope:       "read write",
		}, nil
	})

	proc := newStubbedAuthProcess(t, false,
		"auth", "login",
		"--hub", "https://Hub.Example.COM/",
		"--client-id", "tapper-cli",
		"--scope", "read write",
	)
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// Success line: mentions the hub (as the user typed it) and does
	// NOT leak the access token.
	out := string(res.Stdout)
	require.Contains(t, out, "Logged in to https://Hub.Example.COM/")
	require.NotContains(t, out, "stub-access-token")

	// Options passed through correctly.
	require.Equal(t, "https://Hub.Example.COM/", captured.HubURL)
	require.Equal(t, "tapper-cli", captured.ClientID)
	require.Equal(t, "read write", captured.Scope)

	// Store persisted at StateRoot/auth.yaml, keyed by the canonical
	// form of the hub URL. Use sandbox's home-relative read to avoid
	// hardcoding the XDG path.
	raw := sb.MustReadFile("~/.local/state/tapper/auth.yaml")
	var parsed struct {
		Hubs map[string]struct {
			AccessToken string `yaml:"access_token"`
			TokenType   string `yaml:"token_type"`
			Scope       string `yaml:"scope"`
		} `yaml:"hubs"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &parsed))

	entry, ok := parsed.Hubs["https://hub.example.com"]
	require.True(t, ok, "canonical hub URL key missing; got keys: %v", keysOf(parsed.Hubs))
	require.Equal(t, "stub-access-token", entry.AccessToken)
	require.Equal(t, "Bearer", entry.TokenType)
	require.Equal(t, "read write", entry.Scope)
}

// keysOf is a tiny helper so failure messages are readable when the
// store layout changes unexpectedly.
func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestAuthLoginCmd_DefaultClientID(t *testing.T) {
	// NOTE: no t.Parallel — this test suite mutates the package-level
	// authLoginFn seam, so concurrent test runs would race. The suite
	// is small enough that serial execution is not a bottleneck.
	sb := newTestSandbox(t)

	var captured tapper.AuthLoginOptions
	withStubAuthLogin(t, func(_ context.Context, _ *toolkit.Runtime, opts tapper.AuthLoginOptions) (*tapper.AuthEntry, error) {
		captured = opts
		return &tapper.AuthEntry{AccessToken: "t"}, nil
	})

	proc := newStubbedAuthProcess(t, false, "auth", "login", "--hub", "https://hub.example.com")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "tapper-cli", captured.ClientID, "client-id should default to tapper-cli")
}

func TestAuthLoginCmd_PropagatesFlowError(t *testing.T) {
	// NOTE: no t.Parallel — this test suite mutates the package-level
	// authLoginFn seam, so concurrent test runs would race. The suite
	// is small enough that serial execution is not a bottleneck.
	sb := newTestSandbox(t)

	withStubAuthLogin(t, func(context.Context, *toolkit.Runtime, tapper.AuthLoginOptions) (*tapper.AuthEntry, error) {
		return nil, &stubAuthError{msg: "simulated browser failure"}
	})

	proc := newStubbedAuthProcess(t, false, "auth", "login", "--hub", "https://hub.example.com")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "simulated browser failure")
	// No store file should have been created.
	_, statErr := sb.Runtime().Stat("/home/testuser/.local/state/tapper/auth.yaml", false)
	require.Error(t, statErr, "store file should not exist when the flow errors")
}

type stubAuthError struct{ msg string }

func (e *stubAuthError) Error() string { return e.msg }

// TestAuthLoginCmd_EndToEnd_AgainstMockHub runs the real tapper.AuthLogin
// implementation (no seam override) against a local httptest hub, with
// a browser-opener that drives /authorize by HTTP. This is the only
// CLI test that exercises the full PKCE + loopback path end-to-end.
func TestAuthLoginCmd_EndToEnd_AgainstMockHub(t *testing.T) {
	// NOTE: no t.Parallel — this test suite mutates the package-level
	// authLoginFn seam, so concurrent test runs would race. The suite
	// is small enough that serial execution is not a bottleneck.

	hub := newMockHubForCLI(t)
	defer hub.Close()

	// The CLI command has no way to inject BrowserOpener from flags,
	// so we route through the seam: call the real AuthLogin but
	// pre-inject the BrowserOpener that drives the authorize endpoint.
	withStubAuthLogin(t, func(ctx context.Context, rt *toolkit.Runtime, opts tapper.AuthLoginOptions) (*tapper.AuthEntry, error) {
		opts.BrowserOpener = driveAuthorizeForCLI()
		opts.ListenerFactory = func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		}
		if opts.Timeout == 0 {
			opts.Timeout = 10 * time.Second
		}
		return tapper.AuthLogin(ctx, rt, opts)
	})

	sb := newTestSandbox(t)
	proc := newStubbedAuthProcess(t, false,
		"auth", "login",
		"--hub", hub.URL,
		"--client-id", "tapper-cli",
	)
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	raw := sb.MustReadFile("~/.local/state/tapper/auth.yaml")
	require.Contains(t, string(raw), "e2e-access-token")
	// The raw file must never contain our verifier/state — those are
	// purely in-memory and must not leak through the store.
	require.NotContains(t, string(raw), "code_verifier")
}

// newMockHubForCLI is a copy of the pkg/tapper mock hub, duplicated
// here so the cli test is self-contained (the other is in _test.go of
// a different package and can't be imported).
func newMockHubForCLI(t *testing.T) *httptest.Server {
	t.Helper()
	// Cross-handler shared state: /oauth/authorize captures the challenge,
	// /oauth/token validates the posted verifier against it. httptest runs
	// each handler in its own goroutine, so the shared slot must be
	// synchronized; atomic.Value mirrors the pattern used in the
	// pkg/tapper auth flow tests.
	var challenge atomic.Value // string
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint":           base + "/oauth/authorize",
			"token_endpoint":                   base + "/oauth/token",
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		challenge.Store(q.Get("code_challenge"))
		u, _ := url.Parse(q.Get("redirect_uri"))
		rq := u.Query()
		rq.Set("code", "e2e-code")
		rq.Set("state", q.Get("state"))
		u.RawQuery = rq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotVerifier := r.PostFormValue("code_verifier")
		stored, _ := challenge.Load().(string)
		require.Equal(t, stored, tapper.PKCEChallenge(gotVerifier),
			"posted verifier does not match the challenge captured at /authorize")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "e2e-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	return httptest.NewServer(mux)
}

func driveAuthorizeForCLI() func(ctx context.Context, rt *toolkit.Runtime, u string) error {
	return func(ctx context.Context, _ *toolkit.Runtime, u string) error {
		go func() {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err != nil {
				return
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}()
		return nil
	}
}

// TestAuthLoginCmd_CanonicalizesHubKey confirms the store key is the
// canonical form even when the user types a mixed-case URL.
func TestAuthLoginCmd_CanonicalizesHubKey(t *testing.T) {
	// NOTE: no t.Parallel — this test suite mutates the package-level
	// authLoginFn seam, so concurrent test runs would race. The suite
	// is small enough that serial execution is not a bottleneck.
	sb := newTestSandbox(t)

	withStubAuthLogin(t, func(context.Context, *toolkit.Runtime, tapper.AuthLoginOptions) (*tapper.AuthEntry, error) {
		return &tapper.AuthEntry{AccessToken: "t"}, nil
	})

	proc := newStubbedAuthProcess(t, false, "auth", "login", "--hub", "HTTPS://HUB.EXAMPLE.COM/")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	raw := sb.MustReadFile("~/.local/state/tapper/auth.yaml")
	require.Contains(t, string(raw), "https://hub.example.com")
	require.NotContains(t, strings.ToLower(string(raw)),
		"https://hub.example.com/")
}

// TestAuthCmd_NoArgs_ShowsHelp guards the parent's cobra wiring: calling
// `tap auth` with no child should not error and should print help.
func TestAuthCmd_NoArgs_ShowsHelp(t *testing.T) {
	// NOTE: no t.Parallel — this test suite mutates the package-level
	// authLoginFn seam, so concurrent test runs would race. The suite
	// is small enough that serial execution is not a bottleneck.
	sb := newTestSandbox(t)
	proc := newStubbedAuthProcess(t, false, "auth")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	out := string(res.Stdout) + string(res.Stderr)
	require.Contains(t, out, "login")
}
