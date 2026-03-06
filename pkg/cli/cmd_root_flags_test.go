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
}

func TestRepoHelp_HidesInheritedKegTargetFlags(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)

	res := NewProcess(t, false, "repo", "list", "--help").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	stdout := string(res.Stdout)
	require.NotContains(t, stdout, "--keg")
	require.NotContains(t, stdout, "--project")
	require.NotContains(t, stdout, "--path")
	require.NotContains(t, stdout, "--cwd")

	res = NewProcess(t, false, "repo", "config", "edit", "--help").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	stdout = string(res.Stdout)
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

func TestTap_RootPersistentKegFlagCompletionSuggestsKegs(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "personal")
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

func TestKegV2Help_HidesPersistentKegTargetFlags(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)

	res := NewKegV2Process(t, false, "--help").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	stdout := string(res.Stdout)
	require.NotContains(t, stdout, "--keg")
	require.NotContains(t, stdout, "--project")
	require.NotContains(t, stdout, "--path")
	require.NotContains(t, stdout, "--cwd")
}
