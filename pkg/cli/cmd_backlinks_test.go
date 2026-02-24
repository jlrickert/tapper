package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestBacklinksCommand_TableDrivenErrors(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		fixture     *string
		expectedErr string
	}{
		{
			name:        "missing_node_id",
			args:        []string{"backlinks"},
			expectedErr: "accepts 1 arg",
		},
		{
			name:        "invalid_node_id",
			args:        []string{"backlinks", "abc"},
			fixture:     strPtr("joe"),
			expectedErr: "invalid node ID",
		},
		{
			name:        "missing_alias",
			args:        []string{"backlinks", "0", "--keg", "missing"},
			fixture:     strPtr("joe"),
			expectedErr: "keg alias not found",
		},
		{
			name:        "missing_node",
			args:        []string{"backlinks", "424242", "--keg", "personal"},
			fixture:     strPtr("joe"),
			expectedErr: "node 424242 not found",
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

func TestBacklinksCommand_ListsBacklinkSources(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	targetCreate := NewProcess(t, false, "create", "--title", "Target").Run(sb.Context(), sb.Runtime())
	require.NoError(t, targetCreate.Err)
	require.Equal(t, "1", strings.TrimSpace(string(targetCreate.Stdout)))

	createWithLinkToTarget(t, sb, "# Source Two\n\nSee [target](../1).\n")
	createWithLinkToTarget(t, sb, "# Source Three\n\nAnother link to [target](../1).\n")

	idOnly := NewProcess(t, false, "backlinks", "1", "--id-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, idOnly.Err)
	require.Equal(t, "2\n3", strings.TrimSpace(string(idOnly.Stdout)))

	reversed := NewProcess(t, false, "backlinks", "1", "--id-only", "--reverse").Run(sb.Context(), sb.Runtime())
	require.NoError(t, reversed.Err)
	require.Equal(t, "3\n2", strings.TrimSpace(string(reversed.Stdout)))

	formatted := NewProcess(t, false, "backlinks", "1", "--format", "%i|%t").Run(sb.Context(), sb.Runtime())
	require.NoError(t, formatted.Err)
	out := strings.TrimSpace(string(formatted.Stdout))
	require.Contains(t, out, "2|Source Two")
	require.Contains(t, out, "3|Source Three")
}

func TestBacklinksCommand_NoBacklinksReturnsEmptyOutput(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "backlinks", "0", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "", strings.TrimSpace(string(res.Stdout)))
}

func createWithLinkToTarget(t *testing.T, sb *testutils.Sandbox, content string) {
	t.Helper()

	res := NewProcess(t, true, "create").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(content))
	require.NoError(t, res.Err)
}
