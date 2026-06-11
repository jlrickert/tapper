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
//   - YYYY-MM-DD HH:MM:SSZ [TITLE](../ID)
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
	title := rest[1:sep]            // skip leading "["
	id := rest[sep+5 : len(rest)-1] // skip "](../" and trailing ")"

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
//   - YYYY-MM-DD HH:MM:SSZ [TITLE](../ID)
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
// Core index names
// --------------------------------------------------------------------------

// coreIndexNames is the set of built-in index filenames (bare form) that
// cannot be overridden by config-driven query-filtered indexes.
var coreIndexNames = map[string]bool{
	"changes.md": true,
	"nodes.tsv":  true,
	"links":      true,
	"backlinks":  true,
	"tags":       true,
}

// IsCoreIndex reports whether the given index file name is one of the
// built-in protected index names (e.g. "changes.md").
func IsCoreIndex(name string) bool {
	return coreIndexNames[name]
}

// --------------------------------------------------------------------------
// QueryFilteredIndex
// --------------------------------------------------------------------------

// QueryFilteredIndex is an in-memory index of nodes that match a boolean query
// expression. It supports the full query expression system: tag names,
// key=value attribute predicates, boolean operators (and/or/not), and
// parenthesized grouping.
//
// The resolve callback, when non-nil, is called for each term in the query
// expression with each candidate node. This allows higher-level packages
// (e.g. pkg/tapper) to inject attribute predicate support without creating a
// dependency from pkg/keg to pkg/tapper.
//
// When resolve is nil, the index falls back to tag-only matching (each term
// is evaluated as a tag name against the node's tag set).
//
// Concurrency note: QueryFilteredIndex does not perform internal
// synchronization. Callers should guard access with a mutex when needed.
// QueryFilteredSortOrder controls the sort order of entries in a
// QueryFilteredIndex.
type QueryFilteredSortOrder string

const (
	// QFSortUpdated sorts by Updated descending (newest first). This is the
	// default when no sort order is specified.
	QFSortUpdated QueryFilteredSortOrder = ""
	// QFSortID sorts by node ID ascending.
	QFSortID QueryFilteredSortOrder = "id"
	// QFSortCreated sorts by Created descending (newest first).
	QFSortCreated QueryFilteredSortOrder = "created"
	// QFSortAccessed sorts by Accessed descending (newest first).
	QFSortAccessed QueryFilteredSortOrder = "accessed"
)

type QueryFilteredIndex struct {
	// name is the short index filename used with repo.WriteIndex, e.g. "golang.md".
	name string
	// expr is the compiled query expression evaluated per Add call.
	expr QueryExpr
	// resolve is an optional callback that evaluates a single query term
	// against a node. When nil, terms are matched as tag names only.
	resolve func(term string, data *NodeData) bool
	// sortOrder controls how matched entries are ordered.
	sortOrder QueryFilteredSortOrder
	// data holds matched entries sorted according to sortOrder.
	data []NodeIndexEntry
}

// NewQueryFilteredIndex creates a QueryFilteredIndex for the given index file
// name and boolean query string. The optional resolve callback enables
// key=value attribute predicates and other term types.
//
// When resolve is nil, terms are evaluated as tag names against the node's
// tag set.
//
// name should be the bare filename used when writing to the repository, e.g.
// "golang.md".
func NewQueryFilteredIndex(name, query string, resolve func(term string, data *NodeData) bool) (*QueryFilteredIndex, error) {
	return NewQueryFilteredIndexWithSort(name, query, resolve, QFSortUpdated)
}

// NewQueryFilteredIndexWithSort creates a QueryFilteredIndex with an explicit
// sort order. The sortOrder parameter accepts "id", "created", "accessed", or
// empty string (default: sort by Updated descending).
func NewQueryFilteredIndexWithSort(name, query string, resolve func(term string, data *NodeData) bool, sortOrder QueryFilteredSortOrder) (*QueryFilteredIndex, error) {
	expr, err := ParseQueryExpression(query)
	if err != nil {
		return nil, fmt.Errorf("invalid query expression for %q: %w", name, err)
	}
	return &QueryFilteredIndex{
		name:      name,
		expr:      expr,
		resolve:   resolve,
		sortOrder: sortOrder,
		data:      []NodeIndexEntry{},
	}, nil
}

