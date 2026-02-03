package keg

import (
	"context"
	"slices"
	"sort"
	"strings"
)

// TagIndex is an in-memory index mapping a normalized tag string to the list
// of nodes that declare that tag.
//
// The index format (used by ParseTagIndex and Data) is line-oriented. Each line
// represents a tag and its node list in the form:
//
//	<tag>\t<node1> <node2> ...\n
//
// Where <nodeN> is the node.Path() string representation (for example "42" or
// "42-0001"). Parsers should tolerate empty input and skip empty lines. When
// serializing, the implementation should produce stable output by sorting tag
// keys and de-duplicating and sorting node lists.
//
// Note: TagIndex does not perform internal synchronization. Callers that need
// concurrent access should guard the index with a mutex.
type TagIndex struct {
	data map[string][]NodeId
}

// ParseTagIndex parses the serialized tag index bytes into a TagIndex.
//
// Expected input is zero or more lines separated by newline. Each non-empty
// line must contain a tag, a tab, and a space-separated list of node ids.
// Invalid or malformed lines should be handled gracefully by ignoring the
// offending line and continuing parsing. An empty input yields an empty
// TagIndex and no error.
func ParseTagIndex(ctx context.Context, data []byte) (TagIndex, error) {
	_ = ctx
	idx := TagIndex{data: map[string][]NodeId{}}
	if len(data) == 0 {
		return idx, nil
	}

	// split on newline
	lines := strings.SplitSeq(string(data), "\n")
	for ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		parts := strings.SplitN(ln, "\t", 2)
		if len(parts) < 2 {
			// malformed, skip
			continue
		}
		tag := parts[0]
		if tag == "" {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) == 0 {
			// no nodes -> skip
			continue
		}
		list := make([]NodeId, 0, len(fields))
		for _, s := range fields {
			n, err := ParseNode(s)
			if err != nil {
				// skip malformed node ids
				continue
			}
			list = append(list, *n)
		}
		if len(list) > 0 {
			idx.data[tag] = list
		}
	}
	return idx, nil
}

// Add incorporates the node into the index for each tag present on the node.
//
// Behavior notes:
// - If idx is nil this is a no-op.
// - The method should ensure idx.data is initialized when first used.
// - Duplicate entries for a given tag should be avoided (idempotent add).
// - The node should be added using node.Path() as the identifier.
func (idx *TagIndex) Add(ctx context.Context, data *NodeData) error {
	_ = ctx
	if idx == nil {
		return nil
	}
	if idx.data == nil {
		idx.data = map[string][]NodeId{}
	}
	tags := data.Tags()
	if len(tags) == 0 {
		return nil
	}

	for _, tag := range tags {
		if tag == "" {
			continue
		}
		list := idx.data[tag]
		dup := false
		for _, n := range list {
			if n.Equals(data.ID) {
				dup = true
				break
			}
		}
		if !dup {
			list = append(list, data.ID)
			// keep list deterministic by sorting after append
			slices.SortFunc(list, func(a NodeId, b NodeId) int {
				return a.Compare(b)
			})
			idx.data[tag] = list
		}
	}

	return nil
}

// Rm removes the node from all tag lists in the index.
//
// Behavior notes:
//   - If idx is nil this is a no-op.
//   - If a tag has no remaining nodes after removal it should be removed from
//     the map to avoid emitting empty tag lines when serialized.
func (idx *TagIndex) Rm(ctx context.Context, node NodeId) error {
	_ = ctx
	if idx == nil {
		return nil
	}
	if idx.data == nil {
		idx.data = map[string][]NodeId{}
		return nil
	}

	p := node.Path()
	for tag, list := range idx.data {
		if len(list) == 0 {
			continue
		}
		// filter out node.Path()
		out := list[:0]
		for _, n := range list {
			if n.Path() == p {
				continue
			}
			out = append(out, n)
		}
		if len(out) == 0 {
			delete(idx.data, tag)
		} else {
			// ensure we keep a copy with correct length
			cpy := make([]NodeId, len(out))
			copy(cpy, out)
			idx.data[tag] = cpy
		}
	}
	return nil
}

// Data serializes the TagIndex to the canonical byte representation described
// for ParseTagIndex.
//
// Serialization requirements:
//   - Tags (map keys) must be emitted in a stable, deterministic order. When a
//     tag token can be parsed as a NodeId id it may be ordered numerically; otherwise
//     fall back to lexicographic ordering.
//   - NodeId lists for each tag must be de-duplicated and sorted by numeric id then
//     by code (the same ordering ParseNode/NodeId.Compare implies).
//   - Lines must use a single tab between tag and the node list, and a single
//     space between node ids. Each line must be terminated with a newline.
//   - If the index is empty return an empty byte slice and no error.
func (idx *TagIndex) Data(ctx context.Context) ([]byte, error) {
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
	cmpKey := func(a, b string) bool {
		na, ea := ParseNode(a)
		nb, eb := ParseNode(b)
		if ea == nil && eb == nil {
			return na.Compare(*nb) < 0
		}
		if ea == nil {
			return true
		}
		if eb == nil {
			return false
		}
		return a < b
	}

	sort.Slice(keys, func(i, j int) bool { return cmpKey(keys[i], keys[j]) })

	var b strings.Builder
	for _, tag := range keys {
		srcs := idx.data[tag]
		if len(srcs) == 0 {
			continue
		}

		// dedupe by path keeping a representative NodeId
		m := make(map[string]NodeId, len(srcs))
		for _, n := range srcs {
			m[n.Path()] = n
		}

		uniq := make([]NodeId, 0, len(m))
		for _, n := range m {
			uniq = append(uniq, n)
		}

		// sort uniq by numeric id then code using NodeId.Compare
		sort.Slice(uniq, func(i, j int) bool { return uniq[i].Compare(uniq[j]) < 0 })

		// build line
		b.WriteString(tag)
		b.WriteByte('\t')
		for i, n := range uniq {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(n.Path())
		}
		b.WriteByte('\n')
	}

	return []byte(b.String()), nil
}
