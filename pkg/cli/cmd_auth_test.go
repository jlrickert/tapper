package cli

// Integration-ish tests for `tap auth login/logout/status`. Each test
// exercises the Cobra wiring end-to-end and injects per-test seams through
// the WithTestDepsHook mechanism in cli.go. Because the hook is stashed on
// each invocation's context (not on a package-level var), every test can run
// with t.Parallel() — closures capture their own stub and there is no shared
// mutable state for the race detector to flag.
//
// Login standardizes on the RFC 8628 device flow for the browser path, so the
// stubs here target AuthLoginDeviceFn; the "paste a token" path is stubbed via
// AuthValidateTokenFn, and the interactive hub/method picker via a scripted
// fake AuthPrompter (so no real TTY is required).

import (
	"context"
	"fmt"
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

// stubDeviceLoginHook installs fn as AuthLoginDeviceFn — the browser (device)
// login seam — leaving the rest of Deps untouched.
func stubDeviceLoginHook(fn func(context.Context, *toolkit.Runtime, tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error)) func(*Deps) {
	return func(d *Deps) { d.AuthLoginDeviceFn = fn }
}

// stubValidateTokenHook installs fn as AuthValidateTokenFn so the token-paste
// path can be exercised without contacting a hub.
func stubValidateTokenHook(fn func(context.Context, *toolkit.Runtime, string, string) (*tapper.WhoAmI, error)) func(*Deps) {
	return func(d *Deps) { d.AuthValidateTokenFn = fn }
}

// stubPrompterHook installs a scripted AuthPrompter for the interactive path.
func stubPrompterHook(p AuthPrompter) func(*Deps) {
	return func(d *Deps) { d.AuthPrompter = p }
}

// combineHooks applies several Deps hooks in order so a single test can stub
// more than one seam (e.g. the device fn and the prompter).
func combineHooks(hooks ...func(*Deps)) func(*Deps) {
	return func(d *Deps) {
		for _, h := range hooks {
			if h != nil {
				h(d)
			}
		}
	}
}

// fakeAuthPrompter is a scripted AuthPrompter — no TTY needed. Any method whose
// func is nil fails the test, so each test wires only what it expects called.
type fakeAuthPrompter struct {
	t            *testing.T
	selectHub    func([]hubChoice) (hubChoice, error)
	selectMethod func() (loginMethod, error)
	endpoint     func() (string, error)
	token        func() (string, error)
	confirmOpen  func(context.Context, string) (bool, error)
}

func (f *fakeAuthPrompter) SelectHub(c []hubChoice) (hubChoice, error) {
	if f.selectHub == nil {
		f.t.Fatal("unexpected SelectHub call")
	}
	return f.selectHub(c)
}

func (f *fakeAuthPrompter) SelectMethod() (loginMethod, error) {
	if f.selectMethod == nil {
		f.t.Fatal("unexpected SelectMethod call")
	}
	return f.selectMethod()
}

func (f *fakeAuthPrompter) PromptEndpointURL() (string, error) {
	if f.endpoint == nil {
		f.t.Fatal("unexpected PromptEndpointURL call")
	}
	return f.endpoint()
}

func (f *fakeAuthPrompter) PromptToken() (string, error) {
	if f.token == nil {
		f.t.Fatal("unexpected PromptToken call")
	}
	return f.token()
}

func (f *fakeAuthPrompter) ConfirmOpenBrowser(ctx context.Context, host string) (bool, error) {
	if f.confirmOpen == nil {
		f.t.Fatal("unexpected ConfirmOpenBrowser call")
	}
	return f.confirmOpen(ctx, host)
}

// newAuthProcess builds a non-TTY Process running `tap auth ...`. A nil hook
// runs the unaltered production wiring; otherwise the hook is attached to the
// ctx via WithTestDepsHook and applied to Deps before NewRootCmd runs.
func newAuthProcess(t *testing.T, hook func(*Deps), args ...string) *sandbox.Process {
	t.Helper()
	return newAuthProcessTTY(t, hook, false, args...)
}

// newAuthProcessTTY is newAuthProcess with an explicit isTTY toggle so tests
// can exercise the interactive selection path (rt.Stream().IsTTY == true).
func newAuthProcessTTY(t *testing.T, hook func(*Deps), isTTY bool, args ...string) *sandbox.Process {
	t.Helper()
	return sandbox.NewProcess(func(ctx context.Context, rt *toolkit.Runtime) (int, error) {
		if hook != nil {
			ctx = WithTestDepsHook(ctx, hook)
		}
		return Run(ctx, rt, args)
	}, isTTY)
}

func newTestSandbox(t *testing.T) *sandbox.Sandbox {
	t.Helper()
	sb := sandbox.NewSandbox(t, &sandbox.Options{
		Home: "/home/testuser",
		User: "testuser",
	})
	// Pin a deterministic hostname so the machine-keyed local hub is stable
	// across machines and CI (bootstrap keys the local hub by hostname).
	require.NoError(t, sb.Runtime().Set("HOSTNAME", "testhost"))
	return sb
}

// atomicDeviceOptsSlot lets parallel tests capture the AuthLoginDeviceOptions
// their stub saw without a sync.Mutex. atomic.Value keeps the race detector
// happy; a plain variable would race between the Cobra goroutine and the test.
type atomicDeviceOptsSlot struct{ v atomic.Value }

func (s *atomicDeviceOptsSlot) Store(opts tapper.AuthLoginDeviceOptions) { s.v.Store(opts) }
func (s *atomicDeviceOptsSlot) Load() tapper.AuthLoginDeviceOptions {
	v, ok := s.v.Load().(tapper.AuthLoginDeviceOptions)
	if !ok {
		return tapper.AuthLoginDeviceOptions{}
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

type stubAuthError struct{ msg string }

func (e *stubAuthError) Error() string { return e.msg }

// TestAuthLoginCmd_MissingHub_FallsBackToDefault confirms the resolution
// chain (decision keg-dev/1035) lets an unflagged, non-TTY login land on the
// compiled-in DefaultHubURL when no Config.Hubs entry, no DefaultHub, and no
// DisableAtlasHub override are present. The empty sandbox is equivalent to
// "no config at all" — the cleanest test of step 5.
func TestAuthLoginCmd_MissingHub_FallsBackToDefault(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	var captured atomicDeviceOptsSlot
	hook := stubDeviceLoginHook(func(_ context.Context, _ *toolkit.Runtime, opts tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
		captured.Store(opts)
		return &tapper.AuthEntry{AccessToken: "stub", TokenType: "Bearer"}, nil
	})

	proc := newAuthProcess(t, hook, "auth", "login")
	res := proc.Run(sb.Context(), sb.Runtime())

	require.NoError(t, res.Err)
	require.Equal(t, tapper.DefaultHubURL, captured.Load().HubURL,
		"unflagged login should resolve to DefaultHubURL via the final fallback of the chain")
}

func TestAuthLoginCmd_UsesFallbackHubAndAdoptsNamespace(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	sb.MustWriteFile("~/.config/tapper/config.yaml", []byte(`fallbackHub: acme
namespaces:
  local:
    hub: testhost
hubs:
  testhost:
    kind: local
    defaultNamespace: local
    basePath: /home/testuser/kegs
  acme:
    kind: remote
    url: https://keg.acme.com
`), 0o644)

	var captured atomicDeviceOptsSlot
	hook := combineHooks(
		stubDeviceLoginHook(func(_ context.Context, _ *toolkit.Runtime, opts tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
			captured.Store(opts)
			return &tapper.AuthEntry{AccessToken: "stub-acme-token", TokenType: "Bearer"}, nil
		}),
		stubValidateTokenHook(func(_ context.Context, _ *toolkit.Runtime, hubURL, token string) (*tapper.WhoAmI, error) {
			require.Equal(t, "https://keg.acme.com", hubURL)
			require.Equal(t, "stub-acme-token", token)
			return &tapper.WhoAmI{Username: "alice", DefaultNamespace: "acme"}, nil
		}),
	)

	proc := newAuthProcess(t, hook, "auth", "login")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "https://keg.acme.com", captured.Load().HubURL)
	require.Contains(t, string(res.Stdout), "Logged in to https://keg.acme.com")

	cfgRaw := string(sb.MustReadFile("~/.config/tapper/config.yaml"))
	require.Contains(t, cfgRaw, "fallbackHub: acme")
	require.Contains(t, cfgRaw, "defaultNamespace: acme")
}

// TestAuthLoginCmd_DisableAtlasHubViaEnv_Errors confirms the SOC2-
// auditability opt-out: setting TAP_DISABLE_ATLAS_HUB=1 with no other
// hub configuration produces a clear error rather than silently routing
// to DefaultHubURL.
func TestAuthLoginCmd_DisableAtlasHubViaEnv_Errors(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Runtime().Env().Set("TAP_DISABLE_ATLAS_HUB", "1"))

	hook := stubDeviceLoginHook(func(context.Context, *toolkit.Runtime, tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
		t.Fatal("login fn must not be called when the default hub is disabled and no other hub is configured")
		return nil, nil
	})

	proc := newAuthProcess(t, hook, "auth", "login")
	res := proc.Run(sb.Context(), sb.Runtime())

	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "implicit atlas hub disabled")
}

