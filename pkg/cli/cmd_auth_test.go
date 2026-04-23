package cli

// Integration-ish tests for `tap auth login/logout/status`. Each test
// exercises the Cobra wiring end-to-end and injects a per-test
// AuthLoginFn through the WithTestDepsHook seam in cli.go. Because the
// hook is stashed on each invocation's context (not on a package-level
// var), every test can run with t.Parallel() — closures capture their
// own stub and there is no shared mutable state for the race detector
// to flag.

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

// stubAuthLoginHook returns a Deps hook that installs fn as AuthLoginFn
// and otherwise leaves Deps untouched. Attach via WithTestDepsHook so
// parallel tests each stash their own hook on their own context without
// touching any shared mutable state.
func stubAuthLoginHook(fn func(context.Context, *toolkit.Runtime, tapper.AuthLoginOptions) (*tapper.AuthEntry, error)) func(*Deps) {
	return func(d *Deps) {
		d.AuthLoginFn = fn
	}
}

// newAuthProcess builds a Process running `tap auth ...`. A nil hook
// runs the unaltered production wiring; otherwise the hook is attached
// to the ctx via WithTestDepsHook and applied to Deps before NewRootCmd
// runs.
func newAuthProcess(t *testing.T, hook func(*Deps), args ...string) *sandbox.Process {
	t.Helper()
	return sandbox.NewProcess(func(ctx context.Context, rt *toolkit.Runtime) (int, error) {
		if hook != nil {
			ctx = WithTestDepsHook(ctx, hook)
		}
		return Run(ctx, rt, args)
	}, false)
}

func newTestSandbox(t *testing.T) *sandbox.Sandbox {
	t.Helper()
	return sandbox.NewSandbox(t, &sandbox.Options{
		Home: "/home/testuser",
		User: "testuser",
	})
}

// atomicOptionsSlot is a tiny helper that lets parallel tests capture
// the AuthLoginOptions their stub saw without a sync.Mutex. Using
// atomic.Value keeps the race detector happy; a plain variable would
// race between the Cobra goroutine and the test goroutine.
type atomicOptionsSlot struct{ v atomic.Value }

func (s *atomicOptionsSlot) Store(opts tapper.AuthLoginOptions) { s.v.Store(opts) }
func (s *atomicOptionsSlot) Load() tapper.AuthLoginOptions {
	v, ok := s.v.Load().(tapper.AuthLoginOptions)
	if !ok {
		return tapper.AuthLoginOptions{}
	}
	return v
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

func TestAuthLoginCmd_MissingHub_Errors(t *testing.T) {
	t.Parallel()
	hook := stubAuthLoginHook(func(context.Context, *toolkit.Runtime, tapper.AuthLoginOptions) (*tapper.AuthEntry, error) {
		t.Fatal("authLoginFn should not be called when --hub is missing")
		return nil, nil
	})

	sb := newTestSandbox(t)
	proc := newAuthProcess(t, hook, "auth", "login")
	res := proc.Run(sb.Context(), sb.Runtime())

	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "--hub")
}

func TestAuthLoginCmd_PersistsStoreAndPrintsHub(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	var capturedMu atomicOptionsSlot
	hook := stubAuthLoginHook(func(_ context.Context, _ *toolkit.Runtime, opts tapper.AuthLoginOptions) (*tapper.AuthEntry, error) {
		capturedMu.Store(opts)
		return &tapper.AuthEntry{
			AccessToken: "stub-access-token",
			TokenType:   "Bearer",
			Scope:       "read write",
		}, nil
	})

	proc := newAuthProcess(t, hook,
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

	captured := capturedMu.Load()
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

func TestAuthLoginCmd_DefaultClientID(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	var capturedMu atomicOptionsSlot
	hook := stubAuthLoginHook(func(_ context.Context, _ *toolkit.Runtime, opts tapper.AuthLoginOptions) (*tapper.AuthEntry, error) {
		capturedMu.Store(opts)
		return &tapper.AuthEntry{AccessToken: "t"}, nil
	})

	proc := newAuthProcess(t, hook, "auth", "login", "--hub", "https://hub.example.com")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "tapper-cli", capturedMu.Load().ClientID, "client-id should default to tapper-cli")
}

func TestAuthLoginCmd_PropagatesFlowError(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	hook := stubAuthLoginHook(func(context.Context, *toolkit.Runtime, tapper.AuthLoginOptions) (*tapper.AuthEntry, error) {
		return nil, &stubAuthError{msg: "simulated browser failure"}
	})

	proc := newAuthProcess(t, hook, "auth", "login", "--hub", "https://hub.example.com")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "simulated browser failure")
	_, statErr := sb.Runtime().Stat("/home/testuser/.local/state/tapper/auth.yaml", false)
	require.Error(t, statErr, "store file should not exist when the flow errors")
}

