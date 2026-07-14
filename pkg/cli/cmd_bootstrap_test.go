package cli

// Tests for `tap bootstrap`. They live in the internal cli package so they can
// install a fake login seam via the WithTestDepsHook mechanism (see
// cmd_auth_test.go for the shared helpers newTestSandbox / stubDeviceLoginHook /
// runCompletionViaProcess). Bootstrap's login drives the browser (device) flow.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// newBootstrapProcess builds a Process running `tap bootstrap ...`. A nil hook
// runs the production wiring; isTTY toggles the interactive prompt path. Use
// the returned Process's Run (non-interactive) or RunWithIO (scripted stdin).
func newBootstrapProcess(t *testing.T, hook func(*Deps), isTTY bool, args ...string) *sandbox.Process {
	t.Helper()
	return sandbox.NewProcess(func(ctx context.Context, rt *toolkit.Runtime) (int, error) {
		if hook != nil {
			ctx = WithTestDepsHook(ctx, hook)
		}
		return Run(ctx, rt, args)
	}, isTTY)
}

func stubBootstrapPrompterHook(p BootstrapPrompter) func(*Deps) {
	return func(d *Deps) { d.BootstrapPrompter = p }
}

type fakeBootstrapPrompter struct {
	t            *testing.T
	kind         func() (string, error)
	endpoint     func() (string, error)
	confirmLogin func(string) (bool, error)
	selectKeg    func([]string) (bootstrapDefaultKegSelection, error)
	selectFlight func([]string, string) (string, error)
	manualKeg    func() (string, error)
	newKeg       func() (string, error)
}

func (f *fakeBootstrapPrompter) SelectFlight(available []string, current string) (string, error) {
	if f.selectFlight == nil {
		f.t.Fatal("unexpected SelectFlight call")
	}
	return f.selectFlight(available, current)
}

func (f *fakeBootstrapPrompter) SelectBootstrapKind() (string, error) {
	if f.kind == nil {
		f.t.Fatal("unexpected SelectBootstrapKind call")
	}
	return f.kind()
}

func (f *fakeBootstrapPrompter) PromptBootstrapEndpoint() (string, error) {
	if f.endpoint == nil {
		f.t.Fatal("unexpected PromptBootstrapEndpoint call")
	}
	return f.endpoint()
}

func (f *fakeBootstrapPrompter) ConfirmBootstrapLogin(host string) (bool, error) {
	if f.confirmLogin == nil {
		f.t.Fatal("unexpected ConfirmBootstrapLogin call")
	}
	return f.confirmLogin(host)
}

func (f *fakeBootstrapPrompter) SelectDefaultKeg(available []string) (bootstrapDefaultKegSelection, error) {
	if f.selectKeg == nil {
		f.t.Fatal("unexpected SelectDefaultKeg call")
	}
	return f.selectKeg(available)
}

func (f *fakeBootstrapPrompter) PromptManualDefaultKeg() (string, error) {
	if f.manualKeg == nil {
		f.t.Fatal("unexpected PromptManualDefaultKeg call")
	}
	return f.manualKeg()
}

func (f *fakeBootstrapPrompter) PromptNewKegName() (string, error) {
	if f.newKeg == nil {
		f.t.Fatal("unexpected PromptNewKegName call")
	}
	return f.newKeg()
}

// commandNames returns the direct subcommand names of the root command built
// for the given profile.
func commandNames(t *testing.T, rt *toolkit.Runtime, profile Profile) map[string]bool {
	t.Helper()
	root := NewRootCmd(&Deps{Profile: profile, Runtime: rt})
	names := make(map[string]bool)
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	return names
}

func TestBootstrapCmd_NonInteractive_DefaultsToCloud(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	proc := newBootstrapProcess(t, nil, false, "bootstrap")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	require.Contains(t, out, "Wrote")
	require.Contains(t, out, "kind:         cloud")
	require.Contains(t, out, "fallback hub: atlas")
	require.NotContains(t, out, "namespace:",
		"no login yet, so the cloud hub has no namespace to report")
	require.Contains(t, out, "tap auth login") // no login happened

	raw := sb.MustReadFile("~/.config/tapper/config.yaml")
	require.Contains(t, string(raw), "fallbackHub: atlas")
	require.NotContains(t, string(raw), "fallbackNamespace:",
		"namespace comes from the hub, not a global fallback")
}