func TestAuthLoginCmd_PersistsStoreAndPrintsHub(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	var captured atomicDeviceOptsSlot
	hook := stubDeviceLoginHook(func(_ context.Context, _ *toolkit.Runtime, opts tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
		captured.Store(opts)
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

	// Success line: mentions the canonical hub URL (the chain canonicalizes
	// before passing to the flow) and does NOT leak the access token.
	out := string(res.Stdout)
	require.Contains(t, out, "Logged in to https://hub.example.com")
	require.NotContains(t, out, "stub-access-token")

	captured2 := captured.Load()
	require.Equal(t, "https://hub.example.com", captured2.HubURL)
	require.Equal(t, "tapper-cli", captured2.ClientID)
	require.Equal(t, "read write", captured2.Scope)
	require.Equal(t, "Tapper CLI ("+authDeviceOSLabel()+")", captured2.DeviceLabel)
	require.NotNil(t, captured2.OnUserCode, "device options should carry the CLI's OnUserCode handler")

	// Store persisted at StateRoot/auth.yaml, keyed by the canonical form.
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

func TestAuthDeviceLabelIncludesHostnameWhenRuntimeHasProcessInfo(t *testing.T) {
	t.Parallel()
	rt, err := toolkit.NewRuntime(toolkit.WithProcessInfo(toolkit.ProcessInfo{
		Hostname: "dev laptop.local",
	}))
	require.NoError(t, err)

	require.Equal(t, "Tapper CLI on dev-laptop.local ("+authDeviceOSLabel()+")", authDeviceLabel(rt))
}

func TestAuthLoginCmd_DefaultClientID(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	var captured atomicDeviceOptsSlot
	hook := stubDeviceLoginHook(func(_ context.Context, _ *toolkit.Runtime, opts tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
		captured.Store(opts)
		return &tapper.AuthEntry{AccessToken: "t"}, nil
	})

	proc := newAuthProcess(t, hook, "auth", "login", "--hub", "https://hub.example.com")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "tapper-cli", captured.Load().ClientID, "client-id should default to tapper-cli")
	// User did not pass --timeout, so the CLI passes zero through and lets the
	// device-flow default (10m) win in withDefaults.
	require.Equal(t, time.Duration(0), captured.Load().Timeout)
}

func TestAuthLoginCmd_PropagatesFlowError(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	hook := stubDeviceLoginHook(func(context.Context, *toolkit.Runtime, tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
		return nil, &stubAuthError{msg: "simulated device failure"}
	})

	proc := newAuthProcess(t, hook, "auth", "login", "--hub", "https://hub.example.com")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "simulated device failure")
	_, statErr := sb.Runtime().Stat("/home/testuser/.local/state/tapper/auth.yaml", false)
	require.Error(t, statErr, "store file should not exist when the flow errors")
}

// TestAuthLoginCmd_CanonicalizesHubKey confirms the store key is the
// canonical form even when the user types a mixed-case URL.
func TestAuthLoginCmd_CanonicalizesHubKey(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	hook := stubDeviceLoginHook(func(context.Context, *toolkit.Runtime, tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
		return &tapper.AuthEntry{AccessToken: "t"}, nil
	})

	proc := newAuthProcess(t, hook, "auth", "login", "--hub", "HTTPS://HUB.EXAMPLE.COM/")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	raw := sb.MustReadFile("~/.local/state/tapper/auth.yaml")
	require.Contains(t, string(raw), "https://hub.example.com")
	require.NotContains(t, strings.ToLower(string(raw)), "https://hub.example.com/")
}

// TestAuthLoginCmd_WithToken_ValidatesAndStores covers the non-interactive
// token-paste path: a token piped to --with-token is validated against the hub
// (stubbed) and stored as a Bearer credential.
func TestAuthLoginCmd_WithToken_ValidatesAndStores(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	var seenHub, seenToken atomic.Value // string
	hook := combineHooks(
		stubValidateTokenHook(func(_ context.Context, _ *toolkit.Runtime, hubURL, token string) (*tapper.WhoAmI, error) {
			seenHub.Store(hubURL)
			seenToken.Store(token)
			return &tapper.WhoAmI{UserID: 7, Username: "testuser"}, nil
		}),
		stubDeviceLoginHook(func(context.Context, *toolkit.Runtime, tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
			t.Fatal("device flow must not run on the --with-token path")
			return nil, nil
		}),
	)

	proc := newAuthProcess(t, hook, "auth", "login", "--hub", "https://hub.example.com", "--with-token")
	res := proc.RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader("thub_pasted_token\n"))
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "Logged in to https://hub.example.com")

	require.Equal(t, "https://hub.example.com", seenHub.Load())
	require.Equal(t, "thub_pasted_token", seenToken.Load())

	raw := sb.MustReadFile("~/.local/state/tapper/auth.yaml")
	require.Contains(t, string(raw), "thub_pasted_token")
	require.Contains(t, string(raw), "token_type: Bearer")
}

