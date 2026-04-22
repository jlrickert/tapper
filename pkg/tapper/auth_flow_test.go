package tapper_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// newMockHub wires an httptest.Server that plays the role of a PKCE
// hub:
//
//   - GET /oauth/authorize captures the code_challenge sent by the
//     flow (so the token endpoint can verify the returned verifier)
//     and 302-redirects back to the loopback redirect_uri with a
//     synthetic code and the echoed state.
//   - POST /oauth/token validates grant_type, recomputes the S256
//     challenge from the posted code_verifier, and returns a JSON
//     bearer token.
//
// We keep the captured challenge in a pointer so both handlers share
// the same state without globals.
// registerHubMetadata advertises RFC 8414 authorization server
// metadata on the mock hub. Endpoints are written relative to the
// incoming request's Host so the values are correct regardless of
// which loopback alias (127.0.0.1 vs localhost) the test client hit.
func registerHubMetadata(mux *http.ServeMux) {
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint":           base + "/oauth/authorize",
			"token_endpoint":                   base + "/oauth/token",
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
}

func newMockHub(t *testing.T, accessToken string, expiresIn int64) (*httptest.Server, *atomic.Value /* string */) {
	t.Helper()
	var capturedChallenge atomic.Value // string
	mux := http.NewServeMux()
	registerHubMetadata(mux)

	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		capturedChallenge.Store(q.Get("code_challenge"))
		redir := q.Get("redirect_uri")
		state := q.Get("state")
		require.NotEmpty(t, redir, "authorize received empty redirect_uri")
		require.Equal(t, "S256", q.Get("code_challenge_method"))
		u, err := url.Parse(redir)
		require.NoError(t, err)
		rq := u.Query()
		rq.Set("code", "mock-auth-code")
		rq.Set("state", state)
		u.RawQuery = rq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})

	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "authorization_code", r.PostFormValue("grant_type"))
		require.Equal(t, "mock-auth-code", r.PostFormValue("code"))

		verifier := r.PostFormValue("code_verifier")
		require.NotEmpty(t, verifier, "token exchange missing code_verifier")
		expectChallenge, _ := capturedChallenge.Load().(string)
		require.Equal(t, expectChallenge, tapper.PKCEChallenge(verifier),
			"posted code_verifier does not match the challenge sent to /authorize")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": accessToken,
			"token_type":   "Bearer",
			"expires_in":   expiresIn,
			"scope":        "read write",
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &capturedChallenge
}

// driveAuthorize returns a BrowserOpener that follows the /authorize
// redirect back to the loopback listener. We use a non-redirect-following
// client so the test itself can observe the 302 hop if needed, but here
// we simply issue a GET with redirects enabled — Go's default client
// follows 3xx responses and will hit /callback on the loopback port.
func driveAuthorize() func(ctx context.Context, rt *toolkit.Runtime, u string) error {
	return func(ctx context.Context, _ *toolkit.Runtime, u string) error {
		// Run in the background so AuthLogin can proceed to Accept
		// before the redirect round-trip lands on the loopback port.
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

func TestAuthLogin_HappyPath(t *testing.T) {
	t.Parallel()
	hub, _ := newMockHub(t, "secret-access-token", 3600)
	fx := NewSandbox(t)

	entry, err := tapper.AuthLogin(fx.Context(), fx.Runtime(), tapper.AuthLoginOptions{
		HubURL:   hub.URL,
		ClientID: "tapper-cli",
		Scope:    "read write",
		Timeout:  10 * time.Second,
		ListenerFactory: func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		},
		BrowserOpener: driveAuthorize(),
	})
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.Equal(t, "secret-access-token", entry.AccessToken)
	require.Equal(t, "Bearer", entry.TokenType)
	require.Equal(t, "read write", entry.Scope)
	require.False(t, entry.ExpiresAt.IsZero(), "expires_in > 0 should set ExpiresAt")
}

func TestAuthLogin_NoExpiresIn_LeavesExpiresAtZero(t *testing.T) {
	t.Parallel()
	hub, _ := newMockHub(t, "tok", 0)
	fx := NewSandbox(t)

	entry, err := tapper.AuthLogin(fx.Context(), fx.Runtime(), tapper.AuthLoginOptions{
		HubURL:   hub.URL,
		ClientID: "tapper-cli",
		Timeout:  10 * time.Second,
		ListenerFactory: func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		},
		BrowserOpener: driveAuthorize(),
	})
	require.NoError(t, err)
	require.True(t, entry.ExpiresAt.IsZero(), "missing/zero expires_in should produce zero-value ExpiresAt")
}

