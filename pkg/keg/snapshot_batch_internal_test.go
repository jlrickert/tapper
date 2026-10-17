package keg

import (
	"context"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/stretchr/testify/require"
)

var snapshotBatchStart = time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

// newSnapshotBatchKeg builds a keg whose omega values depend on each other:
// a note scored by the evidence it links to and by the evidence linking back
// to it, with one snapshot of history already present. It returns the node
// ids in snapshot order.
func newSnapshotBatchKeg(t *testing.T) (*LocalKeg, *toolkit.Runtime, []NodeId) {
	t.Helper()
	k, rt := newSnapshotIndexTestKeg(t)
	ctx := t.Context()
	setSnapshotIndexClock(t, rt, snapshotBatchStart)
	require.NoError(t, k.CreateSchema(ctx, "evidence", []byte("type: evidence\n")))
	require.NoError(t, k.CreateSchema(ctx, "note", []byte(`type: note
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
`)))
	evidence, err := k.Create(ctx, &CreateOptions{Body: []byte("# Evidence\n"), Meta: []byte("type: evidence\nstatus: draft\n")})
	require.NoError(t, err)
	_, err = k.AppendSnapshot(ctx, evidence.ID, "first evidence")
	require.NoError(t, err)
	require.NoError(t, k.SetMeta(ctx, evidence.ID, mustParseMeta(t, "type: evidence\nstatus: ready\n")))
	source, err := k.Create(ctx, &CreateOptions{
		Body: []byte("# Source\n\n[Evidence](../" + evidence.ID.Path() + ")\n"),
		Meta: []byte("type: note\n"),
	})
	require.NoError(t, err)
	review, err := k.Create(ctx, &CreateOptions{
		Body: []byte("# Review\n\n[Source](../" + source.ID.Path() + ")\n"),
		Meta: []byte("type: evidence\ncertainty: 0.5\n"),
	})
	require.NoError(t, err)
	return k, rt, []NodeId{evidence.ID, source.ID, review.ID}
}

func mustParseMeta(t *testing.T, raw string) *NodeMeta {
	t.Helper()
	meta, err := ParseMeta(t.Context(), []byte(raw))
	require.NoError(t, err)
	return meta
}

type snapshotOutcome struct {
	snapshotOmegas map[string][]*float64
	liveOmegas     map[string]*float64
	timeline       string
	dirty          string
}

func snapshotOutcomeOf(t *testing.T, ctx context.Context, k *LocalKeg, ids []NodeId) snapshotOutcome {
	t.Helper()
	out := snapshotOutcome{snapshotOmegas: map[string][]*float64{}, liveOmegas: map[string]*float64{}}
	for _, id := range ids {
		snaps, err := k.ListSnapshots(ctx, id)
		require.NoError(t, err)
		for _, snap := range snaps {
			_, _, _, stats, err := k.GetSnapshot(ctx, id, snap.ID, SnapshotReadOptions{})
			require.NoError(t, err)
			out.snapshotOmegas[id.Path()] = append(out.snapshotOmegas[id.Path()], statsOmega(stats))
		}
		stats, err := k.GetStats(ctx, id)
		require.NoError(t, err)
		out.liveOmegas[id.Path()] = statsOmega(stats)
	}
	timeline, err := k.ReadIndex(ctx, TimelineIndexName)
	require.NoError(t, err)
	dirty, err := k.ReadIndex(ctx, DirtyIndexName)
	require.NoError(t, err)
	out.timeline, out.dirty = string(timeline), string(dirty)
	return out
}

func statsOmega(stats *NodeStats) *float64 {
	if omega, ok := stats.Omega(); ok {
		return &omega
	}
	return nil
}