// TestAuthLoginCmd_WithToken_EmptyStdin_Errors guards the obvious foot-gun:
// --with-token but nothing piped in.
func TestAuthLoginCmd_WithToken_EmptyStdin_Errors(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	hook := stubValidateTokenHook(func(context.Context, *toolkit.Runtime, string, string) (*tapper.WhoAmI, error) {
		t.Fatal("validation must not run when stdin is empty")
		return nil, nil
	})

	proc := newAuthProcess(t, hook, "auth", "login", "--hub", "https://hub.example.com", "--with-token")
	res := proc.RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "no token")
}

// TestAuthLoginCmd_WithToken_InvalidToken_Errors confirms a hub rejection is
// surfaced and nothing is persisted.
func TestAuthLoginCmd_WithToken_InvalidToken_Errors(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	hook := stubValidateTokenHook(func(context.Context, *toolkit.Runtime, string, string) (*tapper.WhoAmI, error) {
		return nil, &stubAuthError{msg: "hub rejected the token"}
	})

	proc := newAuthProcess(t, hook, "auth", "login", "--hub", "https://hub.example.com", "--with-token")
	res := proc.RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader("bad-token\n"))
	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "hub rejected the token")
	_, statErr := sb.Runtime().Stat("/home/testuser/.local/state/tapper/auth.yaml", false)
	require.Error(t, statErr, "store file should not exist when the token is rejected")
}

