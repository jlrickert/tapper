package keg

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// makeNodeData is a test helper that constructs a minimal NodeData with the
// given numeric ID, title, tags, and updated timestamp.
func makeNodeData(id int, title string, tags []string, updated time.Time) *NodeData {
	ctx := context.Background()
	meta := NewMeta(ctx, time.Time{})
	meta.SetTags(tags)

	stats := NewStats(updated)
	stats.SetTitle(title)
	stats.SetUpdated(updated)

	return &NodeData{
		ID:    NodeId{ID: id},
		Meta:  meta,
		Stats: stats,
	}
}

// --------------------------------------------------------------------------
// ChangesIndex tests
// --------------------------------------------------------------------------

func TestChangesIndex_AddAndData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t1 := time.Date(2025, 10, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 10, 3, 8, 0, 0, 0, time.UTC)
	t3 := time.Date(2025, 9, 15, 6, 0, 0, 0, time.UTC)

	n1 := makeNodeData(1, "First Node", nil, t1)
	n2 := makeNodeData(2, "Second Node", nil, t2)
	n3 := makeNodeData(3, "Third Node", nil, t3)

	var idx ChangesIndex
	require.NoError(t, idx.Add(ctx, n1))
	require.NoError(t, idx.Add(ctx, n2))
	require.NoError(t, idx.Add(ctx, n3))

	data, err := idx.Data(ctx)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 3)

	// Entries should be newest first: n2 (t2), n1 (t1), n3 (t3)
	require.Contains(t, lines[0], "[Second Node](../2)", "first line should be newest")
	require.Contains(t, lines[1], "[First Node](../1)")
	require.Contains(t, lines[2], "[Third Node](../3)", "last line should be oldest")

	// Verify timestamp format in first line
	require.True(t, strings.HasPrefix(lines[0], "* 2025-10-03 08:00:00Z "))
}

func TestChangesIndex_UpdateExisting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	var idx ChangesIndex
	require.NoError(t, idx.Add(ctx, makeNodeData(5, "Old Title", nil, t1)))
	require.NoError(t, idx.Add(ctx, makeNodeData(5, "New Title", nil, t2)))

	data, err := idx.Data(ctx)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 1, "duplicate node should replace, not append")
	require.Contains(t, lines[0], "New Title")
	require.Contains(t, lines[0], "2025-06-01")
}

func TestChangesIndex_Remove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t1 := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 5, 2, 0, 0, 0, 0, time.UTC)

	var idx ChangesIndex
	require.NoError(t, idx.Add(ctx, makeNodeData(10, "Keep Me", nil, t1)))
	require.NoError(t, idx.Add(ctx, makeNodeData(11, "Remove Me", nil, t2)))

	require.NoError(t, idx.Rm(ctx, NodeId{ID: 11}))

	data, err := idx.Data(ctx)
	require.NoError(t, err)

	s := string(data)
	require.Contains(t, s, "Keep Me")
	require.NotContains(t, s, "Remove Me")
}

func TestChangesIndex_Clear(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var idx ChangesIndex
	require.NoError(t, idx.Add(ctx, makeNodeData(1, "Node", nil, time.Now())))
	require.NoError(t, idx.Clear(ctx))

	data, err := idx.Data(ctx)
	require.NoError(t, err)
	require.Empty(t, data)
}

func TestChangesIndex_ParseAndRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	raw := "* 2025-10-03 20:52:37Z [Tap CLI application (`tap`)](../31)\n" +
		"* 2025-09-18 00:51:16Z [Zekia extension to keg settings](../24)\n"

	idx, err := ParseChangesIndex(ctx, []byte(raw))
	require.NoError(t, err)
	require.Len(t, idx.data, 2)

	require.Equal(t, "31", idx.data[0].ID)
	require.Equal(t, "Tap CLI application (`tap`)", idx.data[0].Title)
	require.Equal(t, "24", idx.data[1].ID)

	// Round-trip: Data() should reproduce the same text.
	out, err := idx.Data(ctx)
	require.NoError(t, err)
	require.Equal(t, raw, string(out))
}

func TestChangesIndex_ParseMalformed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	raw := "bad line\n" +
		"* not-a-timestamp [Title](../1)\n" +
		"* 2025-10-03 20:52:37Z [Valid](../5)\n"

	idx, err := ParseChangesIndex(ctx, []byte(raw))
	require.NoError(t, err)
	// Only the valid line should be parsed.
	require.Len(t, idx.data, 1)
	require.Equal(t, "5", idx.data[0].ID)
}

