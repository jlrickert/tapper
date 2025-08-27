package keg_test

import (
	"reflect"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
)

func TestReadFromDex_Table(t *testing.T) {
	cases := []struct {
		name          string
		nodesTSV      string
		tagsData      string
		linksData     string
		backlinksData string

		wantNodesCount int
		wantNodes      map[keg.NodeID]string

		wantTags      map[string][]keg.NodeID
		wantLinks     map[keg.NodeID][]keg.NodeID
		wantBacklinks map[keg.NodeID][]keg.NodeID
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
				"zeke 3 10 45\n" +
				"keg 5 10 15 42\n" +
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

			wantNodes: map[keg.NodeID]string{
				0:   "Sorry, planned but not yet available",
				1:   "Configuration (config)",
				3:   "Zeke AI utility (zeke)",
				999: "Title with bad time",
			},

			// Note: indices that contain empty member lists may be omitted by parsers.
			// Tests should not assume empty-member entries are always present.
			wantTags: map[string][]keg.NodeID{
				"zeke": {3, 10, 45},
			},
			wantLinks: map[keg.NodeID][]keg.NodeID{
				1: {3, 5},
			},
			wantBacklinks: map[keg.NodeID][]keg.NodeID{
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
			wantNodes:      map[keg.NodeID]string{},
			wantTags:       map[string][]keg.NodeID{},
			wantLinks:      map[keg.NodeID][]keg.NodeID{},
			wantBacklinks:  map[keg.NodeID][]keg.NodeID{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Helper()

			mem := keg.NewMemoryRepo()

			// write indexes only if non-empty (tests may want to omit them)
			if tc.nodesTSV != "" {
				if err := mem.WriteIndex(t.Context(), "nodes.tsv", []byte(tc.nodesTSV)); err != nil {
					t.Fatalf("%s: WriteIndex(nodes.tsv) failed: %v", tc.name, err)
				}
			}
			if tc.tagsData != "" {
				if err := mem.WriteIndex(t.Context(), "tags", []byte(tc.tagsData)); err != nil {
					t.Fatalf("%s: WriteIndex(tags) failed: %v", tc.name, err)
				}
			}
			if tc.linksData != "" {
				if err := mem.WriteIndex(t.Context(), "links", []byte(tc.linksData)); err != nil {
					t.Fatalf("%s: WriteIndex(links) failed: %v", tc.name, err)
				}
			}
			if tc.backlinksData != "" {
				if err := mem.WriteIndex(t.Context(), "backlinks", []byte(tc.backlinksData)); err != nil {
					t.Fatalf("%s: WriteIndex(backlinks) failed: %v", tc.name, err)
				}
			}

			// Read indexes using the Dex convenience wrapper
			dex, err := keg.NewDexFromRepo(t.Context(), mem)
			if err != nil {
				t.Fatalf("%s: NewDexFromRepo returned error: %v", tc.name, err)
			}

			// Nodes count
			if got := len(dex.Nodes()); got != tc.wantNodesCount {
				t.Fatalf("%s: unexpected nodes count: got %d want %d", tc.name, got, tc.wantNodesCount)
			}

			// helper to lookup a NodeRef by id via Dex
			find := func(id keg.NodeID) *keg.NodeRef {
				return dex.GetNode(id)
			}

			// verify expected nodes and titles
			for id, wantTitle := range tc.wantNodes {
				n := find(id)
				if n == nil {
					t.Fatalf("%s: node %d missing", tc.name, int(id))
				}
				if n.Title != wantTitle {
					t.Fatalf("%s: node %d title mismatch: got %q want %q", tc.name, int(id), n.Title, wantTitle)
				}
			}

			// Validate tags
			tags := dex.Tags()
			// Only ensure expected tags are present with expected members.
			for wantTag, wantIDs := range tc.wantTags {
				gotIDs, ok := tags[wantTag]
				if !ok {
					t.Fatalf("%s: expected tag %q missing", tc.name, wantTag)
				}
				if !reflect.DeepEqual(gotIDs, wantIDs) {
					t.Fatalf("%s: tag %q mismatch: got %#v want %#v", tc.name, wantTag, gotIDs, wantIDs)
				}
			}

			// Validate links
			links := dex.Links()
			for wantSrc, wantDsts := range tc.wantLinks {
				gotDsts, ok := links[wantSrc]
				if !ok {
					t.Fatalf("%s: expected links src %d missing", tc.name, int(wantSrc))
				}
				if !reflect.DeepEqual(gotDsts, wantDsts) {
					t.Fatalf("%s: links for %d mismatch: got %#v want %#v", tc.name, int(wantSrc), gotDsts, wantDsts)
				}
			}

			// Validate backlinks
			backlinks := dex.Backlinks()
			for wantDst, wantSrcs := range tc.wantBacklinks {
				gotSrcs, ok := backlinks[wantDst]
				if !ok {
					t.Fatalf("%s: expected backlinks dst %d missing", tc.name, int(wantDst))
				}
				if !reflect.DeepEqual(gotSrcs, wantSrcs) {
					t.Fatalf("%s: backlinks for %d mismatch: got %#v want %#v", tc.name, int(wantDst), gotSrcs, wantSrcs)
				}
			}
		})
	}
}