func TestAuthLogin_MissingHubURL_Errors(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	_, err := tapper.AuthLogin(fx.Context(), fx.Runtime(), tapper.AuthLoginOptions{
		ClientID: "tapper-cli",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "hub URL")
}

func TestAuthLogin_MissingClientID_Errors(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	_, err := tapper.AuthLogin(fx.Context(), fx.Runtime(), tapper.AuthLoginOptions{
		HubURL: "https://hub.example.com",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "client ID")
}

func TestAuthLogin_InvalidScheme_Errors(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	_, err := tapper.AuthLogin(fx.Context(), fx.Runtime(), tapper.AuthLoginOptions{
		HubURL:   "ftp://hub.example.com",
		ClientID: "tapper-cli",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "http or https")
}

// TestAuthLogin_StateMismatch exercises the CSRF guard by having the
// mock hub tamper with the echoed state before redirecting. The flow
// must reject the callback and surface a state-mismatch error.
func TestAuthLogin_StateMismatch(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	registerHubMetadata(mux)
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		redir := q.Get("redirect_uri")
		u, err := url.Parse(redir)
		require.NoError(t, err)
		rq := u.Query()
		rq.Set("code", "c")
		rq.Set("state", "tampered-state")
		u.RawQuery = rq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	fx := NewSandbox(t)
	_, err := tapper.AuthLogin(fx.Context(), fx.Runtime(), tapper.AuthLoginOptions{
		HubURL:   srv.URL,
		ClientID: "tapper-cli",
		Timeout:  5 * time.Second,
		ListenerFactory: func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		},
		BrowserOpener: driveAuthorize(),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "state mismatch")
}

// TestAuthLogin_HubReturnsError surfaces a hub-side error= param as an
// auth error rather than swallowing it into a "missing code" message.
func TestAuthLogin_HubReturnsError(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	registerHubMetadata(mux)
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		u, err := url.Parse(q.Get("redirect_uri"))
		require.NoError(t, err)
		rq := u.Query()
		rq.Set("error", "access_denied")
		rq.Set("error_description", "user clicked no")
		rq.Set("state", q.Get("state"))
		u.RawQuery = rq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	fx := NewSandbox(t)
	_, err := tapper.AuthLogin(fx.Context(), fx.Runtime(), tapper.AuthLoginOptions{
		HubURL:   srv.URL,
		ClientID: "tapper-cli",
		Timeout:  5 * time.Second,
		ListenerFactory: func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		},
		BrowserOpener: driveAuthorize(),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "access_denied")
}

// TestAuthLogin_TokenEndpoint500 asserts that a token-exchange failure
// is propagated verbatim (status + body) so CLI users can debug hub
// misconfigurations without enabling debug logging.
func TestAuthLogin_TokenEndpoint500(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	registerHubMetadata(mux)
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		u, _ := url.Parse(q.Get("redirect_uri"))
		rq := u.Query()
		rq.Set("code", "c")
		rq.Set("state", q.Get("state"))
		u.RawQuery = rq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "kaboom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	fx := NewSandbox(t)
	_, err := tapper.AuthLogin(fx.Context(), fx.Runtime(), tapper.AuthLoginOptions{
		HubURL:   srv.URL,
		ClientID: "tapper-cli",
		Timeout:  5 * time.Second,
		ListenerFactory: func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		},
		BrowserOpener: driveAuthorize(),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
	require.Contains(t, err.Error(), "kaboom")
}

// TestAuthLogin_Timeout fires when the browser never opens; the flow
// should surface a timeout error rather than hanging. We stand up a
// hub that serves metadata correctly so discovery succeeds, then the
// no-op BrowserOpener ensures no callback is ever driven — forcing the
// select-arm timeout to fire.
func TestAuthLogin_Timeout(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	registerHubMetadata(mux)
	// No /oauth/authorize handler — the BrowserOpener is a no-op below,
	// so the authorize endpoint is never hit. The callback listener sits
	// idle until the flow's timeout elapses.
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	fx := NewSandbox(t)
	_, err := tapper.AuthLogin(fx.Context(), fx.Runtime(), tapper.AuthLoginOptions{
		HubURL:   srv.URL,
		ClientID: "tapper-cli",
		Timeout:  100 * time.Millisecond,
		ListenerFactory: func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		},
		BrowserOpener: func(ctx context.Context, rt *toolkit.Runtime, _ string) error {
			// Intentionally do nothing: no callback will ever land.
			return nil
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "timed out")
}

// TestAuthLogin_ContextCanceled exercises the ctx.Done() branch of the
// select — a caller cancellation must abort the flow without waiting
// for the full Timeout to elapse. The mock hub advertises metadata so
// discovery succeeds, then the no-op BrowserOpener parks the flow on
// the loopback listener until ctx is canceled.
func TestAuthLogin_ContextCanceled(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	registerHubMetadata(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	fx := NewSandbox(t)
	ctx, cancel := context.WithCancel(fx.Context())

	done := make(chan error, 1)
	go func() {
		_, err := tapper.AuthLogin(ctx, fx.Runtime(), tapper.AuthLoginOptions{
			HubURL:   srv.URL,
			ClientID: "tapper-cli",
			Timeout:  10 * time.Second,
			ListenerFactory: func() (net.Listener, error) {
				return net.Listen("tcp", "127.0.0.1:0")
			},
			BrowserOpener: func(ctx context.Context, rt *toolkit.Runtime, _ string) error {
				return nil
			},
		})
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("AuthLogin did not return after context cancellation")
	}
}

func TestCanonicalHubURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"https://Hub.Example.COM/", "https://hub.example.com"},
		{"HTTP://HUB.example.com/path/", "http://hub.example.com/path"},
		{"https://hub.example.com", "https://hub.example.com"},
		{"  https://hub.example.com/  ", "https://hub.example.com"},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, tapper.CanonicalHubURL(tc.in), "in=%q", tc.in)
	}
}

// TestAuthLogin_NoClock_UsesRuntimeClock pins the expiry arithmetic to
// rt.Clock().Now() — not time.Now(). We check the ExpiresAt is within a
// small window of the runtime clock's reading plus expires_in.
func TestAuthLogin_UsesRuntimeClockForExpiry(t *testing.T) {
	t.Parallel()
	hub, _ := newMockHub(t, "tok", 7200)
	fx := NewSandbox(t)

	before := fx.Runtime().Clock().Now()
	entry, err := tapper.AuthLogin(fx.Context(), fx.Runtime(), tapper.AuthLoginOptions{
		HubURL:   hub.URL,
		ClientID: "tapper-cli",
		Timeout:  10 * time.Second,
		ListenerFactory: func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		},
		BrowserOpener: driveAuthorize(),
	})
	require.NoError(t, err)
	after := fx.Runtime().Clock().Now()

	minExp := before.Add(7200 * time.Second)
	maxExp := after.Add(7200 * time.Second)
	require.False(t, entry.ExpiresAt.Before(minExp),
		"ExpiresAt %s predates clock+expires_in %s", entry.ExpiresAt, minExp)
	require.False(t, entry.ExpiresAt.After(maxExp),
		"ExpiresAt %s postdates clock+expires_in %s", entry.ExpiresAt, maxExp)
}

// TestAuthLogin_CustomHTTPClient asserts that a caller-supplied client
// is actually used for the token exchange (guards the "default only"
// regression).
func TestAuthLogin_CustomHTTPClient(t *testing.T) {
	t.Parallel()
	hub, _ := newMockHub(t, "tok", 60)
	fx := NewSandbox(t)

	var hits int32
	rt := &countingRoundTripper{next: http.DefaultTransport, hits: &hits}
	client := &http.Client{Transport: rt}

	_, err := tapper.AuthLogin(fx.Context(), fx.Runtime(), tapper.AuthLoginOptions{
		HubURL:   hub.URL,
		ClientID: "tapper-cli",
		Timeout:  10 * time.Second,
		ListenerFactory: func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		},
		BrowserOpener: driveAuthorize(),
		HTTPClient:    client,
	})
	require.NoError(t, err)
	require.Greater(t, atomic.LoadInt32(&hits), int32(0),
		"custom HTTP client should have handled the token exchange")
}