// --------------------------------------------------------------------------
// QueryFilteredIndex tests
// --------------------------------------------------------------------------

func TestQueryFilteredIndex_NewError(t *testing.T) {
	t.Parallel()

	_, err := NewQueryFilteredIndex("golang.md", "", nil)
	require.Error(t, err, "empty query should return error")

	_, err = NewQueryFilteredIndex("golang.md", "a and (b", nil)
	require.Error(t, err, "invalid expression should return error")
}

func TestQueryFilteredIndex_TagOnlyFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// With a nil resolver, QueryFilteredIndex matches on tags only.
	idx, err := NewQueryFilteredIndex("golang.md", "golang", nil)
	require.NoError(t, err)

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)

	goNode := makeNodeData(1, "Go tricks", []string{"golang", "trick"}, t1)
	pyNode := makeNodeData(2, "Python tricks", []string{"python", "trick"}, t2)

	require.NoError(t, idx.Add(ctx, goNode))
	require.NoError(t, idx.Add(ctx, pyNode))

	data, err := idx.Data(ctx)
	require.NoError(t, err)

	s := string(data)
	require.Contains(t, s, "Go tricks", "golang node should be included")
	require.NotContains(t, s, "Python tricks", "python node should be excluded")
}

func TestQueryFilteredIndex_WithResolver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Custom resolver that supports key=value attribute predicates.
	resolve := func(term string, data *NodeData) bool {
		if strings.Contains(term, "=") {
			parts := strings.SplitN(term, "=", 2)
			if data.Meta == nil {
				return false
			}
			got, ok := data.Meta.Get(parts[0])
			return ok && got == parts[1]
		}
		// Default: tag matching
		for _, tag := range data.Tags() {
			if tag == term {
				return true
			}
		}
		return false
	}

	idx, err := NewQueryFilteredIndex("favorites.md", "entity=concept", resolve)
	require.NoError(t, err)

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	conceptNode := makeNodeDataWithAttr(1, "Go concept", []string{"golang"}, t1, map[string]any{"entity": "concept"})
	taskNode := makeNodeDataWithAttr(2, "Go task", []string{"golang"}, t1, map[string]any{"entity": "task"})
	noEntityNode := makeNodeData(3, "No entity", []string{"golang"}, t1)

	require.NoError(t, idx.Add(ctx, conceptNode))
	require.NoError(t, idx.Add(ctx, taskNode))
	require.NoError(t, idx.Add(ctx, noEntityNode))

	data, err := idx.Data(ctx)
	require.NoError(t, err)

	s := string(data)
	require.Contains(t, s, "Go concept", "concept node should match entity=concept")
	require.NotContains(t, s, "Go task", "task node should not match entity=concept")
	require.NotContains(t, s, "No entity", "node without entity should not match")
}

func TestQueryFilteredIndex_AndExpression(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	idx, err := NewQueryFilteredIndex("golang-tricks.md", "golang && trick", nil)
	require.NoError(t, err)

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	goTrick := makeNodeData(1, "Go trick", []string{"golang", "trick"}, t1)
	goOnly := makeNodeData(2, "Go only", []string{"golang"}, t1)
	trickOnly := makeNodeData(3, "Trick only", []string{"trick"}, t1)

	require.NoError(t, idx.Add(ctx, goTrick))
	require.NoError(t, idx.Add(ctx, goOnly))
	require.NoError(t, idx.Add(ctx, trickOnly))

	data, err := idx.Data(ctx)
	require.NoError(t, err)

	s := string(data)
	require.Contains(t, s, "Go trick")
	require.NotContains(t, s, "Go only")
	require.NotContains(t, s, "Trick only")
}

func TestQueryFilteredIndex_Remove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	idx, err := NewQueryFilteredIndex("golang.md", "golang", nil)
	require.NoError(t, err)

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, idx.Add(ctx, makeNodeData(1, "Go A", []string{"golang"}, t1)))
	require.NoError(t, idx.Add(ctx, makeNodeData(2, "Go B", []string{"golang"}, t2)))

	require.NoError(t, idx.Remove(ctx, NodeId{ID: 1}))

	data, err := idx.Data(ctx)
	require.NoError(t, err)
	s := string(data)
	require.NotContains(t, s, "Go A")
	require.Contains(t, s, "Go B")
}

