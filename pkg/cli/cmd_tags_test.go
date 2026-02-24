package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestTagsCommand_TableDrivenErrors(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		fixture     *string
		expectedErr string
	}{
		{
			name:        "too_many_args",
			args:        []string{"tags", "a", "b"},
			expectedErr: "accepts at most 1 arg",
		},
		{
			name:        "missing_alias",
			args:        []string{"tags", "--keg", "missing"},
			fixture:     strPtr("joe"),
			expectedErr: "keg alias not found",
		},
		{
			name:        "invalid_expression",
			args:        []string{"tags", "a and (b", "--keg", "personal"},
			fixture:     strPtr("joe"),
			expectedErr: "invalid tag expression",
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

func TestTagsCommand_ListAllTagsSorted(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "create", "--title", "One", "--tags", "zeta").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewProcess(t, false, "create", "--title", "Two", "--tags", "alpha").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	res = NewProcess(t, false, "create", "--title", "Three", "--tags", "beta").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := NewProcess(t, false, "tags").Run(sb.Context(), sb.Runtime())
	require.NoError(t, out.Err)
	require.Equal(t, "alpha\nbeta\nzeta", strings.TrimSpace(string(out.Stdout)))

	reverseOut := NewProcess(t, false, "tags", "--reverse").Run(sb.Context(), sb.Runtime())
	require.NoError(t, reverseOut.Err)
	require.Equal(t, "zeta\nbeta\nalpha", strings.TrimSpace(string(reverseOut.Stdout)))
}

func TestTagsCommand_ListNodesForTag(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "create", "--title", "Alpha Node", "--tags", "fire").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "1", strings.TrimSpace(string(res.Stdout)))

	res = NewProcess(t, false, "create", "--title", "Beta Node", "--tags", "fire", "--tags", "earth").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "2", strings.TrimSpace(string(res.Stdout)))

	idOnly := NewProcess(t, false, "tags", "fire", "--id-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, idOnly.Err)
	require.Equal(t, "1\n2", strings.TrimSpace(string(idOnly.Stdout)))

	formatted := NewProcess(t, false, "tags", "fire", "--format", "%i|%t").Run(sb.Context(), sb.Runtime())
	require.NoError(t, formatted.Err)
	require.Equal(t, "1|Alpha Node\n2|Beta Node", strings.TrimSpace(string(formatted.Stdout)))

	reverse := NewProcess(t, false, "tags", "fire", "--id-only", "--reverse").Run(sb.Context(), sb.Runtime())
	require.NoError(t, reverse.Err)
	require.Equal(t, "2\n1", strings.TrimSpace(string(reverse.Stdout)))
}

func TestTagsCommand_TagExpression(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	res := NewProcess(t, false, "create", "--title", "Node AB", "--tags", "a", "--tags", "b").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "1", strings.TrimSpace(string(res.Stdout)))

	res = NewProcess(t, false, "create", "--title", "Node AC", "--tags", "a", "--tags", "c").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "2", strings.TrimSpace(string(res.Stdout)))

	res = NewProcess(t, false, "create", "--title", "Node C", "--tags", "c").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "3", strings.TrimSpace(string(res.Stdout)))

	orExpr := NewProcess(t, false, "tags", "a and (b or c)", "--id-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, orExpr.Err)
	require.Equal(t, "1\n2", strings.TrimSpace(string(orExpr.Stdout)))

	notExpr := NewProcess(t, false, "tags", "a and not c", "--id-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, notExpr.Err)
	require.Equal(t, "1", strings.TrimSpace(string(notExpr.Stdout)))

	symbolExpr := NewProcess(t, false, "tags", "a && !c", "--id-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, symbolExpr.Err)
	require.Equal(t, "1", strings.TrimSpace(string(symbolExpr.Stdout)))
}

func TestTagsCommand_NoMatchesReturnsEmptyOutput(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "tags", "missing-tag", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "", strings.TrimSpace(string(res.Stdout)))
}
