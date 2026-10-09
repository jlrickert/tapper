package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSearchCompletion_KegsFlag(t *testing.T) {
	t.Parallel()
	res := runCompletionViaProcess(t, "node", "search", "x", "--kegs", "@team/a,")
	require.NoError(t, res.Err)
	// :6 == ShellCompDirectiveNoFileComp|ShellCompDirectiveNoSpace, so a list
	// keeps growing after a comma.
	require.Contains(t, string(res.Stdout), ":6")
}

func TestKegCreateCompletion_KegvFlag(t *testing.T) {
	t.Parallel()
	res := runCompletionViaProcess(t, "keg", "create", "@team/x", "--kegv", "")
	require.NoError(t, res.Err)
	out := string(res.Stdout)
	require.Contains(t, out, "2025-07")
	require.Contains(t, out, "2026-09")
	require.Contains(t, out, ":4")
}
