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
			expectedErr: "keg alias not found",
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

func createNodeWithBodyFromStdin(t *testing.T, sb *testutils.Sandbox, content string) string {
	t.Helper()

	res := NewProcess(t, true, "create").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(content))
	require.NoError(t, res.Err)
	return strings.TrimSpace(string(res.Stdout))
}
