package keg

import (
	"context"
	"strings"
)

// LinkIndex maps a source node path to the list of destination nodes that the
// source links to. It is used to construct the "links" index artifact.
//
// The underlying map keys are node.Path() values (string). The index is
// expected to be small enough to be kept in memory for index-building tooling.
//
// The type has unexported fields and is safe for in-memory, single-process use.
// Concurrency control is the caller's responsibility.
type LinkIndex struct {
	data map[string][]NodeId
}

// ParseLinkIndex parses the raw bytes of a links index into a LinkIndex.
// The expected on-disk format is one line per source:
//
//	"<src>\t<dst1> <dst2> ...\n"
//
// Behavior:
//   - Empty or nil input yields an empty LinkIndex with no error.
//   - Lines are split on tab to separate source from space-separated destinations.
//   - Duplicate destinations for a source are tolerated and may be deduped by
//     callers of Data.
//
// This function does not modify any external state.
func ParseLinkIndex(ctx context.Context, data []byte) (LinkIndex, error) {
	_ = ctx
	if len(data) == 0 {
		return LinkIndex{data: map[string][]NodeId{}}, nil
	}

	out := LinkIndex{data: map[string][]NodeId{}}
	s := string(data)
	lines := strings.SplitSeq(s, "\n")
	for line := range lines {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) < 1 {
			continue
		}
		src := parts[0]
		var dests []NodeId
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			toks := strings.FieldsSeq(parts[1]) // splits on spaces
			for tok := range toks {
				n, err := ParseNode(tok)
				if err != nil {
					// tolerate malformed tokens by skipping
					continue
				}
				dests = append(dests, *n)
			}
		}
		out.data[src] = dests
	}
	return out, nil
}

// Add incorporates link information from the provided NodeData into the index.
// The NodeData.ID value is used as the source key (via NodeId.Path semantics) and
// NodeData.Links is treated as the list of destination nodes.
//
// Behavior expectations (not enforced here but callers may rely on them):
//   - If idx is nil the call is a no-op and returns nil.
//   - If idx.data is nil it will be initialized.
//   - The method should avoid introducing duplicate destination entries for a
//     given source when possible.
//
// This method only mutates in-memory state and does not perform I/O.
func (idx *LinkIndex) Add(ctx context.Context, data *NodeData) error {
	_ = ctx
	if idx == nil {
		return nil
	}
	if idx.data == nil {
		idx.data = map[string][]NodeId{}
	}

	key := data.ID.Path()
	existing := idx.data[key]

	links := data.Links()

	// dedupe by path, preferring existing representative NodeId
	m := make(map[string]NodeId, len(existing)+len(links))
	for _, n := range existing {
		m[n.Path()] = n
	}
	for _, n := range links {
		m[n.Path()] = n
	}

	uniq := make([]NodeId, 0, len(m))
	for _, n := range m {
		uniq = append(uniq, n)
	}

	// basic insertion sort by NodeId.Compare (ascending)
	for i := 1; i < len(uniq); i++ {
		for j := i; j > 0 && uniq[j-1].Compare(uniq[j]) > 0; j-- {
			uniq[j-1], uniq[j] = uniq[j], uniq[j-1]
		}
	}

	idx.data[key] = uniq
	return nil
}

// Rm removes any references introduced by the given node as a source and
// removes the node from any destination lists where it appears.
//
// Behavior expectations:
//   - If idx is nil the call is a no-op and returns nil.
//   - If idx.data is nil it will be initialized to an empty map.
//   - After removal, entries with no destinations may either remain as empty
//     slices or be deleted; callers should tolerate either representation.
//
// This method only mutates in-memory state and does not perform I/O.
func (idx *LinkIndex) Rm(ctx context.Context, node NodeId) error {
	_ = ctx
	if idx == nil {
		return nil
	}
	if idx.data == nil {
		idx.data = map[string][]NodeId{}
	}

	// remove the node as a source
	delete(idx.data, node.Path())

	// remove the node from any destination lists
	for src, dsts := range idx.data {
		if len(dsts) == 0 {
			continue
		}
		out := dsts[:0]
		for _, d := range dsts {
			if !d.Equals(node) {
				out = append(out, d)
			}
		}
		if len(out) == 0 {
			// remove empty entries to avoid emitting empty lines
			delete(idx.data, src)
		} else {
			// assign trimmed slice
			idx.data[src] = out
		}
	}

	return nil
}

// Data serializes the index into the canonical on-disk format.
//
// Serialization rules:
//   - Each non-empty source produces a line:
//     "<src>\t<dst1> <dst2> ...\n"
//   - Destination lists are deduplicated and sorted in a deterministic order.
//   - Source keys are emitted in a deterministic, parse-aware order (numeric
//     node ids sorted numerically when possible, otherwise lexicographic).
//   - An empty index returns an empty byte slice.
//
// The returned bytes are owned by the caller and may be written atomically by
// the repository layer.
func (idx *LinkIndex) Data(ctx context.Context) ([]byte, error) {
	_ = ctx
	if idx == nil {
		return []byte{}, nil
	}
	if idx.data == nil {
		idx.data = map[string][]NodeId{}
	}
	if len(idx.data) == 0 {
		return []byte{}, nil
	}

	// collect keys
	keys := make([]string, 0, len(idx.data))
	for k := range idx.data {
		keys = append(keys, k)
	}

	// comparator that tries to parse NodeId ids and compare numerically when possible
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

	// insertion sort keys using cmp
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && cmp(keys[j-1], keys[j]) > 0; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}

	var bld []byte
	// build each line: "<src>\t<dst1> <dst2>...\n"
	for _, src := range keys {
		dsts := idx.data[src]
		if len(dsts) == 0 {
			continue
		}

		// dedupe by path and keep representative NodeId
		m := make(map[string]NodeId, len(dsts))
		for _, n := range dsts {
			m[n.Path()] = n
		}

		uniq := make([]NodeId, 0, len(m))
		for _, n := range m {
			uniq = append(uniq, n)
		}

		// sort uniq by numeric id then code (ascending)
		for i := 1; i < len(uniq); i++ {
			for j := i; j > 0 && uniq[j-1].Compare(uniq[j]) > 0; j-- {
				uniq[j-1], uniq[j] = uniq[j], uniq[j-1]
			}
		}

		// build source line
		var line []byte
		line = append(line, src...)
		line = append(line, '\t')
		for i, n := range uniq {
			if i > 0 {
				line = append(line, ' ')
			}
			line = append(line, n.Path()...)
		}
		line = append(line, '\n')
		bld = append(bld, line...)
	}

	return bld, nil
}
