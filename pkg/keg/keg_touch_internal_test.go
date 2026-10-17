package keg

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// touchCountingRepo counts the writes a touch should keep to a minimum, and
// can fail the stats write of one node.
type touchCountingRepo struct {
	*testMemoryRepository
	dirtyWrites atomic.Int32
	metaWrites  atomic.Int32
	failStats   atomic.Pointer[NodeId]
}

func (r *touchCountingRepo) WriteIndex(ctx context.Context, name string, data []byte) error {
	if name == DirtyIndexName {
		r.dirtyWrites.Add(1)
	}
	return r.testMemoryRepository.WriteIndex(ctx, name, data)
}

func (r *touchCountingRepo) WriteMeta(ctx context.Context, id NodeId, data []byte) error {
	r.metaWrites.Add(1)
	return r.testMemoryRepository.WriteMeta(ctx, id, data)
}

func (r *touchCountingRepo) WriteStats(ctx context.Context, id NodeId, stats *NodeStats) error {
	if fail := r.failStats.Load(); fail != nil && *fail == id {
		return errors.New("injected stats write failure")
	}
	return r.testMemoryRepository.WriteStats(ctx, id, stats)
}

func (r *touchCountingRepo) reset() {
	r.dirtyWrites.Store(0)
	r.metaWrites.Store(0)
}

func newTouchTestKeg(t *testing.T) (*LocalKeg, *touchCountingRepo, context.Context) {
	t.Helper()
	fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	repo := &touchCountingRepo{testMemoryRepository: newTestMemoryRepo(fx.Runtime())}
	k := NewLocalKeg(repo, fx.Runtime())
	ctx := t.Context()
	require.NoError(t, k.Init(ctx))
	require.NoError(t, k.UpdateSettings(ctx, func(cfg *Settings) { cfg.SchemaPolicy.Strict = false }))
	return k, repo, ctx
}

func createTouchTestNodes(t *testing.T, ctx context.Context, k *LocalKeg, n int) []NodeId {
	t.Helper()
	ids := make([]NodeId, 0, n)
	for range n {
		created, err := k.Create(ctx, &CreateOptions{Body: []byte("# Node\n\nbody\n"), Meta: []byte("tags:\n  - kept\n")})
		require.NoError(t, err)
		ids = append(ids, created.ID)
	}
	return ids
}

func TestReadNodesTouchRefreshesDirtyIndexAtMostOnce(t *testing.T) {
	k, repo, ctx := newTouchTestKeg(t)
	ids := createTouchTestNodes(t, ctx, k, 8)
	metaBefore := make(map[NodeId][]byte, len(ids))
	for _, id := range ids {
		raw, err := repo.ReadMeta(ctx, id)
		require.NoError(t, err)
		metaBefore[id] = raw
	}

	repo.reset()
	views, err := k.ReadNodes(ctx, ReadNodesOptions{NodeIDs: ids, Touch: true})
	require.NoError(t, err)
	require.Len(t, views, len(ids))
	require.LessOrEqual(t, repo.dirtyWrites.Load(), int32(1))
	require.Zero(t, repo.metaWrites.Load(), "a touch must not rewrite existing metadata")
	for i, id := range ids {
		require.Equal(t, 1, views[i].Stats.AccessCount())
		raw, err := repo.ReadMeta(ctx, id)
		require.NoError(t, err)
		require.Equal(t, metaBefore[id], raw)
		stored, err := repo.ReadStats(ctx, id)
		require.NoError(t, err)
		require.Equal(t, 1, stored.AccessCount())
	}
}

func TestReadNodesTouchCreatesMissingMeta(t *testing.T) {
	k, repo, ctx := newTouchTestKeg(t)
	ids := createTouchTestNodes(t, ctx, k, 4)
	require.NoError(t, repo.updateNode(ctx, ids[1], func(n *memoryNode) { n.meta = nil }))
	require.NoError(t, k.refreshDirtyIndex(ctx))

	repo.reset()
	views, err := k.ReadNodes(ctx, ReadNodesOptions{NodeIDs: ids, Touch: true})
	require.NoError(t, err)
	require.Len(t, views, len(ids))
	require.LessOrEqual(t, repo.dirtyWrites.Load(), int32(1))
	require.Equal(t, int32(1), repo.metaWrites.Load(), "only the node without metadata gets it written")

	stored, err := repo.GetIndex(ctx, DirtyIndexName)
	require.NoError(t, err)
	rebuilt, err := k.buildDirtyIndexData(ctx)
	require.NoError(t, err)
	require.Equal(t, string(rebuilt), string(stored))
}

func TestTouchKeepsDirtyIndexTitleFromStats(t *testing.T) {
	k, repo, ctx := newTouchTestKeg(t)
	created, err := k.Create(ctx, &CreateOptions{Body: []byte("# Placeholder\n")})
	require.NoError(t, err)
	// Blank content has no title, so the dirty row takes the stats title.
	require.NoError(t, repo.updateNode(ctx, created.ID, func(n *memoryNode) { n.content = []byte("\n") }))
	stats, err := repo.ReadStats(ctx, created.ID)
	require.NoError(t, err)
	stats.SetTitle("Title From Stats")
	require.NoError(t, repo.testMemoryRepository.WriteStats(ctx, created.ID, stats))
	require.NoError(t, k.refreshDirtyIndex(ctx))
	before, err := repo.GetIndex(ctx, DirtyIndexName)
	require.NoError(t, err)
	require.Contains(t, string(before), "Title From Stats")

	_, err = k.OpenNode(ctx, NodeOpenOptions{ID: created.ID, Touch: true})
	require.NoError(t, err)
	_, err = k.ReadNodes(ctx, ReadNodesOptions{NodeIDs: []NodeId{created.ID}, Touch: true})
	require.NoError(t, err)
	require.NoError(t, k.Touch(ctx, created.ID))

	after, err := repo.GetIndex(ctx, DirtyIndexName)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
	rebuilt, err := k.buildDirtyIndexData(ctx)
	require.NoError(t, err)
	require.Equal(t, string(rebuilt), string(after))
	stored, err := repo.ReadStats(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "Title From Stats", stored.Title())
	require.Equal(t, 3, stored.AccessCount())
}

