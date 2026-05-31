package cli

// Tests for `tap bootstrap`. They live in the internal cli package so they can
// install a fake login seam via the WithTestDepsHook mechanism (see
// cmd_auth_test.go for the shared helpers newTestSandbox / stubDeviceLoginHook /
// runCompletionViaProcess). Bootstrap's login drives the browser (device) flow.

import (
	"context"
	"strings"
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
	require.Contains(t, out, "kind:               cloud")
	require.Contains(t, out, "fallback hub:       atlas")
	require.Contains(t, out, "fallback namespace: testuser")
	require.Contains(t, out, "tap auth login") // no login happened

	raw := sb.MustReadFile("~/.config/tapper/config.yaml")
	require.Contains(t, string(raw), "fallbackHub: atlas")
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
	require.Contains(t, string(res.Stdout), "fallback hub:       local")
	require.NotContains(t, string(res.Stdout), "tap auth login")

	raw := sb.MustReadFile("~/.config/tapper/config.yaml")
	require.Contains(t, string(raw), "fallbackHub: local")
}

// TestBootstrapCmd_Interactive_Enterprise drives the TTY prompts: kind ->
// endpoint -> "no" to login.
func TestBootstrapCmd_Interactive_Enterprise(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	answers := strings.Join([]string{
		"enterprise",           // kind
		"https://keg.acme.com", // endpoint
		"n",                    // log in now? -> no
		"",                     // trailing buffer
	}, "\n")

	proc := newBootstrapProcess(t, nil, true, "bootstrap")
	res := proc.RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(answers))
	require.NoError(t, res.Err, "interactive enterprise bootstrap should succeed: stderr=%q", string(res.Stderr))

	out := string(res.Stdout)
	require.Contains(t, out, "kind:               enterprise")
	require.Contains(t, out, "fallback hub:       acme")

	raw := sb.MustReadFile("~/.config/tapper/config.yaml")
	require.Contains(t, string(raw), "url: https://keg.acme.com")
}

// TestBootstrapCmd_Cloud_Login confirms --login routes through runAuthLogin
// (stubbed) against the atlas URL and persists the token.
func TestBootstrapCmd_Cloud_Login(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	var captured atomicDeviceOptsSlot
	hook := stubDeviceLoginHook(func(_ context.Context, _ *toolkit.Runtime, opts tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
		captured.Store(opts)
		return &tapper.AuthEntry{AccessToken: "stub-cloud-token", TokenType: "Bearer"}, nil
	})

	proc := newBootstrapProcess(t, hook, false, "bootstrap", "--kind", "cloud", "--login")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	require.Equal(t, tapper.CanonicalHubURL(tapper.DefaultHubURL), captured.Load().HubURL)
	require.Contains(t, string(res.Stdout), "Wrote")

	storeRaw := sb.MustReadFile("~/.local/state/tapper/auth.yaml")
	require.Contains(t, string(storeRaw), "stub-cloud-token")
}

// TestBootstrapCmd_Enterprise_Login confirms --login targets the custom endpoint.
func TestBootstrapCmd_Enterprise_Login(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)

	var captured atomicDeviceOptsSlot
	hook := stubDeviceLoginHook(func(_ context.Context, _ *toolkit.Runtime, opts tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error) {
		captured.Store(opts)
		return &tapper.AuthEntry{AccessToken: "stub-ent-token", TokenType: "Bearer"}, nil
	})

	proc := newBootstrapProcess(t, hook, false,
		"bootstrap", "--kind", "enterprise", "--endpoint", "https://keg.acme.com", "--login")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	require.Equal(t, tapper.CanonicalHubURL("https://keg.acme.com"), captured.Load().HubURL)
	require.Contains(t, string(res.Stdout), "fallback hub:       acme")
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
