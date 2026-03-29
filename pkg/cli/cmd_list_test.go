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

	// Limit to first 2 by updated order (oldest first).
	listRes := NewProcess(t, false, "list", "--id-only", "--sort", "updated", "-n", "2").Run(sb.Context(), sb.Runtime())
	require.NoError(t, listRes.Err)
	lines := strings.Split(strings.TrimSpace(string(listRes.Stdout)), "\n")
	require.Len(t, lines, 2)
	trimmed := make([]string, len(lines))
	for i, l := range lines {
		trimmed[i] = strings.TrimSpace(l)
	}
	// Should be the 2 oldest updated: nodes 0 and 1.
	require.Equal(t, "0", trimmed[0])
	require.Equal(t, "1", trimmed[1])
}

// TestListCommand_FormatCreatedTimestamp verifies the %c format placeholder
// outputs the created timestamp.
func TestListCommand_FormatCreatedTimestamp(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	sb.Advance(1 * time.Hour)
	res := NewProcess(t, false, "create", "--title", "FormatTest").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// Use %c to show created timestamp
	listRes := NewProcess(t, false, "list", "-f", "%i\t%c\t%t", "--sort", "created").Run(sb.Context(), sb.Runtime())
	require.NoError(t, listRes.Err)
	out := strings.TrimSpace(string(listRes.Stdout))
	require.NotEmpty(t, out)

	// Each line should have a valid RFC3339 timestamp in the second column
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 3)
		require.GreaterOrEqual(t, len(parts), 2)
		_, parseErr := time.Parse(time.RFC3339, parts[1])
		require.NoError(t, parseErr, "expected %c to produce valid RFC3339 timestamp, got %q", parts[1])
	}
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

func TestListCommand_OffsetSkipsResults(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Create 3 nodes (plus zero node = 4 total).
	for _, title := range []string{"One", "Two", "Three"} {
		res := NewProcess(t, false, "create", "--title", title).Run(sb.Context(), sb.Runtime())
		require.NoError(t, res.Err)
	}

	// Without offset: should see 0,1,2,3.
	all := NewProcess(t, false, "list", "--id-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, all.Err)
	allLines := strings.Split(strings.TrimSpace(string(all.Stdout)), "\n")
	require.Len(t, allLines, 4)

	// Offset 2: skip first 2 nodes.
	offset := NewProcess(t, false, "list", "--id-only", "--offset", "2").Run(sb.Context(), sb.Runtime())
	require.NoError(t, offset.Err)
	offsetLines := strings.Split(strings.TrimSpace(string(offset.Stdout)), "\n")
	require.Len(t, offsetLines, 2)
	require.Equal(t, "2", strings.TrimSpace(offsetLines[0]))
	require.Equal(t, "3", strings.TrimSpace(offsetLines[1]))
}

func TestListCommand_OffsetWithLimit(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	for _, title := range []string{"A", "B", "C", "D", "E"} {
		res := NewProcess(t, false, "create", "--title", title).Run(sb.Context(), sb.Runtime())
		require.NoError(t, res.Err)
	}

	// 6 total nodes (0-5). Offset 1 skips node 0, leaving (1,2,3,4,5). Limit 4 takes first 4: (1,2,3,4).
	res := NewProcess(t, false, "list", "--id-only", "-n", "4", "--offset", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	lines := strings.Split(strings.TrimSpace(string(res.Stdout)), "\n")
	require.Len(t, lines, 4)
	require.Equal(t, "1", strings.TrimSpace(lines[0]))
	require.Equal(t, "2", strings.TrimSpace(lines[1]))
	require.Equal(t, "3", strings.TrimSpace(lines[2]))
	require.Equal(t, "4", strings.TrimSpace(lines[3]))
}

func TestListCommand_NegativeOffsetErrors(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "list", "--id-only", "--offset", "-1").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "offset must be >= 0")
}

func TestListCommand_OffsetBeyondRange(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "list", "--id-only", "--offset", "9999").Run(sb.Context(), sb.Runtime())
	// Should error because no nodes found after offset.
	require.Error(t, res.Err)
}

