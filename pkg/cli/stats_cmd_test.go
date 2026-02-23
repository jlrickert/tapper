package cli_test

import (
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestStatsCommand_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		fixture     *string
		expectedErr string
	}{
		{
			name:        "missing_node_id",
			args:        []string{"stats"},
			expectedErr: "accepts 1 arg",
		},
		{
			name:        "invalid_node_id",
			args:        []string{"stats", "abc"},
			fixture:     strPtr("joe"),
			expectedErr: "invalid node ID",
		},
		{
			name:        "missing_alias",
			args:        []string{"stats", "0", "--keg", "missing"},
			fixture:     strPtr("joe"),
			expectedErr: "keg alias not found",
		},
		{
			name:        "missing_node",
			args:        []string{"stats", "424242", "--keg", "personal"},
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

func TestStatsCommand_WithJoeFixture(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "stats", "0", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	require.Contains(t, out, "hash:")
	require.Contains(t, out, "updated:")
}