type stubAuthError struct{ msg string }

func (e *stubAuthError) Error() string { return e.msg }

// TestAuthLoginCmd_EndToEnd_AgainstMockHub runs the real tapper.AuthLogin
// implementation against a local httptest hub with a browser-opener
// that drives /authorize by HTTP. Only CLI test that exercises the full
// PKCE + loopback path end-to-end.
func TestAuthLoginCmd_EndToEnd_AgainstMockHub(t *testing.T) {
	t.Parallel()
	hub := newMockHubForCLI(t)
	defer hub.Close()

	// The CLI command has no way to inject BrowserOpener from flags,
	// so we route through the seam: call the real AuthLogin but pre-
	// inject the BrowserOpener that drives the authorize endpoint.
	hook := stubAuthLoginHook(func(ctx context.Context, rt *toolkit.Runtime, opts tapper.AuthLoginOptions) (*tapper.AuthEntry, error) {
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
	proc := newAuthProcess(t, hook,
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
	// synchronized; atomic.Value mirrors the pattern used in pkg/tapper's
	// auth flow tests.
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
	t.Parallel()
	sb := newTestSandbox(t)

	hook := stubAuthLoginHook(func(context.Context, *toolkit.Runtime, tapper.AuthLoginOptions) (*tapper.AuthEntry, error) {
		return &tapper.AuthEntry{AccessToken: "t"}, nil
	})

	proc := newAuthProcess(t, hook, "auth", "login", "--hub", "HTTPS://HUB.EXAMPLE.COM/")
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
	t.Parallel()
	sb := newTestSandbox(t)
	proc := newAuthProcess(t, nil, "auth")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	out := string(res.Stdout) + string(res.Stderr)
	require.Contains(t, out, "login")
	// Help text must advertise logout and status alongside login.
	require.Contains(t, out, "logout")
	require.Contains(t, out, "status")
}

// --- logout ---

// seedAuthStore writes an auth.yaml fixture into the sandbox's state
// root directly — it's the fastest way to arrange a pre-existing login
// without running the full PKCE flow. Returns the first canonical key
// written (handy when a caller seeds just one entry).
func seedAuthStore(t *testing.T, sb *sandbox.Sandbox, entries map[string]tapper.AuthEntry) string {
	t.Helper()
	rt := sb.Runtime()
	ctx := sb.Context()

	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: rt})
	require.NoError(t, err)
	storePath := tap.PathService.AuthStorePath()

	store, err := tapper.LoadAuthStore(ctx, rt, storePath)
	require.NoError(t, err)
	var firstKey string
	for k, v := range entries {
		canon := tapper.CanonicalHubURL(k)
		if firstKey == "" {
			firstKey = canon
		}
		store.Set(canon, v)
	}
	require.NoError(t, store.Save(ctx, rt, storePath))
	return firstKey
}

func TestAuthLogoutCmd_SingleHub_AutoResolves(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	hub := seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		"https://hub.example.com": {AccessToken: "xyz", TokenType: "Bearer"},
	})

	proc := newAuthProcess(t, nil, "auth", "logout")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "Logged out of "+hub)

	// Empty store → the file should be removed (AuthStore.Save contract).
	_, statErr := sb.Runtime().Stat("/home/testuser/.local/state/tapper/auth.yaml", false)
	require.Error(t, statErr, "auth store file should be removed when empty")
}

func TestAuthLogoutCmd_MultipleHubs_RequiresFlag(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		"https://hub-a.example.com": {AccessToken: "a"},
		"https://hub-b.example.com": {AccessToken: "b"},
	})

	proc := newAuthProcess(t, nil, "auth", "logout")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "--hub is required")
}

