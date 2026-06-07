package keg

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseNodeRef(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		want    NodeRef
		wantErr bool
	}{
		{"local", "42", NodeRef{Form: RefLocal, Node: NodeId{ID: 42}}, false},
		{"local_code", "42-0001", NodeRef{Form: RefLocal, Node: NodeId{ID: 42, Code: "0001"}}, false},
		{"zero", "0", NodeRef{Form: RefLocal, Node: NodeId{ID: 0}}, false},
		{"alias", "keg:work/23", NodeRef{Form: RefAlias, Alias: "work", Node: NodeId{ID: 23, Alias: "work"}}, false},
		{"alias_code", "keg:work/23-0042", NodeRef{Form: RefAlias, Alias: "work", Node: NodeId{ID: 23, Code: "0042", Alias: "work"}}, false},
		{"qualified", "keg:@local/work/3", NodeRef{Form: RefQualified, Namespace: "local", KegName: "work", Node: NodeId{ID: 3}}, false},
		{"qualified_code", "keg:@acme/notes/7-0001", NodeRef{Form: RefQualified, Namespace: "acme", KegName: "notes", Node: NodeId{ID: 7, Code: "0001"}}, false},

		// errors
		{"empty", "", NodeRef{}, true},
		{"leading_zeros", "0023", NodeRef{}, true},
		{"bad_code", "42-1", NodeRef{}, true},
		{"alias_uppercase", "keg:Work/23", NodeRef{}, true},
		{"alias_empty", "keg:/23", NodeRef{}, true},
		{"qualified_flights_d", "keg:@flights.d/work/1", NodeRef{}, true},
		{"qualified_dotted_ns", "keg:@a.b/work/1", NodeRef{}, true},
		{"qualified_bad_keg", "keg:@local/Work/1", NodeRef{}, true},
		{"qualified_missing_seg", "keg:@local/3", NodeRef{}, true},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseNodeRef(c.in)
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.want, *got)
			// Round-trip: parse(String) == ref.
			again, err := ParseNodeRef(got.String())
			require.NoError(t, err)
			require.Equal(t, *got, *again, "round-trip mismatch for %q -> %q", c.in, got.String())
		})
	}
}

// TestParseNode_RejectsQualified confirms the legacy ParseNode does not silently
// mis-parse a qualified reference (callers must migrate to ParseNodeRef).
func TestParseNode_RejectsQualified(t *testing.T) {
	t.Parallel()
	_, err := ParseNode("keg:@local/work/3")
	require.Error(t, err)
}
