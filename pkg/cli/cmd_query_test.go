package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// TestQuery_AttrPredicate_Tags verifies that --query on the tags command
// supports key=value attribute predicates from meta.yaml.
//
// Fixture state (joe/kegs/personal):
//   node 0 - meta: {tags: [planned]}
//   node 1 - meta: {entity: trick, tags: [planned]}
//   node 2 - meta: {entity: concept}
//   node 3 - meta: {}
func TestQuery_AttrPredicate_Tags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		query    string
		wantIDs  []string
		wantNone bool
	}{
		{
			name:    "attr_exact_match",
			query:   "entity=trick",
			wantIDs: []string{"1"},
		},
		{
			name:    "attr_and_tag",
			query:   "entity=trick and planned",
			wantIDs: []string{"1"},
		},
		{
			name:    "attr_or_attr",
			query:   "entity=trick or entity=concept",
			wantIDs: []string{"1", "2"},
		},
		{
			name:     "attr_no_match",
			query:    "entity=plan",
			wantNone: true,
		},
		{
			name:    "plain_tag_still_works",
			query:   "planned",
			wantIDs: []string{"0", "1"},
		},
		{
			name:    "not_attr",
			query:   "not entity=trick",
			wantIDs: []string{"0", "2", "3"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

			res := NewProcess(t, false, "tags", "--keg", "personal", "--query", tc.query, "--id-only").
				Run(sb.Context(), sb.Runtime())
			require.NoError(t, res.Err)

			output := strings.TrimSpace(string(res.Stdout))
			if tc.wantNone {
				require.Empty(t, output)
				return
			}

			ids := strings.Split(output, "\n")
			require.ElementsMatch(t, tc.wantIDs, ids)
		})
	}
}

// TestQuery_AttrPredicate_List verifies that --query on the list command
// filters nodes by key=value attribute predicates.
func TestQuery_AttrPredicate_List(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "list", "--keg", "personal", "--query", "entity=concept", "--id-only").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	output := strings.TrimSpace(string(res.Stdout))
	require.Equal(t, "2", output)
}

// TestQuery_AttrPredicate_Cat verifies that --query on the cat command
// selects nodes by attribute predicate.
func TestQuery_AttrPredicate_Cat(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "cat", "--keg", "personal", "--query", "entity=concept").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	output := string(res.Stdout)
	// Should contain the content of node 2 (Project Alpha)
	require.Contains(t, output, "Project Alpha")
}

// TestQuery_InvalidExpression verifies that an invalid --query expression
// returns an error with a descriptive message.
func TestQuery_InvalidExpression(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "tags", "--keg", "personal", "--query", "a and (b").
		Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "invalid query expression")
}
