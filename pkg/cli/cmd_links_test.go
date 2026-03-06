package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestLinksCommand_TableDrivenErrors(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		fixture     *string
		expectedErr string
	}{
		{
			name:        "missing_node_id",
			args:        []string{"links"},
			expectedErr: "accepts 1 arg",
		},
		{
			name:        "invalid_node_id",
			args:        []string{"links", "abc"},
			fixture:     strPtr("joe"),
			expectedErr: "invalid node ID",
		},
		{
			name:        "missing_alias",
			args:        []string{"links", "0", "--keg", "missing"},
			fixture:     strPtr("joe"),
			expectedErr: "keg alias not found",
		},
		{
			name:        "missing_node",
			args:        []string{"links", "424242", "--keg", "personal"},
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

func TestLinksCommand_ListsOutgoingLinks(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Create target nodes first (no links).
	targetA := NewProcess(t, false, "create", "--title", "Target A").Run(sb.Context(), sb.Runtime())
	require.NoError(t, targetA.Err)
	require.Equal(t, "1", strings.TrimSpace(string(targetA.Stdout)))

	targetB := NewProcess(t, false, "create", "--title", "Target B").Run(sb.Context(), sb.Runtime())
	require.NoError(t, targetB.Err)
	require.Equal(t, "2", strings.TrimSpace(string(targetB.Stdout)))

	// Create a source node that links to both targets.
	source := NewProcess(t, true, "create").RunWithIO(
		sb.Context(),
		sb.Runtime(),
		strings.NewReader("# Source\n\nLinks to [A](../1) and [B](../2).\n"),
	)
	require.NoError(t, source.Err)
	require.Equal(t, "3", strings.TrimSpace(string(source.Stdout)))

	// Reindex so the links index is populated.
	reindex := NewProcess(t, false, "index", "rebuild", "--full").Run(sb.Context(), sb.Runtime())
	require.NoError(t, reindex.Err)

	idOnly := NewProcess(t, false, "links", "3", "--id-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, idOnly.Err)
	require.Equal(t, "1\n2", strings.TrimSpace(string(idOnly.Stdout)))

	reversed := NewProcess(t, false, "links", "3", "--id-only", "--reverse").Run(sb.Context(), sb.Runtime())
	require.NoError(t, reversed.Err)
	require.Equal(t, "2\n1", strings.TrimSpace(string(reversed.Stdout)))

	formatted := NewProcess(t, false, "links", "3", "--format", "%i|%t").Run(sb.Context(), sb.Runtime())
	require.NoError(t, formatted.Err)
	out := strings.TrimSpace(string(formatted.Stdout))
	require.Contains(t, out, "1|Target A")
	require.Contains(t, out, "2|Target B")
}

func TestLinksCommand_NoLinksReturnsEmptyOutput(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "links", "0", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "", strings.TrimSpace(string(res.Stdout)))
}
