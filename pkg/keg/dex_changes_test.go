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
		"* 2025-09-18 00:51:16Z [Zekia extension to keg configuration](../24)\n"

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
// TagFilteredIndex tests
// --------------------------------------------------------------------------

func TestTagFilteredIndex_NewError(t *testing.T) {
	t.Parallel()

	_, err := NewTagFilteredIndex("golang.md", "")
	require.Error(t, err, "empty tag query should return error")

	_, err = NewTagFilteredIndex("golang.md", "a and (b")
	require.Error(t, err, "invalid expression should return error")
}

func TestTagFilteredIndex_MatchAndExclude(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	idx, err := NewTagFilteredIndex("golang.md", "golang")
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

func TestTagFilteredIndex_AndExpression(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	idx, err := NewTagFilteredIndex("golang-tricks.md", "golang && trick")
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

func TestTagFilteredIndex_Remove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	idx, err := NewTagFilteredIndex("golang.md", "golang")
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

func TestTagFilteredIndex_Name(t *testing.T) {
	t.Parallel()

	idx, err := NewTagFilteredIndex("my-index.md", "golang")
	require.NoError(t, err)
	require.Equal(t, "my-index.md", idx.Name())
}
