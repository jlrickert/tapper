package cli_test

import (
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestIndexCommand_ErrorHandling(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		setupFixture *string
		expectedErr  string
		description  string
	}{
		{
			name:         "index_list_nonexistent_alias",
			args:         []string{"index", "list", "--alias", "nonexistent"},
			setupFixture: strPtr("joe"),
			expectedErr:  "keg alias not found",
			description:  "Error when keg alias does not exist",
		},
		{
			name:        "index_list_no_keg_configured",
			args:        []string{"index", "list"},
			expectedErr: "no keg configured",
			description: "Error when no keg is configured",
		},
		{
			name:         "index_get_unknown_index",
			args:         []string{"index", "get", "--alias", "example", "does-not-exist.md"},
			setupFixture: strPtr("testuser"),
			expectedErr:  "not found",
			description:  "Error when named index does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(innerT *testing.T) {
			innerT.Parallel()
			var opts []testutils.Option
			if tt.setupFixture != nil {
				opts = append(opts, testutils.WithFixture(*tt.setupFixture, "~"))
			}
			sb := NewSandbox(innerT, opts...)

			h := NewProcess(innerT, false, tt.args...)
			res := h.Run(sb.Context(), sb.Runtime())

			require.Error(innerT, res.Err, "expected error - %s", tt.description)
			stderr := string(res.Stderr)
			require.Contains(innerT, stderr, tt.expectedErr,
				"error message should contain %q, got stderr: %s and err: %v", tt.expectedErr, stderr, res.Err)
		})
	}
}

func TestIndexListCommand_ListIndexes(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Ensure dex artifacts exist first
	rebuild := NewProcess(t, false, "index", "rebuild", "--alias", "example")
	res := rebuild.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	h := NewProcess(t, false, "index", "list", "--alias", "example")
	res = h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "index list should succeed")

	stdout := string(res.Stdout)
	require.Contains(t, stdout, "nodes.tsv")
	require.Contains(t, stdout, "tags")
}

func TestIndexGetCommand_CatNamedIndex(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Ensure dex artifacts exist first
	rebuild := NewProcess(t, false, "index", "rebuild", "--alias", "example")
	res := rebuild.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	h := NewProcess(t, false, "index", "get", "--alias", "example", "nodes.tsv")
	res = h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "index get should succeed")

	stdout := string(res.Stdout)
	require.NotEmpty(t, stdout, "nodes.tsv should have content")
}

func TestIndexGetCommand_CompletionSuggestsIndexNames(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Ensure dex artifacts exist
	rebuild := NewProcess(t, false, "index", "rebuild", "--alias", "example")
	res := rebuild.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	comp := NewCompletionProcess(t, false, 0, "index", "get", "--alias", "example", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "nodes.tsv")
	require.Contains(t, suggestions, "tags")
}
