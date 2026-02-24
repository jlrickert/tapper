package keg

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// changesTimeFmt is the timestamp format used in dex/changes.md entries.
// Example: "2025-10-03 20:52:37Z"
const changesTimeFmt = "2006-01-02 15:04:05Z"

// --------------------------------------------------------------------------
// ChangesIndex
// --------------------------------------------------------------------------

// ChangesIndex is an in-memory index of all nodes sorted by updated time in
// reverse-chronological order (newest first). It is used to build the
// dex/changes.md index artifact.
//
// Concurrency note: ChangesIndex does not perform internal synchronization.
// Callers that require concurrent access should guard an instance with a mutex.
type ChangesIndex struct {
	data []NodeIndexEntry // sorted by Updated descending (newest first)
}

// ParseChangesIndex parses the serialized dex/changes.md bytes into a
// ChangesIndex. Each non-empty line must be in the format:
//
//	* YYYY-MM-DD HH:MM:SSZ [TITLE](../ID)
//
// Malformed lines are silently skipped. An empty input yields an empty
// ChangesIndex with no error.
func ParseChangesIndex(ctx context.Context, data []byte) (ChangesIndex, error) {
	_ = ctx
	idx := ChangesIndex{data: []NodeIndexEntry{}}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return idx, nil
	}
	for ln := range strings.SplitSeq(s, "\n") {
		entry, ok := parseChangesLine(strings.TrimSpace(ln))
		if !ok {
			continue
		}
		idx.data = append(idx.data, entry)
	}
	return idx, nil
}

// parseChangesLine parses a single line from changes.md.
// Expected format: "* 2025-10-03 20:52:37Z [TITLE](../ID)"
func parseChangesLine(line string) (NodeIndexEntry, bool) {
	if !strings.HasPrefix(line, "* ") {
		return NodeIndexEntry{}, false
	}
	rest := line[2:] // strip "* "

	// Timestamp is exactly 20 chars: "2006-01-02 15:04:05Z"
	const tsLen = 20
	if len(rest) < tsLen+1 { // need at least timestamp + space
		return NodeIndexEntry{}, false
	}
	tsStr := rest[:tsLen]
	t, err := time.Parse(changesTimeFmt, tsStr)
	if err != nil {
		return NodeIndexEntry{}, false
	}

	rest = rest[tsLen:]
	if !strings.HasPrefix(rest, " ") {
		return NodeIndexEntry{}, false
	}
	rest = rest[1:] // strip space between timestamp and link

	// Parse markdown link: [TITLE](../ID)
	if !strings.HasPrefix(rest, "[") || !strings.HasSuffix(rest, ")") {
		return NodeIndexEntry{}, false
	}
	// Find the last occurrence of "](.." to split title from ID
	sep := strings.LastIndex(rest, "](../")
	if sep < 0 {
		return NodeIndexEntry{}, false
	}
	title := rest[1:sep]                  // skip leading "["
	id := rest[sep+5 : len(rest)-1]       // skip "](../" and trailing ")"

	if id == "" {
		return NodeIndexEntry{}, false
	}

	return NodeIndexEntry{
		ID:      id,
		Title:   title,
		Updated: t.UTC(),
	}, true
}

// Add inserts or updates the node in the index, maintaining reverse-
// chronological sort order (newest Updated first). If a node with the same ID
// already exists it is replaced.
func (idx *ChangesIndex) Add(ctx context.Context, data *NodeData) error {
	_ = ctx
	if idx == nil {
		return nil
	}
	entry := data.Ref()
	if idx.data == nil {
		idx.data = []NodeIndexEntry{entry}
		return nil
	}

	// Replace existing entry if present.
	for i := range idx.data {
		if idx.data[i].ID == entry.ID {
			idx.data[i] = entry
			sort.SliceStable(idx.data, func(a, b int) bool {
				return idx.data[a].Updated.After(idx.data[b].Updated)
			})
			return nil
		}
	}

	// Insert and re-sort.
	idx.data = append(idx.data, entry)
	sort.SliceStable(idx.data, func(a, b int) bool {
		return idx.data[a].Updated.After(idx.data[b].Updated)
	})
	return nil
}

// Rm removes the node identified by node from the index. If the node is not
// present the call is a no-op.
func (idx *ChangesIndex) Rm(ctx context.Context, node NodeId) error {
	_ = ctx
	if idx == nil || idx.data == nil {
		return nil
	}
	target := node.Path()
	for i := range idx.data {
		if idx.data[i].ID == target {
			idx.data = append(idx.data[:i], idx.data[i+1:]...)
			return nil
		}
	}
	return nil
}

// Clear resets the index to an empty state.
func (idx *ChangesIndex) Clear(ctx context.Context) error {
	_ = ctx
	if idx == nil {
		return nil
	}
	idx.data = []NodeIndexEntry{}
	return nil
}