// TestAuthLoginCmd_Interactive_BrowserFlow drives the TTY path: the fake
// prompter selects a hub and the browser method, the stubbed device flow
// invokes the CLI's OnUserCode handler (with the "copy the URL" branch so no
// real browser launches), and the resulting token is persisted.
func TestAuthLoginCmd_Interactive_BrowserFlow(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	prompter := &fakeAuthPrompter{
		t:            t,
		selectHub:    func(choices []hubChoice) (hubChoice, error) { return choices[0], nil }, // atlas
		selectMethod: func() (loginMethod, error) { return methodBrowser, nil },
		confirmOpen:  func(context.Context, string) (bool, error) { return false, nil }, // copy URL, do not launch
	}

	var captured atomicDeviceOptsSlot
	hook := combineHooks(
		stubPrompterHook(prompter),
		stubDeviceLoginHook(func(ctx context.Context, _ *toolkit.Runtime, opts tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
			captured.Store(opts)
			// Exercise the gh-style handler through the real wiring.
			err := opts.OnUserCode(ctx, tapper.DeviceUserPrompt{
				UserCode:                "ABCD-1234",
				VerificationURI:         "https://atlas.foldwise.ai/device",
				VerificationURIComplete: "https://atlas.foldwise.ai/device?user_code=ABCD-1234",
				ExpiresIn:               600,
			})
			if err != nil {
				return nil, err
			}
			return &tapper.AuthEntry{AccessToken: "browser-token", TokenType: "Bearer"}, nil
		}),
	)

	proc := newAuthProcessTTY(t, hook, true, "auth", "login")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	require.Equal(t, tapper.CanonicalHubURL(tapper.DefaultHubURL), captured.Load().HubURL)
	stderr := string(res.Stderr)
	require.Contains(t, stderr, "ABCD-1234", "one-time code should be shown")
	require.Contains(t, stderr, "atlas.foldwise.ai/device", "verification URL should be shown for copy/paste")
	require.Contains(t, string(res.Stdout), "Logged in to https://atlas.foldwise.ai")

	raw := sb.MustReadFile("~/.local/state/tapper/auth.yaml")
	require.Contains(t, string(raw), "browser-token")
}

