package cli_test

import (
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
	require.Contains(t, stdout, "--project")
	require.Contains(t, stdout, "--path")
	require.Contains(t, stdout, "--cwd")
	require.Contains(t, stdout, "--flight")
}

func TestTap_FlightFlagComposesWithKegTargetFlags(t *testing.T) {
	t.Parallel()

	// A flight is an overlay (keg restriction + instructions), not a target
	// selector, so combining --flight with a single-keg selector must NOT raise
	// the old cobra mutual-exclusivity error. The command may still fail for
	// unrelated reasons (e.g. the flight not existing), but never with the mutex
	// error.
	cases := []struct {
		name string
		args []string
	}{
		{"keg", []string{"--flight", "f1", "--keg", "personal", "orient"}},
		{"project", []string{"--flight", "f1", "--project", "orient"}},
		{"path", []string{"--flight", "f1", "--path", "/tmp", "orient"}},
		{"cwd", []string{"--flight", "f1", "--cwd", "orient"}},
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
	require.NotContains(t, stdout, "--path")
	require.NotContains(t, stdout, "--cwd")
	require.Contains(t, stdout, "--project")
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
// flag completer is registered and returns no suggestions without erroring:
// with the alias map removed, kegs are no longer enumerable from config.
func TestTap_RootPersistentKegFlagCompletion(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Empty(t, suggestions)
}

func TestTap_GlobalFlagsMutuallyExclusive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		errFragment string
	}{
		{
			name:        "keg_and_project_conflict",
			args:        []string{"cat", "0", "--keg", "foo", "--project"},
			errFragment: "--keg cannot be used with --project, --cwd, or --path",
		},
		{
			name:        "keg_and_cwd_conflict",
			args:        []string{"cat", "0", "--keg", "foo", "--cwd"},
			errFragment: "--keg cannot be used with --project, --cwd, or --path",
		},
		{
			name:        "keg_and_path_conflict",
			args:        []string{"cat", "0", "--keg", "foo", "--path", "/tmp"},
			errFragment: "--keg cannot be used with --project, --cwd, or --path",
		},
		{
			name:        "project_and_path_conflict",
			args:        []string{"cat", "0", "--project", "--path", "/tmp"},
			errFragment: "--project cannot be used with --path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(innerT *testing.T) {
			innerT.Parallel()
			sb := NewSandbox(innerT, testutils.WithFixture("testuser", "~"))

			h := NewProcess(innerT, false, tt.args...)
			res := h.Run(sb.Context(), sb.Runtime())

			require.Error(innerT, res.Err)
			require.Contains(innerT, string(res.Stderr), tt.errFragment)
		})
	}
}

func TestTap_PathFlagNonexistentDirectoryShowsClearError(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "--path", "jiberish", "cat", "0").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	stderr := string(res.Stderr)
	require.Contains(t, stderr, "jiberish")
	require.Contains(t, stderr, "does not exist")
}

func TestKegHelp_HidesPersistentKegTargetFlags(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)

	res := NewKegProcess(t, false, "--help").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	stdout := string(res.Stdout)
	require.NotContains(t, stdout, "--keg")
	require.NotContains(t, stdout, "--project")
	require.NotContains(t, stdout, "--path")
	require.NotContains(t, stdout, "--cwd")
}
