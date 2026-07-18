package keg

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/clock"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/stretchr/testify/require"
)

func TestTimelineIndex_OrdersSnapshotRowsAndReplaysBacklinks(t *testing.T) {
	t.Parallel()

	k, rt := newSnapshotIndexTestKeg(t)
	ctx := t.Context()

	t1 := time.Date(2026, 2, 26, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t2.Add(time.Hour)

	setSnapshotIndexClock(t, rt, t1)
	alphaResult, err := k.Create(ctx, &CreateOptions{
		Body: []byte("---\ntype: task\n---\n# Alpha\n\nSee [three](../3).\n"),
	})
	require.NoError(t, err)
	alpha := alphaResult.ID
	alphaSnap1, err := k.AppendSnapshot(ctx, alpha, "initial")
	require.NoError(t, err)

	setSnapshotIndexClock(t, rt, t2)
	betaResult, err := k.Create(ctx, &CreateOptions{
		Body: []byte("# Beta\n\nSee [alpha](../1).\n"),
	})
	require.NoError(t, err)
	beta := betaResult.ID
	_, err = k.AppendSnapshot(ctx, beta, "beta")
	require.NoError(t, err)

	setSnapshotIndexClock(t, rt, t3)
	require.NoError(t, k.SetContent(ctx, beta, []byte("# Beta\n\nunsnapshotted live edit\n")))
	require.NoError(t, k.SetContent(ctx, alpha, []byte("---\ntype: task\n---\n# Alpha\n\nSee [beta](../2).\n")))
	alphaSnap2, err := k.AppendSnapshot(ctx, alpha, "update link")
	require.NoError(t, err)

	raw, err := k.buildTimelineIndexData(ctx)
	require.NoError(t, err)
	require.False(t, bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")), "timeline must be JSON Lines, not a JSON array")

	rows := decodeJSONLines[timelineIndexRow](t, raw)
	require.Len(t, rows, 3, "unsnapshotted live edits must not create timeline rows")
	require.Equal(t, []string{
		t1.Format(time.RFC3339),
		t2.Format(time.RFC3339),
		t3.Format(time.RFC3339),
	}, []string{rows[0].OccurredAt, rows[1].OccurredAt, rows[2].OccurredAt})

	require.Equal(t, "1", rows[0].Node)
	require.Equal(t, int64(alphaSnap1.ID), rows[0].Revision)
	require.Equal(t, "task", rows[0].Schema)
	require.Equal(t, "Alpha", rows[0].Title)
	require.Equal(t, []timelineNodeRef{{Node: "3"}}, rows[0].Links)
	require.Empty(t, rows[0].Backlinks)

	require.Equal(t, "2", rows[1].Node)
	require.Equal(t, []timelineNodeRef{{Node: "1"}}, rows[1].Links, "node 2 row should use its persisted snapshot links, not the later live edit")

	require.Equal(t, "1", rows[2].Node)
	require.Equal(t, int64(alphaSnap2.ID), rows[2].Revision)
	require.Equal(t, int64(alphaSnap1.ID), rows[2].Parent)
	require.Equal(t, "update link", rows[2].Message)
	require.Equal(t, []timelineNodeRef{{Node: "2"}}, rows[2].Links)
	require.Equal(t, []timelineNodeRef{{Node: "2"}}, rows[2].Backlinks)
}

func TestDirtyIndex_DetectsNoSnapshotsMatchingLatestAndStaleStats(t *testing.T) {
	t.Parallel()

	k, _ := newSnapshotIndexTestKeg(t)
	ctx := t.Context()

	created, err := k.Create(ctx, &CreateOptions{Body: []byte("# Alpha\n\nbody\n")})
	require.NoError(t, err)
	id := created.ID

	rows := dirtyRowsByNode(t, k)
	require.Contains(t, rows, id.Path())
	require.Equal(t, int64(0), rows[id.Path()].SnapshotRevision)
	require.Empty(t, rows[id.Path()].SnapshotHash)
	require.NotEmpty(t, rows[id.Path()].CurrentHash)

	snap, err := k.AppendSnapshot(ctx, id, "clean point")
	require.NoError(t, err)
	rows = dirtyRowsByNode(t, k)
	require.NotContains(t, rows, id.Path(), "matching latest snapshot hash should mark node clean")

	// Bypass LocalKeg so stats.json remains at the old snapshot hash. Dirty
	// detection must read current content and not trust stale stats.
	changed := []byte("# Alpha Changed\n\nbody\n")
	require.NoError(t, k.Repo.WriteContent(ctx, id, changed))

	rows = dirtyRowsByNode(t, k)
	require.Contains(t, rows, id.Path())
	require.Equal(t, int64(snap.ID), rows[id.Path()].SnapshotRevision)
	require.Equal(t, snap.ContentHash, rows[id.Path()].SnapshotHash)
	require.NotEqual(t, snap.ContentHash, rows[id.Path()].CurrentHash)
	require.Equal(t, "Alpha Changed", rows[id.Path()].Title)
}

func TestTimelineIndex_EmitsOmegaUpdatesAndIndexPersistsFinalOmega(t *testing.T) {
	t.Parallel()

	k, rt := newSnapshotIndexTestKeg(t)
	ctx := t.Context()

	require.NoError(t, k.WriteSchema(ctx, "evidence", []byte(`type: evidence
meta:
  type: object
  properties:
    type:
      const: evidence
markdown:
  requireTitle: true
`)))
	require.NoError(t, k.WriteSchema(ctx, "note", []byte(`type: note
relations:
  - name: support
    type: evidence
    maturity:
      - direction: links
        attribute: status
        weight: 2
        enum:
          draft: 0.25
          ready: 1
  - name: review
    type: evidence
    maturity:
      - direction: backlinks
        attribute: certainty
        weight: 1
markdown:
  requireTitle: true
`)))

	t1 := time.Date(2026, 2, 26, 10, 0, 0, 0, time.UTC)
	setSnapshotIndexClock(t, rt, t1)
	evidenceResult, err := k.Create(ctx, &CreateOptions{Title: "Evidence", Attrs: map[string]any{"type": "evidence", "status": "ready"}})
	require.NoError(t, err)
	evidence := evidenceResult.ID
	_, err = k.AppendSnapshot(ctx, evidence, "evidence")
	require.NoError(t, err)

	setSnapshotIndexClock(t, rt, t1.Add(time.Hour))
	sourceResult, err := k.Create(ctx, &CreateOptions{
		Body:  []byte("# Source\n\n[Evidence](../" + evidence.Path() + ")\n"),
		Attrs: map[string]any{"type": "note"},
	})
	require.NoError(t, err)
	source := sourceResult.ID
	_, err = k.AppendSnapshot(ctx, source, "source")
	require.NoError(t, err)

	setSnapshotIndexClock(t, rt, t1.Add(2*time.Hour))
	reviewResult, err := k.Create(ctx, &CreateOptions{
		Body:  []byte("# Review\n\n[Source](../" + source.Path() + ")\n"),
		Attrs: map[string]any{"type": "evidence", "certainty": 0.5},
	})
	require.NoError(t, err)
	review := reviewResult.ID
	_, err = k.AppendSnapshot(ctx, review, "review")
	require.NoError(t, err)

	raw, err := k.buildTimelineIndexData(ctx)
	require.NoError(t, err)
	rows := decodeJSONLines[timelineIndexRow](t, raw)
	require.Len(t, rows, 3)
	require.Nil(t, rows[0].Omega, "evidence has no maturity weights")

	require.NotNil(t, rows[1].Omega, "source event should carry its computed omega")
	require.InDelta(t, 2.0/3.0, *rows[1].Omega, 0.000001)
	require.Equal(t, []string{evidence.Path(), source.Path()}, timelineUpdateNodes(rows[1].OmegaUpdates))
	require.Nil(t, rows[1].OmegaUpdates[0].Omega)
	require.NotNil(t, rows[1].OmegaUpdates[1].Omega)

	require.Nil(t, rows[2].Omega, "review event itself has no omega")
	require.Equal(t, []string{source.Path(), review.Path()}, timelineUpdateNodes(rows[2].OmegaUpdates))
	require.NotNil(t, rows[2].OmegaUpdates[0].Omega)
	require.InDelta(t, 2.5/3.0, *rows[2].OmegaUpdates[0].Omega, 0.000001)
	require.Nil(t, rows[2].OmegaUpdates[1].Omega)

	stats, err := k.GetStats(ctx, source)
	require.NoError(t, err)
	omega, ok := stats.Omega()
	require.True(t, ok)
	require.InDelta(t, 2.5/3.0, omega, 0.000001)

	stats.ClearOmega()
	require.NoError(t, k.Repo.WriteStats(ctx, source, stats))
	require.NoError(t, k.Index(ctx, IndexOptions{}))
	stats, err = k.GetStats(ctx, source)
	require.NoError(t, err)
	omega, ok = stats.Omega()
	require.True(t, ok)
	require.InDelta(t, 2.5/3.0, omega, 0.000001)
}

func newSnapshotIndexTestKeg(t *testing.T) (*LocalKeg, *toolkit.Runtime) {
	t.Helper()

	rt, err := toolkit.NewTestRuntime(t.TempDir(), "/home/testuser", "testuser")
	require.NoError(t, err)
	repo := NewMemoryRepo(rt)
	k := NewLocalKeg(repo, rt)
	require.NoError(t, k.Init(t.Context()))
	return k, rt
}

func setSnapshotIndexClock(t *testing.T, rt *toolkit.Runtime, at time.Time) {
	t.Helper()
	clk, ok := rt.Clock().(*clock.TestClock)
	require.True(t, ok, "test runtime should use clock.TestClock")
	clk.Set(at)
}

func dirtyRowsByNode(t *testing.T, k *LocalKeg) map[string]dirtyIndexRow {
	t.Helper()
	raw, err := k.buildDirtyIndexData(t.Context())
	require.NoError(t, err)
	rows := decodeJSONLines[dirtyIndexRow](t, raw)
	out := make(map[string]dirtyIndexRow, len(rows))
	for _, row := range rows {
		out[row.Node] = row
	}
	return out
}

func decodeJSONLines[T any](t *testing.T, raw []byte) []T {
	t.Helper()
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	out := make([]T, 0, len(lines))
	for _, line := range lines {
		var row T
		require.NoError(t, json.Unmarshal([]byte(line), &row), "line: %s", line)
		out = append(out, row)
	}
	return out
}

func timelineUpdateNodes(updates []timelineOmegaUpdate) []string {
	out := make([]string, 0, len(updates))
	for _, update := range updates {
		out = append(out, update.Node)
	}
	return out
}