// TestAuthLoginCmd_Interactive_TokenFlow drives the TTY path where the user
// picks "Other endpoint", types a URL, then chooses to paste a token.
func TestAuthLoginCmd_Interactive_TokenFlow(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	prompter := &fakeAuthPrompter{
		t:            t,
		selectHub:    func(choices []hubChoice) (hubChoice, error) { return choices[len(choices)-1], nil }, // "Other endpoint…"
		endpoint:     func() (string, error) { return "keg.acme.com", nil },                                // bare host → scheme added
		selectMethod: func() (loginMethod, error) { return methodToken, nil },
		token:        func() (string, error) { return "pasted-tok", nil },
	}

	var seenHub atomic.Value // string
	hook := combineHooks(
		stubPrompterHook(prompter),
		stubValidateTokenHook(func(_ context.Context, _ *toolkit.Runtime, hubURL, token string) (*tapper.WhoAmI, error) {
			seenHub.Store(hubURL)
			require.Equal(t, "pasted-tok", token)
			return &tapper.WhoAmI{Username: "acme"}, nil
		}),
		stubDeviceLoginHook(func(context.Context, *toolkit.Runtime, tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
			t.Fatal("device flow must not run when the token method is chosen")
			return nil, nil
		}),
	)

	proc := newAuthProcessTTY(t, hook, true, "auth", "login")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	require.Equal(t, "https://keg.acme.com", seenHub.Load())
	require.Contains(t, string(res.Stdout), "Logged in to https://keg.acme.com")

	raw := sb.MustReadFile("~/.local/state/tapper/auth.yaml")
	require.Contains(t, string(raw), "pasted-tok")
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
// without running the full flow. Returns the first canonical key written
// (handy when a caller seeds just one entry).
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

// stubWhoAmIHook installs fn as the status validation seam (it flows from
// Deps.AuthValidateTokenFn onto Tap.AuthValidateFn), so `tap auth status` can
// be exercised without contacting a hub.
func stubWhoAmIHook(fn func(context.Context, *toolkit.Runtime, string, string) (*tapper.WhoAmI, error)) func(*Deps) {
	return func(d *Deps) { d.AuthValidateTokenFn = fn }
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

	hook := stubWhoAmIHook(func(_ context.Context, _ *toolkit.Runtime, _, _ string) (*tapper.WhoAmI, error) {
		return &tapper.WhoAmI{Username: "jdoe", DisplayName: "Jane Doe"}, nil
	})
	proc := newAuthProcess(t, hook, "auth", "status")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	out := string(res.Stdout)
	// Bare host header + gh-style "Logged in ... account <user> (<name>)".
	require.Contains(t, out, "Logged in as jdoe (Jane Doe)")
	// Host appears once (header line), not repeated in the status line.
	require.Equal(t, 1, strings.Count(out, "hub.example.com"))
	// Token shown by its 12-char prefix (matches the hub UI), not its suffix.
	require.Contains(t, out, "- Token: supersecretX... (Bearer)")
	require.Contains(t, out, "- Scopes: read, write")
	require.NotContains(t, out, "supersecretXXYZ")
}

func TestAuthStatusCmd_NoDisplayName_OmitsParens(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		"https://hub.example.com": {AccessToken: "thub_abcdef0123456789", TokenType: "Bearer"},
	})

	hook := stubWhoAmIHook(func(_ context.Context, _ *toolkit.Runtime, _, _ string) (*tapper.WhoAmI, error) {
		return &tapper.WhoAmI{Username: "jdoe"}, nil
	})
	proc := newAuthProcess(t, hook, "auth", "status")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	out := string(res.Stdout)
	require.Contains(t, out, "Logged in as jdoe")
	require.NotContains(t, out, "jdoe (")
	require.Contains(t, out, "- Token: thub_abcdef0... (Bearer)")
}