func TestBootstrapCmd_Local_NoLogin(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	hook := stubDeviceLoginHook(func(context.Context, *toolkit.Runtime, tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
		t.Fatal("local bootstrap must never log in")
		return nil, nil
	})

	proc := newBootstrapProcess(t, hook, false, "bootstrap", "--kind", "local")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "fallback hub: testhost")
	require.Contains(t, string(res.Stdout), "namespace:    local",
		"a local deployment defaults to the @local namespace, not the OS user")
	require.NotContains(t, string(res.Stdout), "tap auth login")

	raw := sb.MustReadFile("~/.config/tapper/config.yaml")
	require.Contains(t, string(raw), "fallbackHub: testhost")
	require.NotContains(t, string(raw), "fallbackNamespace:",
		"namespace comes from the local hub, not a global fallback")
	require.Contains(t, string(raw), "defaultNamespace: local",
		"the local hub carries the @local namespace")
}

// TestBootstrapCmd_Local_CreatesDefaultKeg confirms a local bootstrap actually
// creates the chosen keg on disk (so the user is immediately up and running) and
// that re-running is idempotent.
func TestBootstrapCmd_Local_CreatesDefaultKeg(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	proc := newBootstrapProcess(t, nil, false, "bootstrap", "--kind", "local", "--default-keg", "private")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "created keg:",
		"local bootstrap should create the chosen keg so the user is ready")
	require.Contains(t, string(res.Stdout), "@local/private")

	// The keg was materialized on disk at the local hub's basePath.
	kegFile := sb.MustReadFile("~/.local/share/tapper/kegs/@local/private/keg")
	require.Contains(t, string(kegFile), "kegv")

	// Re-running is idempotent: the keg already exists, so no error and it is not
	// reported as freshly created.
	proc2 := newBootstrapProcess(t, nil, false, "bootstrap", "--kind", "local", "--default-keg", "private")
	res2 := proc2.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res2.Err)
	require.NotContains(t, string(res2.Stdout), "created keg:",
		"an already-existing keg should not be reported as created")
}

func TestBootstrapCmd_Interactive_SelectsFlightAndPreservesItOnSkip(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/.local/share/tapper/kegs/flights.d/focused.yaml",
		[]byte("title: Focused\n"), 0o644))

	firstPrompter := &fakeBootstrapPrompter{
		t:         t,
		manualKeg: func() (string, error) { return "", nil },
		selectFlight: func(available []string, current string) (string, error) {
			require.Equal(t, []string{"@local/+focused"}, available)
			require.Empty(t, current)
			return "@local/+focused", nil
		},
	}
	first := newBootstrapProcess(t, stubBootstrapPrompterHook(firstPrompter), true,
		"bootstrap", "--kind", "local")
	res := first.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "stderr=%q", string(res.Stderr))
	require.Contains(t, string(res.Stdout), "flight:       @local/+focused")
	require.Contains(t, string(sb.MustReadFile("~/.config/tapper/config.yaml")), "flight: '@local/+focused'")

	secondPrompter := &fakeBootstrapPrompter{
		t:         t,
		manualKeg: func() (string, error) { return "", nil },
		selectFlight: func(available []string, current string) (string, error) {
			require.Equal(t, []string{"@local/+focused"}, available)
			require.Equal(t, "@local/+focused", current, "existing baseline should be preselected")
			return "", nil // Skip for now.
		},
	}
	second := newBootstrapProcess(t, stubBootstrapPrompterHook(secondPrompter), true,
		"bootstrap", "--kind", "local")
	res = second.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "stderr=%q", string(res.Stderr))
	require.Contains(t, string(sb.MustReadFile("~/.config/tapper/config.yaml")), "flight: '@local/+focused'",
		"Skip must preserve the existing baseline")
}

func TestBootstrapCmd_NoFlightsReportsRecoveryOnly(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	prompter := &fakeBootstrapPrompter{
		t:         t,
		manualKeg: func() (string, error) { return "", nil },
	}
	proc := newBootstrapProcess(t, stubBootstrapPrompterHook(prompter), true,
		"bootstrap", "--kind", "local")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "flight:       recovery-only")
	require.Contains(t, string(res.Stdout), "tap bootstrap --flight @namespace/+slug")
	require.NotContains(t, string(sb.MustReadFile("~/.config/tapper/config.yaml")), "flight:")
}

func TestBootstrapCmd_ExplicitFlightValidatesAndPersists(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/.local/share/tapper/kegs/flights.d/focused.yaml",
		[]byte("title: Focused\n"), 0o644))

	proc := newBootstrapProcess(t, nil, false,
		"bootstrap", "--kind", "local", "--flight", "+focused")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "stderr=%q", string(res.Stderr))
	require.Contains(t, string(res.Stdout), "flight:       @local/+focused")
	require.Contains(t, string(sb.MustReadFile("~/.config/tapper/config.yaml")), "flight: '@local/+focused'")

	bad := newBootstrapProcess(t, nil, false,
		"bootstrap", "--kind", "local", "--flight", "+missing")
	badRes := bad.Run(sb.Context(), sb.Runtime())
	require.Error(t, badRes.Err)
	require.Contains(t, badRes.Err.Error(), "invalid bootstrap flight")
}

