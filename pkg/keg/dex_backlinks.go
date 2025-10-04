package keg

import (
	"bytes"
	"context"
	"strings"
)

// BacklinkIndex maps a destination node path to the list of source nodes that
// link to that destination. The underlying map keys are node.Path() values
// (string). The index is used to construct the "backlinks" index artifact.
//
// The type is intended for in-memory, single-process use. Concurrency control
// is the caller's responsibility.
type BacklinkIndex struct {
	data map[string][]Node
}

// ParseBacklinksIndex parses the raw bytes of a backlinks index into a
// BacklinkIndex.
//
// Expected on-disk format is one line per destination:
//
//	"<dst>\t<src1> <src2> ...\n"
//
// Behavior:
//   - Empty or nil input yields an empty BacklinkIndex with no error.
//   - Lines are split on tab to separate destination from space-separated sources.
//   - Duplicate sources for a destination are tolerated and may be deduped by
//     callers of Data.
//   - This function does not modify any external state.
func ParseBacklinksIndex(ctx context.Context, data []byte) (*BacklinkIndex, error) {
	_ = ctx
	idx := &BacklinkIndex{
		data: map[string][]Node{},
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return idx, nil
	}

	lines := bytes.Split(data, []byte{'\n'})
	for _, l := range lines {
		line := strings.TrimSpace(string(l))
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			// malformed line; skip
			continue
		}
		dst := parts[0]
		srcs := strings.Fields(parts[1])
		if len(srcs) == 0 {
			continue
		}
		for _, s := range srcs {
			n, err := ParseNode(s)
			if err != nil {
				// skip malformed node tokens
				continue
			}
			idx.data[dst] = append(idx.data[dst], *n)
		}
	}

	return idx, nil
}

// Add incorporates backlink information derived from the provided NodeData.
// For each outgoing link listed in data.Links the function will add the source
// node (data.ID) to the corresponding destination entry in the index.
//
// Behavior expectations:
//   - If idx is nil the call is a no-op and returns nil.
//   - If idx.data is nil it will be initialized.
//   - The method should avoid introducing duplicate source entries for a given
//     destination when possible.
//
// This method only mutates in-memory state and does not perform I/O.
func (idx *BacklinkIndex) Add(ctx context.Context, data *NodeData) error {
	if idx == nil {
		return nil
	}
	if idx.data == nil {
		idx.data = map[string][]Node{}
	}

	for _, dst := range data.Links() {
		key := dst.Path()
		list := idx.data[key]
		dup := false
		for _, n := range list {
			if n.Equals(data.ID) {
				dup = true
				break
			}
		}
		if !dup {
			idx.data[key] = append(list, data.ID)
		}
	}

	return nil
}

// Rm removes any backlink references introduced by the given node. It removes
// the node as a source from any destination lists and may remove the entry for
// a destination if it ends up with no sources.
//
// Behavior expectations:
//   - If idx is nil the call is a no-op and returns nil.
//   - If idx.data is nil it will be initialized to an empty map.
//   - After removal, entries with no sources may either remain as empty slices
//     or be deleted; callers should tolerate either representation.
//
// This method only mutates in-memory state and does not perform I/O.
func (idx *BacklinkIndex) Rm(ctx context.Context, node Node) error {
	if idx == nil {
		return nil
	}
	if idx.data == nil {
		idx.data = map[string][]Node{}
	}

	for dst, list := range idx.data {
		if len(list) == 0 {
			continue
		}
		var out []Node
		removed := false
		for _, n := range list {
			if n.Equals(node) {
				removed = true
				continue
			}
			out = append(out, n)
		}
		if !removed {
			continue
		}
		if len(out) == 0 {
			// remove empty entries to avoid emitting empty lines
			delete(idx.data, dst)
		} else {
			idx.data[dst] = out
		}
	}

	return nil
}

// Data serializes the index into the canonical on-disk format.
//
// Serialization rules:
//   - Each non-empty destination produces a line:
//     "<dst>\t<src1> <src2> ...\n"
//   - Source lists are deduplicated and sorted in a deterministic order.
//   - Destination keys are emitted in a deterministic, parse-aware order
//     (numeric node ids sorted numerically when possible, otherwise lexicographic).
//   - An empty index returns an empty byte slice.
//
// The returned bytes are owned by the caller and may be written atomically by
// the repository layer.
func (idx *BacklinkIndex) Data(ctx context.Context) ([]byte, error) {
	_ = ctx
	if idx == nil {
		return []byte{}, nil
	}
	if idx.data == nil {
		idx.data = map[string][]Node{}
	}

	if len(idx.data) == 0 {
		return []byte{}, nil
	}

	// collect keys
	keys := make([]string, 0, len(idx.data))
	for k := range idx.data {
		keys = append(keys, k)
	}

	// comparator that tries to parse Node ids and compare numerically when possible
	cmp := func(a, b string) int {
		na, ea := ParseNode(a)
		nb, eb := ParseNode(b)
		if ea == nil && eb == nil {
			return na.Compare(*nb)
		}
		// if only a parsed, prefer parsed
		if ea == nil {
			return -1
		}
		if eb == nil {
			return 1
		}
		// fallback to lexicographic
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}

	// insertion sort keys using cmp to avoid adding imports
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && cmp(keys[j-1], keys[j]) > 0; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}

	var bld []byte
	// build each line: "<dst>\t<src1> <src2>...\n"
	for _, dst := range keys {
		srcs := idx.data[dst]
		if len(srcs) == 0 {
			// skip destinations with no sources
			continue
		}

		// dedupe by path and keep a representative Node for sorting
		m := make(map[string]Node, len(srcs))
		for _, n := range srcs {
			m[n.Path()] = n
		}

		// extract unique nodes
		uniq := make([]Node, 0, len(m))
		for _, n := range m {
			uniq = append(uniq, n)
		}

		// sort uniq by numeric id then code (simple insertion sort)
		for i := 1; i < len(uniq); i++ {
			for j := i; j > 0; j-- {
				aj := uniq[j-1]
				aj1 := uniq[j]
				less := false
				if aj.ID != aj1.ID {
					less = aj.ID > aj1.ID // note: we want ascending, swap when previous > current
				} else {
					less = aj.Code > aj1.Code
				}
				if !less {
					break
				}
				uniq[j-1], uniq[j] = uniq[j], uniq[j-1]
			}
		}

		// build source path list
		var line []byte
		line = append(line, dst...)
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