func TestAuthStatusCmd_RejectedToken(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		"https://hub.example.com": {AccessToken: "thub_revokedtoken123", TokenType: "Bearer"},
	})

	hook := stubWhoAmIHook(func(_ context.Context, _ *toolkit.Runtime, _, _ string) (*tapper.WhoAmI, error) {
		return nil, fmt.Errorf("auth: %w (401 Unauthorized)", tapper.ErrTokenRejected)
	})
	proc := newAuthProcess(t, hook, "auth", "status")
	res := proc.Run(sb.Context(), sb.Runtime())
	// A rejected token is reported, not a command failure.
	require.NoError(t, res.Err)
	out := string(res.Stdout)
	require.Contains(t, out, "Failed to validate token: hub rejected the token (401 Unauthorized)")
	require.Contains(t, out, "- Token: thub_revoked... (Bearer)")
}

func TestAuthStatusCmd_UnreachableHub_Degrades(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		"https://hub.example.com": {AccessToken: "thub_sometoken12345", TokenType: "Bearer"},
	})

	hook := stubWhoAmIHook(func(_ context.Context, _ *toolkit.Runtime, _, _ string) (*tapper.WhoAmI, error) {
		return nil, fmt.Errorf("auth: contact hub: dial tcp: connection refused")
	})
	proc := newAuthProcess(t, hook, "auth", "status")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	out := string(res.Stdout)
	require.Contains(t, out, "Could not reach hub to validate token")
	require.Contains(t, out, "- Token: thub_sometok... (Bearer)")
}

func TestAuthStatusCmd_Offline_SkipsValidation(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		"https://hub.example.com": {AccessToken: "thub_offlinetoken99", TokenType: "Bearer"},
	})

	// No validation hook: --offline must not reach the network at all.
	proc := newAuthProcess(t, nil, "auth", "status", "--offline")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	out := string(res.Stdout)
	require.Contains(t, out, "Logged in (offline; token not validated)")
	require.Contains(t, out, "- Token: thub_offline... (Bearer)")
	require.Contains(t, out, "- Method: API token (no expiry)")
	require.NotContains(t, out, "account")
}

func TestAuthStatusCmd_ShortToken_UsesPlaceholder(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		"https://hub.example.com": {AccessToken: "abc"},
	})

	hook := stubWhoAmIHook(func(_ context.Context, _ *toolkit.Runtime, _, _ string) (*tapper.WhoAmI, error) {
		return &tapper.WhoAmI{Username: "jdoe"}, nil
	})
	proc := newAuthProcess(t, hook, "auth", "status")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "- Token: [set]")
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
