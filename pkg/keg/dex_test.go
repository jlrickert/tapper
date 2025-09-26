package keg

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadFromDex_Table(t *testing.T) {
	cases := []struct {
		name          string
		nodesTSV      string
		tagsData      string
		linksData     string
		backlinksData string

		wantNodesCount int
		wantNodes      map[int]string

		wantTags      map[string][]int
		wantLinks     map[int][]int
		wantBacklinks map[int][]int
	}{
		{
			name: "basic",
			nodesTSV: "" +
				"0\t2025-08-04 22:03:53Z\tSorry, planned but not yet available\n" +
				"1\t2025-08-04 23:06:30Z\tConfiguration (config)\n" +
				"3\t2025-08-09 17:44:04Z\tZeke AI utility (zeke)\n" +
				"badline-without-tabs\n" + // malformed - should be skipped
				"999\tnot-a-time\tTitle with bad time\n", // id parses, time parse will produce zero time
			tagsData: "" +
				"zeke\t3 10 45\n" +
				"keg\t5 10 15 42\n" +
				"emptytag\n", // tag present with empty members
			linksData: "" +
				"1\t3 5\n" +
				"10\t\n" + // explicit empty destinations
				"bad\t3 4\n", // invalid source id -> skipped
			backlinksData: "" +
				"3\t1 2\n" +
				"42\t3 7\n" +
				"15\t\n", // empty sources

			wantNodesCount: 4, // 0,1,3,999

			wantNodes: map[int]string{
				0:   "Sorry, planned but not yet available",
				1:   "Configuration (config)",
				3:   "Zeke AI utility (zeke)",
				999: "Title with bad time",
			},

			// Note: indices that contain empty member lists may be omitted by parsers.
			// Tests should not assume empty-member entries are always present.
			wantTags: map[string][]int{
				"zeke": {3, 10, 45},
			},
			wantLinks: map[int][]int{
				1: {3, 5},
			},
			wantBacklinks: map[int][]int{
				3: {1, 2},
			},
		},
		{
			name:           "empty_indexes",
			nodesTSV:       "",
			tagsData:       "",
			linksData:      "",
			backlinksData:  "",
			wantNodesCount: 0,
			wantNodes:      map[int]string{},
			wantTags:       map[string][]int{},
			wantLinks:      map[int][]int{},
			wantBacklinks:  map[int][]int{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			t.Helper()

			mem := NewMemoryRepo()

			// write indexes only if non-empty (tests may want to omit them)
			if tc.nodesTSV != "" {
				require.NoError(t, mem.WriteIndex(t.Context(), "nodes.tsv", []byte(tc.nodesTSV)))
			}
			if tc.tagsData != "" {
				require.NoError(t, mem.WriteIndex(t.Context(), "tags", []byte(tc.tagsData)))
			}
			if tc.linksData != "" {
				require.NoError(t, mem.WriteIndex(t.Context(), "links", []byte(tc.linksData)))
			}
			if tc.backlinksData != "" {
				require.NoError(t, mem.WriteIndex(t.Context(), "backlinks", []byte(tc.backlinksData)))
			}

			// Read indexes using the Dex convenience wrapper
			dex, err := NewDexFromRepo(t.Context(), mem)
			require.NoError(t, err)

			// Nodes count
			require.Len(t, dex.Nodes(t.Context()), tc.wantNodesCount)

			// helper to lookup a NodeRef by id via Dex
			find := func(id Node) *NodeIndexEntry {
				return dex.GetRef(t.Context(), id)
			}

			// verify expected nodes and titles
			for id, wantTitle := range tc.wantNodes {
				n := find(Node{ID: id})
				require.NotNil(t, n, "node %d missing", int(id))
				require.Equal(t, wantTitle, n.Title, "node %d title mismatch", int(id))
			}

			// Validate tags by parsing the raw tags index directly from the repo
			gotTags := map[string][]int{}
			if tc.tagsData != "" {
				data, err := mem.GetIndex(t.Context(), "tags")
				require.NoError(t, err)
				parsed, err := ParseTagIndex(t.Context(), data)
				require.NoError(t, err)
				// parsed has unexported internal structure; tests are in-package so access directly
				for tag, nodes := range parsed.data {
					ids := make([]int, 0, len(nodes))
					for _, n := range nodes {
						ids = append(ids, n.ID)
					}
					gotTags[tag] = ids
				}
			}

			// Only ensure expected tags are present with expected members.
			for wantTag, wantIDs := range tc.wantTags {
				gotIDs, ok := gotTags[wantTag]
				require.True(t, ok, "expected tag %q missing", wantTag)
				require.Equal(t, wantIDs, gotIDs, "tag %q mismatch", wantTag)
			}

			// Validate links by querying Dex per source node
			for wantSrc, wantDsts := range tc.wantLinks {
				gotNodes, ok := dex.Links(t.Context(), Node{ID: wantSrc})
				require.True(t, ok, "expected links src %d missing", int(wantSrc))
				gotDsts := make([]int, 0, len(gotNodes))
				for _, n := range gotNodes {
					gotDsts = append(gotDsts, n.ID)
				}
				require.Equal(t, wantDsts, gotDsts, "links for %d mismatch", int(wantSrc))
			}

			// Validate backlinks by querying Dex per destination node
			for wantDst, wantSrcs := range tc.wantBacklinks {
				gotNodes, ok := dex.Backlinks(t.Context(), Node{ID: wantDst})
				require.True(t, ok, "expected backlinks dst %d missing", int(wantDst))
				gotSrcs := make([]int, 0, len(gotNodes))
				for _, n := range gotNodes {
					gotSrcs = append(gotSrcs, n.ID)
				}
				require.Equal(t, wantSrcs, gotSrcs, "backlinks for %d mismatch", int(wantDst))
			}
		})
	}
}