func TestListCommand_DotPrefixQuery_CreatedGT(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Create nodes with advancing clock so they have different created timestamps.
	// The zero node is created at the sandbox base time (2000-01-01 00:00:00 UTC
	// is the default for test sandboxes).
	sb.Advance(1 * time.Hour)
	res := NewProcess(t, false, "create", "--title", "Early").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	sb.Advance(48 * time.Hour)
	res = NewProcess(t, false, "create", "--title", "Late").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// First, get the actual timestamps from the list output to compute a midpoint.
	fullRes := NewProcess(t, false, "list", "-f", "%i\t%c").Run(sb.Context(), sb.Runtime())
	require.NoError(t, fullRes.Err)
	// Parse the created timestamp of node 1 ("Early") to use as a boundary.
	var earlyCreated, lateCreated time.Time
	for _, line := range strings.Split(strings.TrimSpace(string(fullRes.Stdout)), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "\t", 2)
		if len(parts) < 2 {
			continue
		}
		ts, parseErr := time.Parse(time.RFC3339, parts[1])
		if parseErr != nil {
			continue
		}
		switch parts[0] {
		case "1":
			earlyCreated = ts
		case "2":
			lateCreated = ts
		}
	}
	require.False(t, earlyCreated.IsZero(), "should have parsed early created time")
	require.False(t, lateCreated.IsZero(), "should have parsed late created time")

	// Compute a midpoint date between the two nodes.
	midpoint := earlyCreated.Add(lateCreated.Sub(earlyCreated) / 2)
	queryDate := midpoint.Format("2006-01-02T15:04:05Z")

	listRes := NewProcess(t, false, "list", "--id-only", "--query", fmt.Sprintf(".created>%s", queryDate)).Run(sb.Context(), sb.Runtime())
	require.NoError(t, listRes.Err)
	lines := strings.Split(strings.TrimSpace(string(listRes.Stdout)), "\n")
	trimmed := make([]string, len(lines))
	for i, l := range lines {
		trimmed[i] = strings.TrimSpace(l)
	}
	require.Contains(t, trimmed, "2", "late node should match .created> query")
	require.NotContains(t, trimmed, "0", "zero node should not match .created> query")
}

func TestListCommand_DotPrefixQuery_CombinedWithAttribute(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// The zero node exists from the fixture. Create an additional node and
	// query with a stats field combined with a tag.
	sb.Advance(1 * time.Hour)
	res := NewProcess(t, false, "create", "--title", "Tagged", "--tags", "golang").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	sb.Advance(1 * time.Hour)
	res = NewProcess(t, false, "create", "--title", "Untagged").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// ".created and golang" should return only the tagged node.
	listRes := NewProcess(t, false, "list", "--id-only", "--query", ".created and golang").Run(sb.Context(), sb.Runtime())
	require.NoError(t, listRes.Err)
	lines := strings.Split(strings.TrimSpace(string(listRes.Stdout)), "\n")
	trimmed := make([]string, len(lines))
	for i, l := range lines {
		trimmed[i] = strings.TrimSpace(l)
	}
	require.Contains(t, trimmed, "1", "tagged node should match combined query")
	require.NotContains(t, trimmed, "2", "untagged node should not match combined query")
}

func TestListCommand_DotPrefixQuery_BooleanCheck(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	sb.Advance(1 * time.Hour)
	res := NewProcess(t, false, "create", "--title", "A").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	sb.Advance(1 * time.Hour)
	res = NewProcess(t, false, "create", "--title", "B").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// ".created" without operator is a boolean check: all nodes with a
	// non-zero created timestamp should match. The fixture zero node may
	// have a zero created time if its dex entry lacks it, so we only
	// check that at least the newly created nodes appear.
	listRes := NewProcess(t, false, "list", "--id-only", "--query", ".created").Run(sb.Context(), sb.Runtime())
	require.NoError(t, listRes.Err)
	out := strings.TrimSpace(string(listRes.Stdout))
	require.NotEmpty(t, out)

	lines := strings.Split(out, "\n")
	trimmed := make([]string, len(lines))
	for i, l := range lines {
		trimmed[i] = strings.TrimSpace(l)
	}
	require.Contains(t, trimmed, "1", "newly created node 1 should match .created boolean")
	require.Contains(t, trimmed, "2", "newly created node 2 should match .created boolean")
}
