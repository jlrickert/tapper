package keg

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReadFromDex_Table(t *testing.T) {
	cases := []struct {
		name             string
		nodesTSV         string
		tagsData         string
		linksData        string
		backlinksData    string
		wantNodesCount   int
		wantNodes        map[int]string
		wantNodesUpdated map[int]string

		wantTags      map[string][]int
		wantLinks     map[int][]int
		wantBacklinks map[int][]int
	}{
		{
			name: "basic",
			nodesTSV: "" +
				"0\t2025-08-04T22:03:53Z\tSorry, planned but not yet available\n" +
				"1\t2025-08-04T23:06:30Z\tConfiguration (config)\n" +
				"3\t2025-08-09T17:44:04Z\tZeke AI utility (zeke)\n" +
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

			// Expected updated timestamps as they appear in the index. When the
			// value is not parseable we expect a zero time.
			wantNodesUpdated: map[int]string{
				0:   "2025-08-04T22:03:53Z",
				1:   "2025-08-04T23:06:30Z",
				3:   "2025-08-09T17:44:04Z",
				999: "not-a-time",
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
			name:             "empty_indexes",
			nodesTSV:         "",
			tagsData:         "",
			linksData:        "",
			backlinksData:    "",
			wantNodesCount:   0,
			wantNodes:        map[int]string{},
			wantNodesUpdated: map[int]string{},
			wantTags:         map[string][]int{},
			wantLinks:        map[int][]int{},
			wantBacklinks:    map[int][]int{},
		},
	}

	parseTS := func(s string) (time.Time, bool) {
		if s == "" {
			return time.Time{}, true
		}
		layouts := []string{
			time.RFC3339,
			"2006-01-02T15:04:05Z07:00",
			"2006-01-02T15:04:05Z",
		}
		for _, l := range layouts {
			if t, err := time.Parse(l, s); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
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
			find := func(id NodeId) *NodeIndexEntry {
				return dex.GetRef(t.Context(), id)
			}

			// verify expected nodes, titles and timestamps
			for id, wantTitle := range tc.wantNodes {
				n := find(NodeId{ID: id})
				require.NotNil(t, n, "node %d missing", int(id))
				require.Equal(t, wantTitle, n.Title, "node %d title mismatch", int(id))

				if expectStr, ok := tc.wantNodesUpdated[id]; ok {
					expT, okp := parseTS(expectStr)
					if okp && !expT.IsZero() {
						require.True(t, n.Updated.Equal(expT),
							"node %d updated mismatch: want %v got %v", int(id), expT, n.Updated)
					} else {
						// Unparsable expected value implies we expect zero time.
						require.True(t, n.Updated.IsZero(),
							"node %d expected zero updated time, got %v", int(id), n.Updated)
					}
				}
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
				gotNodes, ok := dex.Links(t.Context(), NodeId{ID: wantSrc})
				require.True(t, ok, "expected links src %d missing", int(wantSrc))
				gotDsts := make([]int, 0, len(gotNodes))
				for _, n := range gotNodes {
					gotDsts = append(gotDsts, n.ID)
				}
				require.Equal(t, wantDsts, gotDsts, "links for %d mismatch", int(wantSrc))
			}

			// Validate backlinks by querying Dex per destination node
			for wantDst, wantSrcs := range tc.wantBacklinks {
				gotNodes, ok := dex.Backlinks(t.Context(), NodeId{ID: wantDst})
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