func TestAppendSnapshotsMatchesSequentialAppends(t *testing.T) {
	batched, batchedRT, ids := newSnapshotBatchKeg(t)
	sequential, sequentialRT, _ := newSnapshotBatchKeg(t)
	ctx := t.Context()
	at := snapshotBatchStart.Add(time.Hour)

	setSnapshotIndexClock(t, batchedRT, at)
	requests := make([]NodeSnapshotRequest, 0, len(ids))
	for _, id := range ids {
		requests = append(requests, NodeSnapshotRequest{ID: id, Message: "checkpoint"})
	}
	_, err := batched.AppendSnapshots(ctx, requests)
	require.NoError(t, err)

	setSnapshotIndexClock(t, sequentialRT, at)
	for _, id := range ids {
		_, err := sequential.AppendSnapshot(ctx, id, "checkpoint")
		require.NoError(t, err)
	}

	want := snapshotOutcomeOf(t, ctx, sequential, ids)
	got := snapshotOutcomeOf(t, ctx, batched, ids)
	require.Equal(t, want, got)
	require.NotNil(t, got.snapshotOmegas[ids[1].Path()][0], "the source scores against evidence snapshotted earlier in the batch")
}

func TestRunSnapshotPolicyMatchesSequentialAppends(t *testing.T) {
	policy, policyRT, ids := newSnapshotBatchKeg(t)
	sequential, sequentialRT, _ := newSnapshotBatchKeg(t)
	ctx := t.Context()
	idle := 10 * time.Minute
	for _, k := range []*LocalKeg{policy, sequential} {
		require.NoError(t, k.UpdateSettings(ctx, func(cfg *Settings) {
			cfg.Snapshots = &SnapshotSettings{Mode: SnapshotModeAuto, IdleAfter: idle.String()}
		}))
	}
	at := snapshotBatchStart.Add(time.Hour)

	setSnapshotIndexClock(t, policyRT, at)
	result, err := policy.RunSnapshotPolicy(ctx)
	require.NoError(t, err)
	created := make([]NodeId, 0, len(result.Created))
	for _, snap := range result.Created {
		created = append(created, snap.Node)
	}
	require.Contains(t, created, ids[1])

	setSnapshotIndexClock(t, sequentialRT, at)
	for _, id := range created {
		_, err := sequential.AppendSnapshot(ctx, id, AutoSnapshotMessage(idle))
		require.NoError(t, err)
	}

	all, err := policy.ListNodes(ctx)
	require.NoError(t, err)
	require.Equal(t, snapshotOutcomeOf(t, ctx, sequential, all), snapshotOutcomeOf(t, ctx, policy, all))
}

// perNodeRepo hides the batch-read capability, so reads go node by node.
type perNodeRepo struct {
	Repository
	RepositorySnapshots
}

func TestDirtyIndexIsTheSameWithBatchReads(t *testing.T) {
	k, rt, ids := newSnapshotBatchKeg(t)
	ctx := t.Context()
	repo := k.Repo.(*testMemoryRepository)
	setSnapshotIndexClock(t, rt, snapshotBatchStart.Add(time.Hour))
	_, err := k.AppendSnapshot(ctx, ids[1], "clean")
	require.NoError(t, err)
	untitled, err := k.Create(ctx, &CreateOptions{Body: []byte("# Placeholder\n")})
	require.NoError(t, err)
	require.NoError(t, repo.updateNode(ctx, untitled.ID, func(n *memoryNode) {
		n.content = []byte("\n")
		n.stats.SetTitle("Title From Stats")
	}))
	bare, err := k.Create(ctx, &CreateOptions{Body: []byte("# Bare\n")})
	require.NoError(t, err)
	require.NoError(t, repo.updateNode(ctx, bare.ID, func(n *memoryNode) { n.meta = nil }))

	batched, err := k.buildDirtyIndexData(ctx)
	require.NoError(t, err)
	perNode, err := NewLocalKeg(perNodeRepo{Repository: repo, RepositorySnapshots: repo}, rt).buildDirtyIndexData(ctx)
	require.NoError(t, err)
	require.Equal(t, string(perNode), string(batched))

	rows := decodeJSONLines[dirtyIndexRow](t, batched)
	byNode := map[string]dirtyIndexRow{}
	for _, row := range rows {
		byNode[row.Node] = row
	}
	require.NotContains(t, byNode, ids[1].Path(), "a node matching its latest snapshot is clean")
	require.Equal(t, "Title From Stats", byNode[untitled.ID.Path()].Title)
	require.Empty(t, byNode[bare.ID.Path()].CurrentMetaHash)
}
