package cli_test

import (
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// nodeCompletionCase describes a single node-ID completion test scenario.
type nodeCompletionCase struct {
	name        string
	words       []string // args to __complete after the command
	wantAll     bool     // expect all node IDs from the keg
	wantContain []string // IDs that must appear in suggestions
	wantAbsent  []string // IDs that must NOT appear
	wantEmpty   bool     // expect empty suggestion list
}

// runNodeCompletionCases runs table-driven node-ID completion tests using the
// joe fixture with the personal keg as default (nodes 0, 1, 2, 3).
func runNodeCompletionCases(t *testing.T, cases []nodeCompletionCase) {
	t.Helper()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

			comp := NewCompletionProcess(t, false, 0, tc.words...).Run(sb.Context(), sb.Runtime())
			require.NoError(t, comp.Err)
			suggestions := parseCompletionSuggestions(string(comp.Stdout))

			if tc.wantEmpty {
				require.Empty(t, suggestions)
				return
			}
			if tc.wantAll {
				require.ElementsMatch(t, []string{"0", "1", "2", "3"}, suggestions)
				return
			}
			for _, id := range tc.wantContain {
				require.Contains(t, suggestions, id)
			}
			for _, id := range tc.wantAbsent {
				require.NotContains(t, suggestions, id)
			}
		})
	}
}

func TestNodeCompletion_Cat(t *testing.T) {
	t.Parallel()
	runNodeCompletionCases(t, []nodeCompletionCase{
		{
			name:    "lists_all_ids",
			words:   []string{"cat", "--keg", "personal", ""},
			wantAll: true,
		},
		{
			name:        "prefix_filter",
			words:       []string{"cat", "--keg", "personal", "1"},
			wantContain: []string{"1"},
			wantAbsent:  []string{"0", "2", "3"},
		},
		{
			// cat has unlimited args: still offers completions after first ID
			name:        "offers_completions_after_first_arg",
			words:       []string{"cat", "--keg", "personal", "1", ""},
			wantContain: []string{"0", "2", "3"},
		},
	})
}

func TestNodeCompletion_Edit(t *testing.T) {
	t.Parallel()
	runNodeCompletionCases(t, []nodeCompletionCase{
		{
			name:    "lists_all_ids",
			words:   []string{"edit", "--keg", "personal", ""},
			wantAll: true,
		},
		{
			// edit takes exactly 1 arg: no completions after first
			name:      "stops_after_one_arg",
			words:     []string{"edit", "--keg", "personal", "1", ""},
			wantEmpty: true,
		},
	})
}

func TestNodeCompletion_Backlinks(t *testing.T) {
	t.Parallel()
	runNodeCompletionCases(t, []nodeCompletionCase{
		{
			name:    "lists_all_ids",
			words:   []string{"backlinks", "--keg", "personal", ""},
			wantAll: true,
		},
		{
			name:      "stops_after_one_arg",
			words:     []string{"backlinks", "--keg", "personal", "1", ""},
			wantEmpty: true,
		},
	})
}

func TestNodeCompletion_Meta(t *testing.T) {
	t.Parallel()
	runNodeCompletionCases(t, []nodeCompletionCase{
		{
			name:    "lists_all_ids",
			words:   []string{"meta", "--keg", "personal", ""},
			wantAll: true,
		},
		{
			name:      "stops_after_one_arg",
			words:     []string{"meta", "--keg", "personal", "1", ""},
			wantEmpty: true,
		},
	})
}

func TestNodeCompletion_Stats(t *testing.T) {
	t.Parallel()
	runNodeCompletionCases(t, []nodeCompletionCase{
		{
			name:    "lists_all_ids",
			words:   []string{"stats", "--keg", "personal", ""},
			wantAll: true,
		},
		{
			name:      "stops_after_one_arg",
			words:     []string{"stats", "--keg", "personal", "1", ""},
			wantEmpty: true,
		},
	})
}

func TestNodeCompletion_Rm(t *testing.T) {
	t.Parallel()
	runNodeCompletionCases(t, []nodeCompletionCase{
		{
			name:    "lists_all_ids",
			words:   []string{"rm", "--keg", "personal", ""},
			wantAll: true,
		},
		{
			// rm accepts multiple IDs: still offers completions after first
			name:        "offers_completions_after_first_arg",
			words:       []string{"rm", "--keg", "personal", "1", ""},
			wantContain: []string{"0", "2", "3"},
		},
	})
}

func TestNodeCompletion_Mv(t *testing.T) {
	t.Parallel()
	runNodeCompletionCases(t, []nodeCompletionCase{
		{
			name:    "lists_all_ids_for_src",
			words:   []string{"mv", "--keg", "personal", ""},
			wantAll: true,
		},
		{
			name:        "lists_all_ids_for_dst",
			words:       []string{"mv", "--keg", "personal", "1", ""},
			wantContain: []string{"0", "2", "3"},
		},
		{
			// mv takes exactly 2 args: no completions after second
			name:      "stops_after_two_args",
			words:     []string{"mv", "--keg", "personal", "1", "2", ""},
			wantEmpty: true,
		},
	})
}

// TestNodeCompletion_RespectsKegFlag verifies that completions for a named
// alias return that keg's node IDs, not the default keg's.
func TestNodeCompletion_RespectsKegFlag(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Default keg for joe is "personal" (nodes 0-3).
	// "work" keg has only node 0.
	comp := NewCompletionProcess(t, false, 0, "cat", "--keg", "work", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "0")
	require.NotContains(t, suggestions, "1", "work keg only has node 0")
	require.NotContains(t, suggestions, "2", "work keg only has node 0")
}

// TestNodeCompletion_EmptyKeg verifies that completing against a keg with only
// node 0 returns exactly ["0"].
func TestNodeCompletion_EmptyKeg(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	comp := NewCompletionProcess(t, false, 0, "cat", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Equal(t, []string{"0"}, suggestions)
}
