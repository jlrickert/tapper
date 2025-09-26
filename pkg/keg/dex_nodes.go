package keg

import (
	"context"
	"strings"
	"time"
)

// NodeIndex is an in-memory index of node descriptors used to construct the
// `nodes.tsv` index artifact.
//
// The index stores a slice of `NodeIndexEntry` values in a deterministic order
// (ascending by numeric node id, with codes used to break ties). It provides
// helpers to parse a serialized index, mutate the in-memory list, and produce
// the canonical serialized bytes.
//
// Concurrency note: NodeIndex itself does not perform internal synchronization.
// Callers that require concurrent access should guard an instance with a mutex.
type NodeIndex struct {
	data []NodeIndexEntry
}

// ParseNodeIndex parses the serialized nodes index bytes into a NodeIndex.
//
// Expected input is zero or more lines separated by newline. Each non-empty
// line represents a node entry in the canonical TSV format used by the repo.
// Parsers should tolerate empty input and skip malformed lines while continuing
// to parse the remainder. An empty input yields an empty NodeIndex and no error.
//
// Parsing rules and leniency:
//   - Each valid line is expected to contain at least the ID field. Additional
//     columns (for example updated timestamp and title) are accepted when present.
//   - Lines that cannot be parsed into a valid NodeIndexEntry are skipped and do
//     not cause the entire parse to fail. This allows forward compatibility when
//     new columns are added to the on-disk format.
//   - The returned NodeIndex contains entries in the order they were parsed; it
//     is the caller's responsibility to sort or normalize ordering if desired.
//
// Returns:
//   - a NodeIndex containing parsed NodeIndexEntry values.
//   - a non nil error only for unexpected conditions preventing parsing of the
//     entire input (for example severe encoding issues). Minor line-level parse
//     problems are tolerated and do not cause an error.
//
// Example input:
//
//	"42\t2025-01-02T15:04:05Z\tMy Title\n0\t2024-12-01T12:00:00Z\tZero Node\n"
//
// Note: the expected column order is: id<TAB>updated<TAB>title
func ParseNodeIndex(ctx context.Context, data []byte) (NodeIndex, error) {
	_ = ctx
	idx := NodeIndex{data: []NodeIndexEntry{}}

	s := strings.TrimSpace(string(data))
	if s == "" {
		return idx, nil
	}

	lines := strings.SplitSeq(s, "\n")
	for ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}

		// Expect columns: id<TAB>updated<TAB>title
		parts := strings.SplitN(ln, "\t", 3)
		if len(parts) < 3 {
			// malformed line; skip
			continue
		}

		id := strings.TrimSpace(parts[0])
		if id == "" {
			// missing id; skip
			continue
		}

		updated := strings.TrimSpace(parts[1])
		if updated == "" {
			continue
		}
		var u time.Time
		if strings.TrimSpace(parts[1]) != "" {
			if t, err := time.Parse(time.RFC3339, strings.TrimSpace(parts[1])); err == nil {
				u = t
			}
			// if parse fails, leave updated zero and continue; tolerate malformed timestamps
		}

		title := strings.TrimSpace(parts[2])

		entry := NodeIndexEntry{
			ID:      id,
			Title:   title,
			Updated: u,
		}
		idx.data = append(idx.data, entry)
	}

	return idx, nil
}