type countingRoundTripper struct {
	next http.RoundTripper
	hits *int32
}

func (c *countingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	atomic.AddInt32(c.hits, 1)
	return c.next.RoundTrip(req)
}

// TestAuthLogin_CustomRandReader_Deterministic seeds the entropy source
// with a long repeatable byte sequence and confirms the flow still
// completes — it's a sanity check that the reader plumbing doesn't
// short-read or panic on a non-crypto reader.
func TestAuthLogin_CustomRandReader(t *testing.T) {
	t.Parallel()
	hub, _ := newMockHub(t, "tok", 60)
	fx := NewSandbox(t)

	// 1 KiB of seeded bytes — enough for both verifier and state reads.
	seed := bytes.Repeat([]byte("abcdefghijklmnop"), 64)

	_, err := tapper.AuthLogin(fx.Context(), fx.Runtime(), tapper.AuthLoginOptions{
		HubURL:   hub.URL,
		ClientID: "tapper-cli",
		Timeout:  10 * time.Second,
		ListenerFactory: func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		},
		BrowserOpener: driveAuthorize(),
		RandReader:    bytes.NewReader(seed),
	})
	require.NoError(t, err)
}

// TestBuildAuthorizeURLIncludesAllParams is an indirect test: we drive
// the flow against a minimal /authorize handler that captures the
// entire query string and then assert every PKCE parameter is present.
// Safer than poking into buildAuthorizeURL directly because the public
// surface is AuthLogin — if we refactor the helper signature, this
// test still passes as long as the on-the-wire contract is intact.
func TestAuthLogin_AuthorizeURLContainsPKCEParams(t *testing.T) {
	t.Parallel()

	var gotQuery atomic.Value // url.Values
	mux := http.NewServeMux()
	registerHubMetadata(mux)
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.Query())
		q := r.URL.Query()
		u, _ := url.Parse(q.Get("redirect_uri"))
		rq := u.Query()
		rq.Set("code", "c")
		rq.Set("state", q.Get("state"))
		u.RawQuery = rq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"t","token_type":"Bearer"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	fx := NewSandbox(t)
	_, err := tapper.AuthLogin(fx.Context(), fx.Runtime(), tapper.AuthLoginOptions{
		HubURL:   srv.URL,
		ClientID: "my-client",
		Scope:    "read write",
		Timeout:  10 * time.Second,
		ListenerFactory: func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		},
		BrowserOpener: driveAuthorize(),
	})
	require.NoError(t, err)

	q, _ := gotQuery.Load().(url.Values)
	require.NotNil(t, q)
	require.Equal(t, "code", q.Get("response_type"))
	require.Equal(t, "my-client", q.Get("client_id"))
	require.True(t, strings.HasPrefix(q.Get("redirect_uri"), "http://127.0.0.1:"),
		"redirect_uri must bind to loopback, got %q", q.Get("redirect_uri"))
	require.NotEmpty(t, q.Get("code_challenge"))
	require.Equal(t, "S256", q.Get("code_challenge_method"))
	require.NotEmpty(t, q.Get("state"))
	require.Equal(t, "read write", q.Get("scope"))
}

