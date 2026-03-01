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

func TestTap_RootPersistentKegFlagCompletionSuggestsKegs(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "--keg", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "personal")
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