// Add inserts the provided node into the index. The index should remain sorted
// by ascending node id after the operation.
//
// Behavior expectations:
//   - If idx is nil the call is a no-op and returns nil.
//   - The method should ensure idx.data is initialized when first used.
//   - Adding an existing node id should be idempotent: the existing entry should
//     be updated or replaced rather than producing duplicates.
//   - The operation is in-memory only and does not perform I/O.
//
// Typical callers:
// - Index builders that aggregate node metadata into the nodes index.
// - Tests that need to construct an in-memory nodes list.
//
// Note: This method does not acquire any synchronization; callers should hold a
// lock if concurrent mutations are possible.
//
// Implementation note:
//
//	The function should insert or update the NodeIndexEntry derived from the
//	supplied NodeData. After modification, idx.data must be ordered so that
//	Next and serialized output are stable and deterministic.
func (idx *NodeIndex) Add(ctx context.Context, data NodeData) error {
	_ = ctx
	if idx == nil {
		return nil
	}
	entry := NodeIndexEntry{
		ID:      data.ID,
		Title:   data.Title,
		Updated: data.Updated,
	}
	// initialize if needed
	if idx.data == nil {
		idx.data = []NodeIndexEntry{entry}
		return nil
	}

	// comparator: returns negative if a < b, 0 if equal, positive if a > b
	cmp := func(a, b string) int {
		na, ea := ParseNode(a)
		nb, eb := ParseNode(b)
		if ea == nil && eb == nil {
			return na.Compare(*nb)
		}
		if ea == nil {
			return -1
		}
		if eb == nil {
			return 1
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}

	// find existing entry or insertion point
	for i := range idx.data {
		// if equal id, replace
		if idx.data[i].ID == entry.ID {
			idx.data[i] = entry
			return nil
		}
		// if current id > new id, insert before
		if cmp(idx.data[i].ID, entry.ID) > 0 {
			// insert at i
			newSlice := make([]NodeIndexEntry, 0, len(idx.data)+1)
			newSlice = append(newSlice, idx.data[:i]...)
			newSlice = append(newSlice, entry)
			newSlice = append(newSlice, idx.data[i:]...)
			idx.data = newSlice
			return nil
		}
	}
	// append at end
	idx.data = append(idx.data, entry)
	return nil
}

// Rm removes the node identified by id from the index.
//
// Behavior expectations:
// - If idx is nil the call is a no-op and returns nil.
// - If the node is not present the call should not error.
// - After removal the index slice should remain in a stable, sorted state.
// - This method only mutates in-memory state and does not perform I/O.
//
// Typical callers:
// - Index maintenance routines that remove entries for deleted nodes.
// - Tests cleaning up expected state.
//
// Implementation note:
//
//	The function should locate the entry whose ID equals node.Path() and remove
//	it from the slice. The remaining slice should preserve deterministic order.
func (idx *NodeIndex) Rm(ctx context.Context, node Node) error {
	_ = ctx
	if idx == nil || idx.data == nil {
		return nil
	}
	target := node.Path()
	for i := range idx.data {
		if idx.data[i].ID == target {
			// remove element i
			idx.data = append(idx.data[:i], idx.data[i+1:]...)
			return nil
		}
	}
	return nil
}

// Data serializes the NodeIndex into the canonical on-disk TSV representation.
//
// Serialization rules:
//   - Each entry produces a single line in the form used by the repository's
//     nodes index. Common column order is: id<TAB>title<TAB>updated<LF>.
//   - Entries must be emitted in ascending node id order.
//   - An empty index returns an empty byte slice.
//
// The returned bytes are owned by the caller and may be written atomically by
// the repository layer. The function should not modify idx.data.
//
// Implementation note:
//
//	The function should not rely on external state. It must produce stable,
//	deterministic output suitable for writing to an index file.
func (idx *NodeIndex) Data(ctx context.Context) ([]byte, error) {
	_ = ctx
	if idx == nil || len(idx.data) == 0 {
		return []byte{}, nil
	}
	var b strings.Builder
	for _, e := range idx.data {
		b.WriteString(e.ID)
		b.WriteByte('\t')
		b.WriteString(e.Title)
		if !e.Updated.IsZero() {
			b.WriteByte('\t')
			b.WriteString(e.Updated.Format(time.RFC3339))
		}
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

// List returns the in-memory slice of NodeIndexEntry. The returned slice is the
// underlying data and callers should not mutate it to avoid data races.
func (idx *NodeIndex) List(ctx context.Context) []NodeIndexEntry {
	return idx.data
}

// Get returns the NodeIndexEntry pointer for the provided node if present.
// The lookup uses node.Path() to match the ID field of entries.
//
// Returns:
//   - *NodeIndexEntry when the entry is present.
//   - nil when the entry is not present or idx is nil.
//
// The returned pointer points into the internal slice. Callers that need to
// modify the entry should copy it first to avoid data races.
func (idx *NodeIndex) Get(ctx context.Context, node Node) *NodeIndexEntry {
	_ = ctx
	if idx == nil || idx.data == nil {
		return nil
	}
	id := node.Path()
	for i := range idx.data {
		if idx.data[i].ID == id {
			return &idx.data[i]
		}
	}
	return nil
}

// Next returns the next available Node id based on the current index contents.
//
// Semantics:
//   - If the index is empty, Next returns Node{ID:0, Code:""} (the zeroth id).
//   - Otherwise Next returns a Node whose ID is one greater than the highest
//     numeric ID present in the index. If entries contain code suffixes the
//     numeric portion is used for ordering.
//   - The function does not modify the index.
//
// Implementation note:
//
//	The function should examine idx.data to determine the maximal numeric id and
//	return the subsequent id. It should not allocate or write any external state.
func (idx *NodeIndex) Next(ctx context.Context) Node {
	_ = ctx
	if idx == nil || len(idx.data) == 0 {
		return Node{ID: 0, Code: ""}
	}
	maxID := -1
	for _, e := range idx.data {
		if n, err := ParseNode(e.ID); err == nil {
			if n.ID > maxID {
				maxID = n.ID
			}
		}
	}
	if maxID < 0 {
		// No parsable numeric ids found; return zero as next id.
		return Node{ID: 0, Code: ""}
	}
	return Node{ID: maxID + 1, Code: ""}
}
