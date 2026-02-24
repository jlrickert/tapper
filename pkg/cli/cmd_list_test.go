package cli_test

import (
	"regexp"
	"strings"
	"testing"

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