func TestQueryFilteredIndex_Name(t *testing.T) {
	t.Parallel()

	idx, err := NewQueryFilteredIndex("my-index.md", "golang", nil)
	require.NoError(t, err)
	require.Equal(t, "my-index.md", idx.Name())
}

func TestQueryFilteredIndex_Clear(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	idx, err := NewQueryFilteredIndex("test.md", "golang", nil)
	require.NoError(t, err)

	require.NoError(t, idx.Add(ctx, makeNodeData(1, "Node", []string{"golang"}, time.Now())))
	require.NoError(t, idx.Clear(ctx))

	data, err := idx.Data(ctx)
	require.NoError(t, err)
	require.Empty(t, data)
}

func TestQueryFilteredIndex_MixedResolverTerms(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Resolver supports both tags and key=value attributes.
	resolve := func(term string, data *NodeData) bool {
		if strings.Contains(term, "=") {
			parts := strings.SplitN(term, "=", 2)
			if data.Meta == nil {
				return false
			}
			got, ok := data.Meta.Get(parts[0])
			return ok && got == parts[1]
		}
		for _, tag := range data.Tags() {
			if tag == term {
				return true
			}
		}
		return false
	}

	// Query: must be entity=concept AND have the golang tag
	idx, err := NewQueryFilteredIndex("go-concepts.md", "entity=concept && golang", resolve)
	require.NoError(t, err)

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	goConcept := makeNodeDataWithAttr(1, "Go Concurrency", []string{"golang"}, t1, map[string]any{"entity": "concept"})
	goTask := makeNodeDataWithAttr(2, "Go Refactor", []string{"golang"}, t1, map[string]any{"entity": "task"})
	pyConcept := makeNodeDataWithAttr(3, "Python Concept", []string{"python"}, t1, map[string]any{"entity": "concept"})

	require.NoError(t, idx.Add(ctx, goConcept))
	require.NoError(t, idx.Add(ctx, goTask))
	require.NoError(t, idx.Add(ctx, pyConcept))

	data, err := idx.Data(ctx)
	require.NoError(t, err)

	s := string(data)
	require.Contains(t, s, "Go Concurrency", "matches both entity=concept and golang")
	require.NotContains(t, s, "Go Refactor", "entity=task should not match")
	require.NotContains(t, s, "Python Concept", "missing golang tag should not match")
}

// makeNodeDataWithAttr is a test helper that extends makeNodeData with
// arbitrary metadata attributes (e.g. entity, status).
func makeNodeDataWithAttr(id int, title string, tags []string, updated time.Time, attrs map[string]any) *NodeData {
	nd := makeNodeData(id, title, tags, updated)
	if nd.Meta != nil && len(attrs) > 0 {
		ctx := context.Background()
		nd.Meta.SetAttrs(ctx, attrs)
	}
	return nd
}

// makeNodeDataWithTimes is a test helper that constructs a NodeData with
// distinct updated, created, and accessed timestamps.
func makeNodeDataWithTimes(id int, title string, tags []string, updated, created, accessed time.Time) *NodeData {
	nd := makeNodeData(id, title, tags, updated)
	nd.Stats.SetCreated(created)
	nd.Stats.SetAccessed(accessed)
	return nd
}

// --------------------------------------------------------------------------
// QueryFilteredIndex sort tests
// --------------------------------------------------------------------------

func TestQueryFilteredIndex_SortByID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	idx, err := NewQueryFilteredIndexWithSort("test.md", "golang", nil, QFSortID)
	require.NoError(t, err)

	t1 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)

	// Add nodes in non-ID order to verify sorting.
	require.NoError(t, idx.Add(ctx, makeNodeData(3, "Node C", []string{"golang"}, t1)))
	require.NoError(t, idx.Add(ctx, makeNodeData(1, "Node A", []string{"golang"}, t1)))
	require.NoError(t, idx.Add(ctx, makeNodeData(2, "Node B", []string{"golang"}, t1)))

	data, err := idx.Data(ctx)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 3)
	require.Contains(t, lines[0], "[Node A](../1)", "first entry should be ID 1")
	require.Contains(t, lines[1], "[Node B](../2)", "second entry should be ID 2")
	require.Contains(t, lines[2], "[Node C](../3)", "third entry should be ID 3")
}