func TestAuthLogoutCmd_ExplicitHub_Canonicalizes(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		"https://hub.example.com": {AccessToken: "xyz"},
	})

	proc := newAuthProcess(t, nil, "auth", "logout", "--hub", "HTTPS://Hub.Example.COM/")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "Logged out of https://hub.example.com")
}

func TestAuthLogoutCmd_UnknownHub_SoftSuccess(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		"https://hub.example.com": {AccessToken: "xyz"},
	})

	proc := newAuthProcess(t, nil, "auth", "logout", "--hub", "https://ghost.example.com")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "unknown hub is a soft success, not an error")
	require.Contains(t, string(res.Stderr), "No login stored for https://ghost.example.com")
	raw := sb.MustReadFile("~/.local/state/tapper/auth.yaml")
	require.Contains(t, string(raw), "https://hub.example.com")
}

func TestAuthLogoutCmd_EmptyStore_NoOp(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	proc := newAuthProcess(t, nil, "auth", "logout")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stderr), "No hub logins stored.")
}

// --- status ---

func TestAuthStatusCmd_EmptyStore_DirectedMessage(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	proc := newAuthProcess(t, nil, "auth", "status")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "No hub logins stored")
	require.Contains(t, string(res.Stdout), "tap auth login --hub URL")
}

func TestAuthStatusCmd_SingleHub_FormatsStatus(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		"https://hub.example.com": {
			AccessToken: "supersecretXXYZ",
			TokenType:   "Bearer",
			Scope:       "read write",
		},
	})

	proc := newAuthProcess(t, nil, "auth", "status")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	out := string(res.Stdout)
	require.Contains(t, out, "Logged in to https://hub.example.com")
	require.Contains(t, out, "token: ...XXYZ (Bearer)")
	require.Contains(t, out, "scope: read write")
	require.Contains(t, out, "expires: unknown")
	require.NotContains(t, out, "supersecretXXYZ")
}

func TestAuthStatusCmd_ShortToken_UsesPlaceholder(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		"https://hub.example.com": {AccessToken: "abc"},
	})

	proc := newAuthProcess(t, nil, "auth", "status")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "token: [set]")
}

func TestAuthStatusCmd_MultipleHubs_RequiresFlag(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		"https://hub-a.example.com": {AccessToken: "a"},
		"https://hub-b.example.com": {AccessToken: "b"},
	})

	proc := newAuthProcess(t, nil, "auth", "status")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "--hub is required")
}

func TestAuthStatusCmd_UnknownHub_NotPresent(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		"https://hub.example.com": {AccessToken: "xyz"},
	})

	proc := newAuthProcess(t, nil, "auth", "status", "--hub", "https://ghost.example.com")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "No login stored for https://ghost.example.com")
}

// --- completion ---

// The completion tests live in the internal cli package because we need
// direct access to Run to exercise the full Cobra tree. Cobra's
// __complete handler prints suggestions to stdout followed by a `:N`
// directive line.

func runCompletionViaProcess(t *testing.T, words ...string) *sandbox.ProcessResult {
	t.Helper()
	sb := newTestSandbox(t)
	proc := sandbox.NewProcess(func(ctx context.Context, rt *toolkit.Runtime) (int, error) {
		return RunCompletion(ctx, rt, words)
	}, false)
	return proc.Run(sb.Context(), sb.Runtime())
}

func TestAuthCompletion_LogoutHubFlag_NoFileComp(t *testing.T) {
	t.Parallel()
	res := runCompletionViaProcess(t, "auth", "logout", "--hub", "")
	require.NoError(t, res.Err)
	// :4 == ShellCompDirectiveNoFileComp. Cobra prints the directive
	// on the last line as ":<bitmask>".
	require.Contains(t, string(res.Stdout), ":4")
}

func TestAuthCompletion_StatusHubFlag_NoFileComp(t *testing.T) {
	t.Parallel()
	res := runCompletionViaProcess(t, "auth", "status", "--hub", "")
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), ":4")
}

func TestAuthCompletion_ParentLists_LoginLogoutStatus(t *testing.T) {
	t.Parallel()
	res := runCompletionViaProcess(t, "auth", "")
	require.NoError(t, res.Err)
	out := string(res.Stdout)
	for _, sub := range []string{"login", "logout", "status"} {
		require.Contains(t, out, sub, "auth subcommand %q missing from completion output:\n%s", sub, out)
	}
}
