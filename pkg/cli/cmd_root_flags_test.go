package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestTapHelp_ShowsPersistentKegTargetFlags(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)

	res := NewProcess(t, false, "--help").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	stdout := string(res.Stdout)
	require.Contains(t, stdout, "--keg")
	require.Contains(t, stdout, "--namespace")
	require.Contains(t, stdout, "--hub")
	require.Contains(t, stdout, "--flight")
}

func TestTap_FlightFlagComposesWithKegTargetFlags(t *testing.T) {
	t.Parallel()

	// A flight is not a target selector, so combining --flight with a single-keg
	// selector must NOT raise the old cobra mutual-exclusivity error. The command
	// may still fail for unrelated reasons (e.g. the flight not existing), but
	// never with the mutex error.
	cases := []struct {
		name string
		args []string
	}{
		{"keg", []string{"--flight", "f1", "--keg", "personal", "orient"}},
		{"namespace", []string{"--flight", "f1", "--namespace", "local", "orient"}},
		{"hub", []string{"--flight", "f1", "--hub", "local", "orient"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			sb := NewSandbox(t)

			res := NewProcess(t, false, c.args...).Run(sb.Context(), sb.Runtime())
			combined := string(res.Stdout) + string(res.Stderr)
			require.NotContains(t, combined, "none of the others can be",
				"--flight must compose with --%s, not be mutually exclusive", c.name)
		})
	}
}

// TestConfigHelp_HidesInheritedKegTargetFlags verifies that a command which
// re-binds a local --project flag (here `config edit`) hides the inherited
// persistent keg-target flags from its help so users don't see duplicate
// entries. (The old `repo` alias group is gone; kegs are listed via
// `tap hub list`, so only `config`/`init` still filter these flags.)
func TestConfigHelp_HidesInheritedKegTargetFlags(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)

	// `config` moved to the top level but still defines a local --project flag
	// that shadows the persistent keg-target flags; its help should hide
	// --keg/--path/--cwd while keeping its own --project.
	res := NewProcess(t, false, "config", "edit", "--help").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	stdout := string(res.Stdout)
	require.NotContains(t, stdout, "--keg")
	require.NotContains(t, stdout, "--namespace")
	require.NotContains(t, stdout, "--hub")
	require.Contains(t, stdout, "--project")
}

func TestTap_DirectCatBypassesFlightCover(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "cat", "0", "--keg", "work", "--flight", "+focused", "--content-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "# Sorry, planned but not yet available")
}

func TestTap_DirectCreateBypassesViewerFlightCap(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "create", "--keg", "personal", "--flight", "+focused", "--title", "Allowed CLI Write").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	nodeID := strings.TrimSpace(string(res.Stdout))
	require.NotEmpty(t, nodeID)
	content := fixtureContent(t, sb.Runtime(), "personal", nodeID)
	require.Contains(t, content, "# Allowed CLI Write")
}

func TestTap_RootPersistentKegFlagBeforeCommand(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "--keg", "personal", "cat", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "# Personal Overview")
}

func TestTap_RootPersistentShortKegFlagNumericShorthandUsesCat(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "-k", "personal", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "# Personal Overview")
}

func TestTap_RootPersistentKegFlagNumericShorthandCompletionUsesCat(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "-k", "personal", "1", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "0")
	require.Contains(t, suggestions, "2")
	require.Contains(t, suggestions, "3")
}

// TestTap_RootPersistentKegFlagCompletion verifies the root persistent --keg
// flag completer is registered and returns logical keg references.
func TestTap_RootPersistentKegFlagCompletion(t *testing.T) {
	t.Parallel()

	sb := NewRemoteKegListSandbox(t, remoteCompletionKegs())

	comp := NewCompletionProcess(t, false, 0, "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "@team/personal")
	require.Contains(t, suggestions, "personal")
}

// TestTap_KegNamespaceConflict verifies that pinning a namespace twice — once
// inside an @namespace/keg reference and again with --namespace — is rejected,
// rather than one silently winning.
func TestTap_KegNamespaceConflict(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "cat", "0", "--keg", "@work/dev", "--namespace", "other").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "conflicts with the namespace")
}