func TestQueryFilteredIndex_SortByUpdated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// QFSortUpdated is the default (empty string).
	idx, err := NewQueryFilteredIndexWithSort("test.md", "golang", nil, QFSortUpdated)
	require.NoError(t, err)

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, idx.Add(ctx, makeNodeData(1, "Old", []string{"golang"}, t1)))
	require.NoError(t, idx.Add(ctx, makeNodeData(2, "Newest", []string{"golang"}, t2)))
	require.NoError(t, idx.Add(ctx, makeNodeData(3, "Middle", []string{"golang"}, t3)))

	data, err := idx.Data(ctx)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 3)
	// Updated descending: newest first.
	require.Contains(t, lines[0], "[Newest](../2)", "newest updated should be first")
	require.Contains(t, lines[1], "[Middle](../3)", "middle updated should be second")
	require.Contains(t, lines[2], "[Old](../1)", "oldest updated should be last")
}

func TestQueryFilteredIndex_SortByCreated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	idx, err := NewQueryFilteredIndexWithSort("test.md", "golang", nil, QFSortCreated)
	require.NoError(t, err)

	// All nodes have the same updated time but different created times.
	baseUpdated := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	c1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC) // oldest created
	c2 := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC) // newest created
	c3 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC) // middle created
	baseAccessed := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, idx.Add(ctx, makeNodeDataWithTimes(1, "Oldest Created", []string{"golang"}, baseUpdated, c1, baseAccessed)))
	require.NoError(t, idx.Add(ctx, makeNodeDataWithTimes(2, "Newest Created", []string{"golang"}, baseUpdated, c2, baseAccessed)))
	require.NoError(t, idx.Add(ctx, makeNodeDataWithTimes(3, "Middle Created", []string{"golang"}, baseUpdated, c3, baseAccessed)))

	data, err := idx.Data(ctx)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 3)
	// Created descending: newest first.
	require.Contains(t, lines[0], "[Newest Created](../2)", "newest created should be first")
	require.Contains(t, lines[1], "[Middle Created](../3)", "middle created should be second")
	require.Contains(t, lines[2], "[Oldest Created](../1)", "oldest created should be last")
}

func TestQueryFilteredIndex_SortByAccessed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	idx, err := NewQueryFilteredIndexWithSort("test.md", "golang", nil, QFSortAccessed)
	require.NoError(t, err)

	// All nodes have the same updated/created times but different accessed times.
	baseUpdated := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	baseCreated := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	a1 := time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC) // oldest accessed
	a2 := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC) // newest accessed
	a3 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC) // middle accessed

	require.NoError(t, idx.Add(ctx, makeNodeDataWithTimes(1, "Oldest Accessed", []string{"golang"}, baseUpdated, baseCreated, a1)))
	require.NoError(t, idx.Add(ctx, makeNodeDataWithTimes(2, "Newest Accessed", []string{"golang"}, baseUpdated, baseCreated, a2)))
	require.NoError(t, idx.Add(ctx, makeNodeDataWithTimes(3, "Middle Accessed", []string{"golang"}, baseUpdated, baseCreated, a3)))

	data, err := idx.Data(ctx)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 3)
	// Accessed descending: newest first.
	require.Contains(t, lines[0], "[Newest Accessed](../2)", "newest accessed should be first")
	require.Contains(t, lines[1], "[Middle Accessed](../3)", "middle accessed should be second")
	require.Contains(t, lines[2], "[Oldest Accessed](../1)", "oldest accessed should be last")
}

func TestQueryFilteredIndex_DefaultSortIsUpdated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// NewQueryFilteredIndex (without explicit sort) should default to updated descending.
	idx, err := NewQueryFilteredIndex("test.md", "golang", nil)
	require.NoError(t, err)

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, idx.Add(ctx, makeNodeData(1, "Older", []string{"golang"}, t1)))
	require.NoError(t, idx.Add(ctx, makeNodeData(2, "Newer", []string{"golang"}, t2)))

	data, err := idx.Data(ctx)
	require.NoError(t, err)

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], "[Newer](../2)", "default sort should place newest updated first")
	require.Contains(t, lines[1], "[Older](../1)", "default sort should place oldest updated last")
}
