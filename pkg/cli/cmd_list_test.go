package cli_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestListCommand_IdOnlyOutputsOnlyIDs(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "create", "--title", "One").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewProcess(t, false, "create", "--title", "Two").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	defaultRes := NewProcess(t, false, "list").Run(sb.Context(), sb.Runtime())
	require.NoError(t, defaultRes.Err)
	defaultOut := strings.TrimSpace(string(defaultRes.Stdout))
	require.NotEmpty(t, defaultOut)
	require.Contains(t, defaultOut, "\t", "default list output should include formatted columns")

	idOnlyRes := NewProcess(t, false, "list", "--id-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, idOnlyRes.Err)
	idOnlyOut := strings.TrimSpace(string(idOnlyRes.Stdout))
	require.NotEmpty(t, idOnlyOut)

	lines := strings.Split(idOnlyOut, "\n")
	idPattern := regexp.MustCompile(`^\d+(?:-\d{4})?$`)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		require.NotEmpty(t, line)
		require.Regexp(t, idPattern, line, "id-only output should contain only node IDs")
	}
}

func TestListCommand_ReverseOrdering(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "create", "--title", "One").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewProcess(t, false, "create", "--title", "Two").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewProcess(t, false, "create", "--title", "Three").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	normal := NewProcess(t, false, "list", "--id-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, normal.Err)
	normalLines := strings.Split(strings.TrimSpace(string(normal.Stdout)), "\n")
	require.GreaterOrEqual(t, len(normalLines), 4)
	require.Equal(t, "0", strings.TrimSpace(normalLines[0]))
	require.Equal(t, "3", strings.TrimSpace(normalLines[len(normalLines)-1]))

	reversed := NewProcess(t, false, "list", "--id-only", "--reverse").Run(sb.Context(), sb.Runtime())
	require.NoError(t, reversed.Err)
	reversedLines := strings.Split(strings.TrimSpace(string(reversed.Stdout)), "\n")
	require.GreaterOrEqual(t, len(reversedLines), 4)
	require.Equal(t, "3", strings.TrimSpace(reversedLines[0]))
	require.Equal(t, "0", strings.TrimSpace(reversedLines[len(reversedLines)-1]))
}

// TestListCommand_StaleIndexDoesNotCrash verifies that when on-disk nodes
// significantly outnumber indexed nodes, the list command still succeeds.
// The staleness detection code emits a logger warning but must not break the
// command.
func TestListCommand_StaleIndexDoesNotCrash(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Create one node through the normal path so the dex has entries.
	res := NewProcess(t, false, "create", "--title", "Indexed").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// Write bare node directories directly on disk (content only, no dex update).
	// This simulates an external tool adding nodes without rebuilding the index.
	rt := sb.Runtime()
	kegRoot := "~/kegs/example"
	for i := 100; i < 110; i++ {
		dir := fmt.Sprintf("%s/%d", kegRoot, i)
		require.NoError(t, rt.Mkdir(dir, 0o755, true))
		require.NoError(t, rt.WriteFile(
			fmt.Sprintf("%s/README.md", dir),
			[]byte(fmt.Sprintf("# Bare node %d\n\nNo meta.\n", i)),
			0o644,
		))
	}

	// list should still succeed — the stale-index warning fires via the logger
	// but the command output is unaffected.
	listRes := NewProcess(t, false, "list", "--id-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, listRes.Err)
	listOut := strings.TrimSpace(string(listRes.Stdout))
	require.NotEmpty(t, listOut)

	// The indexed node (and the zero node) should still be in the output.
	require.Contains(t, listOut, "0")
	require.Contains(t, listOut, "1")
}

func TestListCommand_SortUpdated(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Create nodes with advancing clock so they have different updated timestamps.
	sb.Advance(1 * time.Hour)
	res := NewProcess(t, false, "create", "--title", "First").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	sb.Advance(1 * time.Hour)
	res = NewProcess(t, false, "create", "--title", "Second").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	sb.Advance(1 * time.Hour)
	res = NewProcess(t, false, "create", "--title", "Third").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// Sort by updated: oldest first, newest last.
	listRes := NewProcess(t, false, "list", "--id-only", "--sort", "updated").Run(sb.Context(), sb.Runtime())
	require.NoError(t, listRes.Err)
	lines := strings.Split(strings.TrimSpace(string(listRes.Stdout)), "\n")
	trimmed := make([]string, len(lines))
	for i, l := range lines {
		trimmed[i] = strings.TrimSpace(l)
	}
	// Node 0 (zero node) was created first, then 1, 2, 3.
	require.Equal(t, "0", trimmed[0], "oldest updated node should be first")
	require.Equal(t, "3", trimmed[len(trimmed)-1], "newest updated node should be last")
}

func TestListCommand_SortUpdated_WithLimit(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	sb.Advance(1 * time.Hour)
	res := NewProcess(t, false, "create", "--title", "A").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	sb.Advance(1 * time.Hour)
	res = NewProcess(t, false, "create", "--title", "B").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	sb.Advance(1 * time.Hour)
	res = NewProcess(t, false, "create", "--title", "C").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// Limit to 2 most recently updated.
	listRes := NewProcess(t, false, "list", "--id-only", "--sort", "updated", "-n", "2").Run(sb.Context(), sb.Runtime())
	require.NoError(t, listRes.Err)
	lines := strings.Split(strings.TrimSpace(string(listRes.Stdout)), "\n")
	require.Len(t, lines, 2)
	trimmed := make([]string, len(lines))
	for i, l := range lines {
		trimmed[i] = strings.TrimSpace(l)
	}
	// Should be the 2 most recently updated: nodes 2 and 3.
	require.Equal(t, "2", trimmed[0])
	require.Equal(t, "3", trimmed[1])
}

func TestListCommand_SortInvalid(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	listRes := NewProcess(t, false, "list", "--sort", "bogus").Run(sb.Context(), sb.Runtime())
	require.Error(t, listRes.Err)
	require.Contains(t, listRes.Err.Error(), "unknown sort type")
}

func TestListCommand_SortFlagCompletion(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	comp := NewCompletionProcess(t, false, 0, "list", "--sort", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "id")
	require.Contains(t, suggestions, "updated")
	require.Contains(t, suggestions, "created")
	require.Contains(t, suggestions, "accessed")
}