// TestAuthLogin_ScopeOmittedWhenEmpty confirms we don't send an empty
// scope= parameter — some hubs treat "scope=" (present, empty) as a
// validation error distinct from an absent scope.
func TestAuthLogin_ScopeOmittedWhenEmpty(t *testing.T) {
	t.Parallel()

	var gotQuery atomic.Value
	mux := http.NewServeMux()
	registerHubMetadata(mux)
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.Query())
		q := r.URL.Query()
		u, _ := url.Parse(q.Get("redirect_uri"))
		rq := u.Query()
		rq.Set("code", "c")
		rq.Set("state", q.Get("state"))
		u.RawQuery = rq.Encode()
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"t","token_type":"Bearer"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	fx := NewSandbox(t)
	_, err := tapper.AuthLogin(fx.Context(), fx.Runtime(), tapper.AuthLoginOptions{
		HubURL:   srv.URL,
		ClientID: "my-client",
		Timeout:  10 * time.Second,
		ListenerFactory: func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		},
		BrowserOpener: driveAuthorize(),
	})
	require.NoError(t, err)

	q, _ := gotQuery.Load().(url.Values)
	_, present := q["scope"]
	require.False(t, present, "scope key should be absent from query when empty")
}

// Sanity check: the package builds cleanly — catches an import we
// might otherwise only notice via `go test`. Kept trivial so it
// contributes no runtime.
func TestAuthFlow_Smoke(t *testing.T) {
	t.Parallel()
	_ = fmt.Sprintf // reserved for future debug prints
	_ = (*tapper.AuthEntry)(nil)
}
