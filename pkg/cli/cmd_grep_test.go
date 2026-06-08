package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestGrepCommand_TableDrivenErrors(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		fixture     *string
		expectedErr string
	}{
		{
			name:        "missing_query",
			args:        []string{"grep"},
			expectedErr: "accepts 1 arg",
		},
		{
			name:        "invalid_regex",
			args:        []string{"grep", "["},
			fixture:     strPtr("joe"),
			expectedErr: "invalid query regex",
		},
		{
			name:        "missing_alias",
			args:        []string{"grep", "anything", "--keg", "missing"},
			fixture:     strPtr("joe"),
			expectedErr: "keg not initialized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(innerT *testing.T) {
			innerT.Parallel()
			var opts []testutils.Option
			if tt.fixture != nil {
				opts = append(opts, testutils.WithFixture(*tt.fixture, "~"))
			}
			sb := NewSandbox(innerT, opts...)

			res := NewProcess(innerT, false, tt.args...).Run(sb.Context(), sb.Runtime())

			require.Error(innerT, res.Err)
			require.Contains(innerT, string(res.Stderr), tt.expectedErr)
		})
	}
}

func TestGrepCommand_DefaultOutputShowsMatchingLinesByNode(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	firstID := createNodeWithBodyFromStdin(
		t,
		sb,
		"# Alpha\n\nfire one\nnothing\nsecond fire line\n",
	)
	require.Equal(t, "1", firstID)

	secondID := createNodeWithBodyFromStdin(
		t,
		sb,
		"# Beta\n\nnone\nwildfire item\n",
	)
	require.Equal(t, "2", secondID)

	res := NewProcess(t, false, "grep", "fire").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	expected := strings.Join([]string{
		"1 Alpha",
		"3:fire one",
		"5:second fire line",
		"",
		"2 Beta",
		"4:wildfire item",
	}, "\n")
	require.Equal(t, expected, strings.TrimSpace(string(res.Stdout)))
}

func TestGrepCommand_IgnoreCaseIdOnlyReverseAndFormat(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	createNodeWithBodyFromStdin(t, sb, "# Alpha\n\nfire one\n")
	createNodeWithBodyFromStdin(t, sb, "# Beta\n\nwildfire item\n")

	idOnly := NewProcess(t, false, "grep", "FIRE", "--ignore-case", "--id-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, idOnly.Err)
	require.Equal(t, "1\n2", strings.TrimSpace(string(idOnly.Stdout)))

	reversed := NewProcess(t, false, "grep", "FIRE", "--ignore-case", "--id-only", "--reverse").Run(sb.Context(), sb.Runtime())
	require.NoError(t, reversed.Err)
	require.Equal(t, "2\n1", strings.TrimSpace(string(reversed.Stdout)))

	formatted := NewProcess(t, false, "grep", "FIRE", "--ignore-case", "--format", "%i|%t").Run(sb.Context(), sb.Runtime())
	require.NoError(t, formatted.Err)
	require.Equal(t, "1|Alpha\n2|Beta", strings.TrimSpace(string(formatted.Stdout)))
}

func TestGrepCommand_NoMatchesReturnsEmptyOutput(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "grep", "not-found-token-zzzx", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "", strings.TrimSpace(string(res.Stdout)))
}

func TestGrepCommand_OffsetSkipsMatchingNodes(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	createNodeWithBodyFromStdin(t, sb, "# Alpha\n\nfire one\n")
	createNodeWithBodyFromStdin(t, sb, "# Beta\n\nwildfire item\n")
	createNodeWithBodyFromStdin(t, sb, "# Gamma\n\nfire three\n")

	// Without offset: all three match "fire" (nodes 1,2,3).
	all := NewProcess(t, false, "grep", "fire", "--id-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, all.Err)
	require.Equal(t, "1\n2\n3", strings.TrimSpace(string(all.Stdout)))

	// Offset 1: skip first match.
	offset := NewProcess(t, false, "grep", "fire", "--id-only", "--offset", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, offset.Err)
	require.Equal(t, "2\n3", strings.TrimSpace(string(offset.Stdout)))

	// Offset 1 skips node 1, leaving (2,3). Limit 2 takes first 2: (2,3).
	combined := NewProcess(t, false, "grep", "fire", "--id-only", "-n", "2", "--offset", "1").Run(sb.Context(), sb.Runtime())
	require.NoError(t, combined.Err)
	require.Equal(t, "2\n3", strings.TrimSpace(string(combined.Stdout)))
}

func TestGrepCommand_MaxLinesTruncatesPerNode(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	createNodeWithBodyFromStdin(
		t,
		sb,
		"# Alpha\n\nfire one\nfire two\nfire three\nfire four\n",
	)

	// Without --max-lines: all 4 lines match.
	all := NewProcess(t, false, "grep", "fire").Run(sb.Context(), sb.Runtime())
	require.NoError(t, all.Err)
	lines := strings.Split(strings.TrimSpace(string(all.Stdout)), "\n")
	// Header + 4 match lines = 5 lines total.
	require.Equal(t, 5, len(lines))

	// With --max-lines 2: only first 2 match lines per node.
	truncated := NewProcess(t, false, "grep", "fire", "--max-lines", "2").Run(sb.Context(), sb.Runtime())
	require.NoError(t, truncated.Err)
	tlines := strings.Split(strings.TrimSpace(string(truncated.Stdout)), "\n")
	// Header + 2 match lines = 3 lines total.
	require.Equal(t, 3, len(tlines))
	require.Contains(t, tlines[1], "fire one")
	require.Contains(t, tlines[2], "fire two")
}

func createNodeWithBodyFromStdin(t *testing.T, sb *testutils.Sandbox, content string) string {
	t.Helper()

	res := NewProcess(t, true, "create").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(content))
	require.NoError(t, res.Err)
	return strings.TrimSpace(string(res.Stdout))
}
