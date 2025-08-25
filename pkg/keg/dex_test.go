package keg_test

// import (
// 	"testing"
//
// 	"github.com/jlrickert/tapper/pkg/internal"
// 	"github.com/jlrickert/tapper/pkg/keg"
// )
//
// func TestReadFromDex_BasicParsing(t *testing.T) {
// 	// Prepare sample index contents (exercise nodes, tags, links, backlinks)
// 	nodesTSV := "" +
// 		"0\t2025-08-04 22:03:53Z\tSorry, planned but not yet available\n" +
// 		"1\t2025-08-04 23:06:30Z\tConfiguration (config)\n" +
// 		"3\t2025-08-09 17:44:04Z\tZeke AI utility (zeke)\n" +
// 		"badline-without-tabs\n" + // malformed - should be skipped
// 		"999\tnot-a-time\tTitle with bad time\n" // id parses, time parse will produce zero time
//
// 	tagsData := "" +
// 		"zeke 3 10 45\n" +
// 		"keg 5 10 15 42\n" +
// 		"emptytag\n" // tag present with empty members
//
// 	linksData := "" +
// 		"1\t3 5\n" +
// 		"10\t\n" + // explicit empty destinations
// 		"bad\t3 4\n" // invalid source id -> skipped
//
// 	backlinksData := "" +
// 		"3\t1 2\n" +
// 		"42\t3 7\n" +
// 		"15\t\n" // empty sources
//
// 	// Create an in-memory repo and populate indexes.
// 	mem := keg.NewMemoryRepo()
//
// 	if err := mem.WriteIndex("nodes.tsv", []byte(nodesTSV)); err != nil {
// 		t.Fatalf("WriteIndex(nodes.tsv) failed: %v", err)
// 	}
// 	if err := mem.WriteIndex("tags", []byte(tagsData)); err != nil {
// 		t.Fatalf("WriteIndex(tags) failed: %v", err)
// 	}
// 	if err := mem.WriteIndex("links", []byte(linksData)); err != nil {
// 		t.Fatalf("WriteIndex(links) failed: %v", err)
// 	}
// 	if err := mem.WriteIndex("backlinks", []byte(backlinksData)); err != nil {
// 		t.Fatalf("WriteIndex(backlinks) failed: %v", err)
// 	}
//
// 	k := keg.NewKeg(
// 		mem,
// 		keg.WithLinkResolver(keg.NewBasicLinkResolver(func(alias, node string) (string, error) {
// 			return "", nil
// 		})),
// 		keg.WithClock(&internal.FixedClock{}),
// 	)
//
// 	dx, err := keg.ReadFromDex(k.Repo)
// 	if err != nil {
// 		t.Fatalf("ReadFromDex returned error: %v", err)
// 	}
//
// 	// Validate parsed nodes
// 	if len(dx.Nodes) != 4 { // 0,1,3,999 -> malformed line skipped; 4 entries
// 		t.Fatalf("unexpected nodes count: got %d want %d", len(dx.Nodes), 4)
// 	}
//
// 	// helper to lookup a NodeRef by id
// 	find := func(id keg.NodeID) *keg.NodeRef {
// 		for i := range dx.Nodes {
// 			if dx.Nodes[i].ID == id {
// 				return &dx.Nodes[i]
// 			}
// 		}
// 		return nil
// 	}
//
// 	n0 := find(0)
// 	if n0 == nil || n0.Title != "Sorry, planned but not yet available" {
// 		t.Fatalf("node 0 missing or title mismatch: %#v", n0)
// 	}
// 	n1 := find(1)
// 	if n1 == nil || n1.Title != "Configuration (config)" {
// 		t.Fatalf("node 1 missing or title mismatch: %#v", n1)
// 	}
// 	n3 := find(3)
// 	if n3 == nil || n3.Title != "Zeke AI utility (zeke)" {
// 		t.Fatalf("node 3 missing or title mismatch: %#v", n3)
// 	}
// 	n999 := find(999)
// 	if n999 == nil || n999.Title != "Title with bad time" {
// 		t.Fatalf("node 999 missing or title mismatch: %#v", n999)
// 	}
//
// 	// Check that the timestamp parsing produced a valid time for node 3
// 	want3, _ := time.Parse("2006-01-02 15:04:05Z07:00", "2025-08-09 17:44:04Z")
// 	if !n3.Updated.Equal(want3) {
// 		t.Fatalf("node 3 modified mismatch: got %v want %v", n3.Updated, want3)
// 	}
// 	// node 999 had an unparsable time; Modified should be zero time
// 	if !n999.Updated.IsZero() {
// 		t.Fatalf("expected zero Modified for node 999, got %v", n999.Updated)
// 	}
//
// 	// Validate tags
// 	// zeke -> 3,10,45
// 	gotZeke, ok := dx.Tags["zeke"]
// 	if !ok {
// 		t.Fatalf("expected tag 'zeke' present")
// 	}
// 	wantZeke := []keg.NodeID{3, 10, 45}
// 	if !reflect.DeepEqual(gotZeke, wantZeke) {
// 		t.Fatalf("tag zeke mismatch: got %#v want %#v", gotZeke, wantZeke)
// 	}
// 	// emptytag should exist with empty slice
// 	gotEmpty, ok := dx.Tags["emptytag"]
// 	if !ok {
// 		t.Fatalf("expected tag 'emptytag' present")
// 	}
// 	if len(gotEmpty) != 0 {
// 		t.Fatalf("expected empty slice for emptytag, got %#v", gotEmpty)
// 	}
//
// 	// Validate links
// 	// 1 -> [3,5]
// 	gotLinks1, ok := dx.Links[keg.NodeID(1)]
// 	if !ok {
// 		t.Fatalf("expected links for source 1")
// 	}
// 	wantLinks1 := []keg.NodeID{3, 5}
// 	if !reflect.DeepEqual(gotLinks1, wantLinks1) {
// 		t.Fatalf("links for 1 mismatch: got %#v want %#v", gotLinks1, wantLinks1)
// 	}
// 	// 10 -> empty slice
// 	gotLinks10, ok := dx.Links[keg.NodeID(10)]
// 	if !ok {
// 		t.Fatalf("expected links entry for source 10")
// 	}
// 	if len(gotLinks10) != 0 {
// 		t.Fatalf("expected empty links slice for 10, got %#v", gotLinks10)
// 	}
//
// 	// Validate backlinks
// 	gotBack3, ok := dx.Backlinks[keg.NodeID(3)]
// 	if !ok {
// 		t.Fatalf("expected backlinks for dest 3")
// 	}
// 	wantBack3 := []keg.NodeID{1, 2}
// 	if !reflect.DeepEqual(gotBack3, wantBack3) {
// 		t.Fatalf("backlinks for 3 mismatch: got %#v want %#v", gotBack3, wantBack3)
// 	}
// 	gotBack15, ok := dx.Backlinks[keg.NodeID(15)]
// 	if !ok {
// 		t.Fatalf("expected backlinks entry for dest 15")
// 	}
// 	if len(gotBack15) != 0 {
// 		t.Fatalf("expected empty backlinks slice for 15, got %#v", gotBack15)
// 	}
// }