func TestReadNodesTouchRollsBackEveryTouchOnFailure(t *testing.T) {
	k, repo, ctx := newTouchTestKeg(t)
	ids := createTouchTestNodes(t, ctx, k, 3)
	// The first id appears twice so the rollback must undo both touches.
	request := []NodeId{ids[0], ids[1], ids[0], ids[2]}
	repo.failStats.Store(&ids[2])

	_, err := k.ReadNodes(ctx, ReadNodesOptions{NodeIDs: request, Touch: true})
	require.ErrorContains(t, err, "injected stats write failure")
	for _, id := range ids {
		stored, err := repo.ReadStats(ctx, id)
		require.NoError(t, err)
		require.Zero(t, stored.AccessCount(), "node %s keeps no touch", id.Path())
	}
}

func TestReadNodesTouchRepeatedIDShowsFinalStats(t *testing.T) {
	k, _, ctx := newTouchTestKeg(t)
	ids := createTouchTestNodes(t, ctx, k, 2)

	views, err := k.ReadNodes(ctx, ReadNodesOptions{NodeIDs: []NodeId{ids[0], ids[1], ids[0]}, Touch: true})
	require.NoError(t, err)
	require.Len(t, views, 3)
	require.Equal(t, 2, views[0].Stats.AccessCount())
	require.Equal(t, 1, views[1].Stats.AccessCount())
	require.Equal(t, 2, views[2].Stats.AccessCount())
}

// busyKegRepo reports the keg write lock as busy to any write that bounds its
// wait, as a repository does when another writer holds the lock past it.
type busyKegRepo struct {
	*testMemoryRepository
	busy     atomic.Bool
	lastWait atomic.Int64
}

func (r *busyKegRepo) WithKegWrite(ctx context.Context, fn func(context.Context) error) error {
	r.lastWait.Store(int64(LockWaitFrom(ctx)))
	if r.busy.Load() && LockWaitFrom(ctx) > 0 {
		return fmt.Errorf("keg row lock: %w", ErrLockTimeout)
	}
	return r.testMemoryRepository.WithKegWrite(ctx, fn)
}

func newBusyTestKeg(t *testing.T) (*LocalKeg, *busyKegRepo, context.Context) {
	t.Helper()
	fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	repo := &busyKegRepo{testMemoryRepository: newTestMemoryRepo(fx.Runtime())}
	k := NewLocalKeg(repo, fx.Runtime())
	ctx := t.Context()
	require.NoError(t, k.Init(ctx))
	require.NoError(t, k.UpdateSettings(ctx, func(cfg *Settings) { cfg.SchemaPolicy.Strict = false }))
	return k, repo, ctx
}

func TestTouchedReadsBoundTheKegLockWait(t *testing.T) {
	k, repo, ctx := newBusyTestKeg(t)
	created, err := k.Create(ctx, &CreateOptions{Body: []byte("# Bounded\n")})
	require.NoError(t, err)

	_, err = k.ReadNodes(ctx, ReadNodesOptions{NodeIDs: []NodeId{created.ID}, Touch: true})
	require.NoError(t, err)
	require.Equal(t, int64(touchLockWait), repo.lastWait.Load())

	_, err = k.OpenNode(WithLockWait(ctx, time.Minute), NodeOpenOptions{ID: created.ID, Touch: true})
	require.NoError(t, err)
	require.Equal(t, int64(time.Minute), repo.lastWait.Load(), "a caller's own bound wins")

	require.NoError(t, k.Touch(ctx, created.ID))
	require.Zero(t, repo.lastWait.Load(), "an explicit Touch waits as long as it takes")
}

func TestTouchedReadsServeContentWhenTheKegLockIsBusy(t *testing.T) {
	k, repo, ctx := newBusyTestKeg(t)
	created, err := k.Create(ctx, &CreateOptions{Body: []byte("# Busy\n\nbody\n")})
	require.NoError(t, err)
	repo.busy.Store(true)

	views, err := k.ReadNodes(ctx, ReadNodesOptions{NodeIDs: []NodeId{created.ID}, Touch: true})
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.Contains(t, string(views[0].Content), "# Busy")
	require.Zero(t, views[0].Stats.AccessCount())

	view, err := k.OpenNode(ctx, NodeOpenOptions{ID: created.ID, Touch: true})
	require.NoError(t, err)
	require.Contains(t, string(view.Content), "# Busy")
	require.Zero(t, view.Stats.AccessCount())

	stats, err := repo.ReadStats(ctx, created.ID)
	require.NoError(t, err)
	require.Zero(t, stats.AccessCount())
}

func TestTouchedReadFailsWhenItsContextIsDone(t *testing.T) {
	k, repo, ctx := newBusyTestKeg(t)
	created, err := k.Create(ctx, &CreateOptions{Body: []byte("# Done\n")})
	require.NoError(t, err)
	repo.busy.Store(true)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = k.ReadNodes(canceled, ReadNodesOptions{NodeIDs: []NodeId{created.ID}, Touch: true})
	require.ErrorIs(t, err, ErrLockTimeout)
}