func TestBootstrapCmd_ImplicitFlightOverrideIsNotPersisted(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Runtime().Env().Set("TAP_FLIGHT", "@local/+environment"))

	proc := newBootstrapProcess(t, nil, false, "bootstrap", "--kind", "local")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.NotContains(t, string(sb.MustReadFile("~/.config/tapper/config.yaml")), "flight:",
		"an inherited environment value must not become the user baseline")
}

func TestBootstrapCmd_ExplicitRemoteFlightPersistsCanonicalRef(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/v1/@bob/+focused", r.URL.Path)
		require.Equal(t, "Bearer remote-token", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(tapper.HubFlight{Namespace: "bob", Slug: "focused", Title: "Focused"})
	}))
	defer srv.Close()
	store := &tapper.AuthStore{}
	store.Set(tapper.CanonicalHubURL(srv.URL), tapper.AuthEntry{AccessToken: "remote-token", TokenType: "Bearer"})
	require.NoError(t, store.Save(sb.Context(), sb.Runtime(), "/home/testuser/.local/state/tapper/auth.yaml"))

	proc := newBootstrapProcess(t, nil, false,
		"bootstrap", "--kind", "enterprise", "--endpoint", srv.URL, "--no-login",
		"--flight", "@bob/+focused")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "stderr=%q", string(res.Stderr))
	require.Contains(t, string(res.Stdout), "flight:       @bob/+focused")
	require.Contains(t, string(sb.MustReadFile("~/.config/tapper/config.yaml")), "flight: '@bob/+focused'")
}

// TestBootstrapCmd_Cloud_DoesNotCreateKeg confirms cloud/enterprise bootstrap
// records the chosen keg but does not create it (a remote create needs login +
// hub permissions, deferred to `tap keg create`).
func TestBootstrapCmd_Cloud_DoesNotCreateKeg(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	proc := newBootstrapProcess(t, nil, false, "bootstrap", "--kind", "cloud", "--default-keg", "notes", "--no-login")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "default keg:  notes")
	require.NotContains(t, string(res.Stdout), "created keg:",
		"cloud bootstrap records the keg but does not create it on the hub")
}

// TestBootstrapCmd_Interactive_Enterprise drives the TTY prompts: kind ->
// endpoint -> "no" to login.
func TestBootstrapCmd_Interactive_Enterprise(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	prompter := &fakeBootstrapPrompter{
		t:        t,
		kind:     func() (string, error) { return tapper.BootstrapKindEnterprise, nil },
		endpoint: func() (string, error) { return "https://keg.acme.com", nil },
		confirmLogin: func(host string) (bool, error) {
			require.Equal(t, "keg.acme.com", host)
			return false, nil
		},
		manualKeg: func() (string, error) { return "", nil },
	}
	hook := stubBootstrapPrompterHook(prompter)

	proc := newBootstrapProcess(t, hook, true, "bootstrap")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "interactive enterprise bootstrap should succeed: stderr=%q", string(res.Stderr))

	out := string(res.Stdout)
	require.Contains(t, out, "kind:         enterprise")
	require.Contains(t, out, "fallback hub: acme")

	raw := sb.MustReadFile("~/.config/tapper/config.yaml")
	require.Contains(t, string(raw), "url: https://keg.acme.com")
}