// Data serializes the ChangesIndex to the canonical dex/changes.md format.
// Each entry is emitted as:
//
//	* YYYY-MM-DD HH:MM:SSZ [TITLE](../ID)
//
// Entries are in reverse-chronological order (newest first). An empty index
// returns an empty byte slice.
func (idx *ChangesIndex) Data(ctx context.Context) ([]byte, error) {
	_ = ctx
	if idx == nil || len(idx.data) == 0 {
		return []byte{}, nil
	}
	var b strings.Builder
	for _, e := range idx.data {
		b.WriteString("* ")
		if !e.Updated.IsZero() {
			b.WriteString(e.Updated.UTC().Format(changesTimeFmt))
		} else {
			b.WriteString("0001-01-01 00:00:00Z")
		}
		b.WriteByte(' ')
		b.WriteByte('[')
		b.WriteString(e.Title)
		b.WriteString("](../")
		b.WriteString(e.ID)
		b.WriteByte(')')
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

// --------------------------------------------------------------------------
// TagFilteredIndex
// --------------------------------------------------------------------------

// coreIndexNames is the set of built-in index names (using the dex/ prefix as
// used in keg config Indexes entries) that cannot be overridden by
// config-driven tag-filtered indexes.
var coreIndexNames = map[string]bool{
	"dex/changes.md": true,
	"dex/nodes.tsv":  true,
	"dex/links":      true,
	"dex/backlinks":  true,
	"dex/tags":       true,
}

// IsCoreIndex reports whether the given index file path (as used in a keg
// config Indexes entry, e.g. "dex/changes.md") is one of the built-in
// protected index names.
func IsCoreIndex(name string) bool {
	return coreIndexNames[name]
}

// TagFilteredIndex is an in-memory index of nodes that match a boolean tag
// expression. It is used to build custom dex/NAME.md index artifacts driven
// by keg config Indexes entries with a non-empty Tags field.
//
// Concurrency note: TagFilteredIndex does not perform internal
// synchronization. Callers should guard access with a mutex when needed.
type TagFilteredIndex struct {
	// name is the short index filename used with repo.WriteIndex, e.g. "golang.md".
	name string
	// expr is the compiled tag expression evaluated per Add call.
	expr TagExpr
	// data holds matched entries sorted by Updated descending (newest first).
	data []NodeIndexEntry
}

// NewTagFilteredIndex creates a TagFilteredIndex for the given index file name
// and boolean tag query string. Returns an error if tagQuery fails to parse.
//
// name should be the short filename (without the "dex/" prefix) used when
// writing to the repository, e.g. "golang.md".
func NewTagFilteredIndex(name, tagQuery string) (*TagFilteredIndex, error) {
	expr, err := ParseTagExpression(tagQuery)
	if err != nil {
		return nil, fmt.Errorf("invalid tag expression for %q: %w", name, err)
	}
	return &TagFilteredIndex{
		name: name,
		expr: expr,
		data: []NodeIndexEntry{},
	}, nil
}

// Name returns the short index filename used with repo.WriteIndex.
func (idx *TagFilteredIndex) Name() string {
	if idx == nil {
		return ""
	}
	return idx.name
}

// Add evaluates the tag expression against the node and, if it matches,
// inserts or updates the node entry maintaining reverse-chronological order.
// A node matches when EvaluateTagExpression returns a non-empty set.
func (idx *TagFilteredIndex) Add(ctx context.Context, data *NodeData) error {
	_ = ctx
	if idx == nil || data == nil {
		return nil
	}

	path := data.ID.Path()
	universe := map[string]struct{}{path: {}}

	nodeTags := data.Tags()
	tagSet := make(map[string]struct{}, len(nodeTags))
	for _, t := range nodeTags {
		tagSet[t] = struct{}{}
	}

	result := EvaluateTagExpression(idx.expr, universe, func(tag string) map[string]struct{} {
		if _, ok := tagSet[tag]; ok {
			return map[string]struct{}{path: {}}
		}
		return map[string]struct{}{}
	})

	entry := data.Ref()

	if len(result) == 0 {
		// Node does not match; ensure it is not in the index.
		return idx.Remove(ctx, data.ID)
	}

	// Upsert: replace existing entry or append.
	for i := range idx.data {
		if idx.data[i].ID == entry.ID {
			idx.data[i] = entry
			sort.SliceStable(idx.data, func(a, b int) bool {
				return idx.data[a].Updated.After(idx.data[b].Updated)
			})
			return nil
		}
	}
	idx.data = append(idx.data, entry)
	sort.SliceStable(idx.data, func(a, b int) bool {
		return idx.data[a].Updated.After(idx.data[b].Updated)
	})
	return nil
}

// Remove removes the node identified by node from the index. If the node is
// not present the call is a no-op.
func (idx *TagFilteredIndex) Remove(ctx context.Context, node NodeId) error {
	_ = ctx
	if idx == nil || idx.data == nil {
		return nil
	}
	target := node.Path()
	for i := range idx.data {
		if idx.data[i].ID == target {
			idx.data = append(idx.data[:i], idx.data[i+1:]...)
			return nil
		}
	}
	return nil
}

// Clear resets the index to an empty state.
func (idx *TagFilteredIndex) Clear(ctx context.Context) error {
	_ = ctx
	if idx == nil {
		return nil
	}
	idx.data = []NodeIndexEntry{}
	return nil
}

// Data serializes the TagFilteredIndex to the same markdown format as
// ChangesIndex.Data. Entries are in reverse-chronological order.
func (idx *TagFilteredIndex) Data(ctx context.Context) ([]byte, error) {
	_ = ctx
	if idx == nil || len(idx.data) == 0 {
		return []byte{}, nil
	}
	var b strings.Builder
	for _, e := range idx.data {
		b.WriteString("* ")
		if !e.Updated.IsZero() {
			b.WriteString(e.Updated.UTC().Format(changesTimeFmt))
		} else {
			b.WriteString("0001-01-01 00:00:00Z")
		}
		b.WriteByte(' ')
		b.WriteByte('[')
		b.WriteString(e.Title)
		b.WriteString("](../")
		b.WriteString(e.ID)
		b.WriteByte(')')
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}