// Name returns the short index filename used with repo.WriteIndex.
func (idx *QueryFilteredIndex) Name() string {
	if idx == nil {
		return ""
	}
	return idx.name
}

// Add evaluates the query expression against the node and, if it matches,
// inserts or updates the node entry maintaining the configured sort order.
func (idx *QueryFilteredIndex) Add(ctx context.Context, data *NodeData) error {
	_ = ctx
	if idx == nil || data == nil {
		return nil
	}

	path := data.ID.Path()
	universe := map[string]struct{}{path: {}}

	resolve := idx.resolverForNode(data)
	result := EvaluateQueryExpression(idx.expr, universe, resolve)
	entry := data.Ref()

	if len(result) == 0 {
		// Node does not match; ensure it is not in the index.
		return idx.Remove(ctx, data.ID)
	}

	// Upsert: replace existing entry or append.
	for i := range idx.data {
		if idx.data[i].ID == entry.ID {
			idx.data[i] = entry
			idx.sortData()
			return nil
		}
	}
	idx.data = append(idx.data, entry)
	idx.sortData()
	return nil
}

// sortData sorts idx.data according to idx.sortOrder.
func (idx *QueryFilteredIndex) sortData() {
	switch idx.sortOrder {
	case QFSortID:
		sort.SliceStable(idx.data, func(a, b int) bool {
			na, ea := ParseNode(idx.data[a].ID)
			nb, eb := ParseNode(idx.data[b].ID)
			if ea == nil && eb == nil && na != nil && nb != nil {
				return na.Compare(*nb) < 0
			}
			return idx.data[a].ID < idx.data[b].ID
		})
	case QFSortCreated:
		sort.SliceStable(idx.data, func(a, b int) bool {
			return idx.data[a].Created.After(idx.data[b].Created)
		})
	case QFSortAccessed:
		sort.SliceStable(idx.data, func(a, b int) bool {
			return idx.data[a].Accessed.After(idx.data[b].Accessed)
		})
	default:
		// QFSortUpdated or empty: sort by Updated descending.
		sort.SliceStable(idx.data, func(a, b int) bool {
			return idx.data[a].Updated.After(idx.data[b].Updated)
		})
	}
}

// resolverForNode returns a tag expression resolver function for a single node.
// When idx.resolve is set, it delegates each term to the injected callback.
// Otherwise, it falls back to tag-only matching.
func (idx *QueryFilteredIndex) resolverForNode(data *NodeData) func(term string) map[string]struct{} {
	path := data.ID.Path()

	if idx.resolve != nil {
		return func(term string) map[string]struct{} {
			if idx.resolve(term, data) {
				return map[string]struct{}{path: {}}
			}
			return map[string]struct{}{}
		}
	}

	// Default: tag-only matching.
	nodeTags := data.Tags()
	tagSet := make(map[string]struct{}, len(nodeTags))
	for _, t := range nodeTags {
		tagSet[t] = struct{}{}
	}
	return func(term string) map[string]struct{} {
		if _, ok := tagSet[term]; ok {
			return map[string]struct{}{path: {}}
		}
		return map[string]struct{}{}
	}
}

// Remove removes the node identified by node from the index. If the node is
// not present the call is a no-op.
func (idx *QueryFilteredIndex) Remove(ctx context.Context, node NodeId) error {
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
func (idx *QueryFilteredIndex) Clear(ctx context.Context) error {
	_ = ctx
	if idx == nil {
		return nil
	}
	idx.data = []NodeIndexEntry{}
	return nil
}

// Data serializes the QueryFilteredIndex to the same markdown format as
// ChangesIndex.Data. Entries are in reverse-chronological order.
func (idx *QueryFilteredIndex) Data(ctx context.Context) ([]byte, error) {
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