// TestBootstrapCmd_Cloud_Login confirms --login routes through runAuthLogin
// (stubbed) against the atlas URL and persists the token.
func TestBootstrapCmd_Cloud_Login(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	var captured atomicDeviceOptsSlot
	// Compose the device-login stub with a whoami stub so the post-login
	// namespace probe stays offline and deterministic: the hub reports alice's
	// home namespace, which bootstrap adopts onto the cloud hub's namespace field.
	hook := combineHooks(
		stubDeviceLoginHook(func(_ context.Context, _ *toolkit.Runtime, opts tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
			captured.Store(opts)
			return &tapper.AuthEntry{AccessToken: "stub-cloud-token", TokenType: "Bearer"}, nil
		}),
		stubValidateTokenHook(func(_ context.Context, _ *toolkit.Runtime, _, _ string) (*tapper.WhoAmI, error) {
			return &tapper.WhoAmI{UserID: 1, Username: "alice", DefaultNamespace: "alice"}, nil
		}),
	)

	proc := newBootstrapProcess(t, hook, false, "bootstrap", "--kind", "cloud", "--login")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	require.Equal(t, tapper.CanonicalHubURL(tapper.DefaultHubURL), captured.Load().HubURL)
	require.Contains(t, string(res.Stdout), "Wrote")
	require.Contains(t, string(res.Stdout), "namespace:    alice",
		"cloud bootstrap adopts the hub's default_namespace after login")

	storeRaw := sb.MustReadFile("~/.local/state/tapper/auth.yaml")
	require.Contains(t, string(storeRaw), "stub-cloud-token")

	cfgRaw := sb.MustReadFile("~/.config/tapper/config.yaml")
	require.NotContains(t, string(cfgRaw), "fallbackNamespace:",
		"the adopted namespace lives on the hub, not as a global fallback")
	require.Contains(t, string(cfgRaw), "defaultNamespace: alice",
		"the adopted namespace becomes the cloud hub's per-hub default")
}

// TestBootstrapCmd_Enterprise_Login confirms --login targets the custom endpoint.
func TestBootstrapCmd_Enterprise_Login(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	var captured atomicDeviceOptsSlot
	hook := combineHooks(
		stubDeviceLoginHook(func(_ context.Context, _ *toolkit.Runtime, opts tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
			captured.Store(opts)
			return &tapper.AuthEntry{AccessToken: "stub-ent-token", TokenType: "Bearer"}, nil
		}),
		stubValidateTokenHook(func(_ context.Context, _ *toolkit.Runtime, _, _ string) (*tapper.WhoAmI, error) {
			return &tapper.WhoAmI{UserID: 2, Username: "bob", DefaultNamespace: "bob"}, nil
		}),
	)

	proc := newBootstrapProcess(t, hook, false,
		"bootstrap", "--kind", "enterprise", "--endpoint", "https://keg.acme.com", "--login")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	require.Equal(t, tapper.CanonicalHubURL("https://keg.acme.com"), captured.Load().HubURL)
	require.Contains(t, string(res.Stdout), "fallback hub: acme")
	require.Contains(t, string(res.Stdout), "namespace:    bob",
		"enterprise bootstrap adopts the hub's default_namespace after login")

	cfgRaw := sb.MustReadFile("~/.config/tapper/config.yaml")
	require.NotContains(t, string(cfgRaw), "fallbackNamespace:",
		"the adopted namespace lives on the hub, not as a global fallback")
	// The adopted namespace becomes the enterprise hub's per-hub default.
	require.Contains(t, string(cfgRaw), "defaultNamespace: bob")
}

func TestBootstrapCmd_Interactive_LoginSelectsExistingKeg(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	var sawList atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/flights" {
			_ = json.NewEncoder(w).Encode([]tapper.HubFlight{})
			return
		}
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/kegs" {
			t.Errorf("unexpected hub request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer pasted-token" {
			t.Errorf("unexpected authorization header: %q", got)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		sawList.Store(true)
		_ = json.NewEncoder(w).Encode([]tapper.HubKeg{{Namespace: "bob", Alias: "notes"}})
	}))
	defer srv.Close()

	authPrompter := &fakeAuthPrompter{
		t:            t,
		selectMethod: func() (loginMethod, error) { return methodToken, nil },
		token:        func() (string, error) { return "pasted-token", nil },
	}
	bootstrapPrompter := &fakeBootstrapPrompter{
		t: t,
		selectKeg: func(available []string) (bootstrapDefaultKegSelection, error) {
			require.Equal(t, []string{"@bob/notes"}, available)
			return bootstrapDefaultKegSelection{Action: bootstrapDefaultKegUseRef, Ref: "@bob/notes"}, nil
		},
	}
	hook := combineHooks(
		stubPrompterHook(authPrompter),
		stubBootstrapPrompterHook(bootstrapPrompter),
		stubValidateTokenHook(func(_ context.Context, _ *toolkit.Runtime, hubURL, token string) (*tapper.WhoAmI, error) {
			require.Equal(t, srv.URL, hubURL)
			require.Equal(t, "pasted-token", token)
			return &tapper.WhoAmI{UserID: 2, Username: "bob", DefaultNamespace: "bob"}, nil
		}),
		stubDeviceLoginHook(func(context.Context, *toolkit.Runtime, tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
			t.Fatal("token bootstrap must not start browser login")
			return nil, nil
		}),
	)

	proc := newBootstrapProcess(t, hook, true,
		"bootstrap", "--kind", "enterprise", "--endpoint", srv.URL, "--login")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "stderr=%q", string(res.Stderr))
	require.True(t, sawList.Load(), "bootstrap should list remote kegs after login")
	require.Contains(t, string(res.Stdout), "default keg:  @bob/notes")
	require.NotContains(t, string(res.Stdout), "created keg:")

	cfgRaw := string(sb.MustReadFile("~/.config/tapper/config.yaml"))
	require.Contains(t, cfgRaw, "fallbackKeg: '@bob/notes'")
	require.Contains(t, cfgRaw, "defaultNamespace: bob")
}

func TestBootstrapCmd_Interactive_NoKegsCreatesOne(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	var sawList atomic.Bool
	var sawCreate atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer pasted-token" {
			t.Errorf("unexpected authorization header: %q", got)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/kegs":
			sawList.Store(true)
			_ = json.NewEncoder(w).Encode([]tapper.HubKeg{})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/flights":
			_ = json.NewEncoder(w).Encode([]tapper.HubFlight{})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/@bob/kegs":
			var payload map[string]string
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode create payload: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if payload["alias"] != "notes" {
				t.Errorf("unexpected create alias: %q", payload["alias"])
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			sawCreate.Store(true)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"namespace": "bob", "alias": "notes"})
		default:
			t.Errorf("unexpected hub request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	authPrompter := &fakeAuthPrompter{
		t:            t,
		selectMethod: func() (loginMethod, error) { return methodToken, nil },
		token:        func() (string, error) { return "pasted-token", nil },
	}
	bootstrapPrompter := &fakeBootstrapPrompter{
		t:      t,
		newKeg: func() (string, error) { return "notes", nil },
	}
	hook := combineHooks(
		stubPrompterHook(authPrompter),
		stubBootstrapPrompterHook(bootstrapPrompter),
		stubValidateTokenHook(func(_ context.Context, _ *toolkit.Runtime, hubURL, token string) (*tapper.WhoAmI, error) {
			require.Equal(t, srv.URL, hubURL)
			require.Equal(t, "pasted-token", token)
			return &tapper.WhoAmI{UserID: 2, Username: "bob", DefaultNamespace: "bob"}, nil
		}),
		stubDeviceLoginHook(func(context.Context, *toolkit.Runtime, tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
			t.Fatal("token bootstrap must not start browser login")
			return nil, nil
		}),
	)

	proc := newBootstrapProcess(t, hook, true,
		"bootstrap", "--kind", "enterprise", "--endpoint", srv.URL, "--login")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "stderr=%q", string(res.Stderr))
	require.True(t, sawList.Load(), "bootstrap should list remote kegs after login")
	require.True(t, sawCreate.Load(), "bootstrap should create a keg when the hub reports none")
	require.Contains(t, string(res.Stdout), "default keg:  @bob/notes")
	require.Contains(t, string(res.Stdout), "created keg:  @bob/notes")

	cfgRaw := string(sb.MustReadFile("~/.config/tapper/config.yaml"))
	require.Contains(t, cfgRaw, "fallbackKeg: '@bob/notes'")
	require.Contains(t, cfgRaw, "defaultNamespace: bob")
}

func TestBootstrapCmd_Enterprise_NonInteractiveRequiresEndpoint(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	proc := newBootstrapProcess(t, nil, false, "bootstrap", "--kind", "enterprise")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "endpoint")
}

// TestBootstrapCmd_ProfileGate confirms bootstrap is registered for `tap`
// (IncludeConfigCommand) and absent from the pruned `keg` profile.
func TestBootstrapCmd_ProfileGate(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	tapCmds := commandNames(t, sb.Runtime(), TapProfile())
	require.True(t, tapCmds["bootstrap"], "tap should expose the bootstrap command")

	kegCmds := commandNames(t, sb.Runtime(), KegProfile())
	require.False(t, kegCmds["bootstrap"], "keg must not expose the bootstrap command")
}

// --- completion ---

func TestBootstrapCompletion_KindFlag_ListsKinds(t *testing.T) {
	t.Parallel()
	res := runCompletionViaProcess(t, "bootstrap", "--kind", "")
	require.NoError(t, res.Err)
	out := string(res.Stdout)
	require.Contains(t, out, "local")
	require.Contains(t, out, "cloud")
	require.Contains(t, out, "enterprise")
}

func TestBootstrapCompletion_EndpointFlag_NoFileComp(t *testing.T) {
	t.Parallel()
	res := runCompletionViaProcess(t, "bootstrap", "--endpoint", "")
	require.NoError(t, res.Err)
	// :4 == ShellCompDirectiveNoFileComp.
	require.Contains(t, string(res.Stdout), ":4")
}
