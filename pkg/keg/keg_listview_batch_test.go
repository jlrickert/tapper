package keg_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	kegpkg "github.com/jlrickert/tapper/pkg/keg"
)

// countingRepo wraps a repository and records how many per-node metadata reads
// it serves, plus how many batch reads. It deliberately does NOT implement
// keg.RepositoryBatchRead unless batch is true, so one test fixture can
// exercise both paths.
type countingRepo struct {
	kegpkg.Repository
	perNode int
	batches int
}

func (r *countingRepo) ReadMeta(ctx context.Context, id kegpkg.NodeId) ([]byte, error) {
	r.perNode++
	return r.Repository.ReadMeta(ctx, id)
}

// batchingRepo adds the batch capability on top of countingRepo.
type batchingRepo struct{ *countingRepo }

func (r *batchingRepo) ReadMetaBatch(ctx context.Context, ids []kegpkg.NodeId) (map[string][]byte, error) {
	r.batches++
	out := make(map[string][]byte, len(ids))
	for _, id := range ids {
		// Read through the embedded repository directly so the per-node
		// counter stays a measure of what the caller did, not of this helper.
		data, err := r.countingRepo.Repository.ReadMeta(ctx, id)
		if err != nil {
			continue
		}
		out[id.Path()] = data
	}
	return out, nil
}

func (r *batchingRepo) ReadStatsBatch(ctx context.Context, ids []kegpkg.NodeId) (map[string]*kegpkg.NodeStats, error) {
	r.batches++
	out := make(map[string]*kegpkg.NodeStats, len(ids))
	for _, id := range ids {
		stats, err := r.countingRepo.Repository.ReadStats(ctx, id)
		if err != nil {
			continue
		}
		out[id.Path()] = stats
	}
	return out, nil
}

// seedNodes creates n nodes carrying a `type` metadata key.
func seedNodes(t *testing.T, k *kegpkg.LocalKeg, n int) {
	t.Helper()
	ctx := context.Background()
	for i := range n {
		_, err := k.Create(ctx, &kegpkg.CreateOptions{
			Title: fmt.Sprintf("Node %d", i),
			Attrs: map[string]any{"type": fmt.Sprintf("kind%d", i%3)},
		})
		require.NoError(t, err)
	}
}

// TestListViewBatchesMetadataReads is the guard for the cost model that makes
// metadata columns usable. Without batching, showing `type` on a 610-node keg
// meant a read per node -- and on the hosted backend, two queries per node.
func TestListViewBatchesMetadataReads(t *testing.T) {
	t.Parallel()
	const nodeCount = 12

	fx := NewSandbox(t)
	base := kegpkg.NewMemoryRepo(fx.Runtime())
	counter := &countingRepo{Repository: base}
	k := kegpkg.NewLocalKeg(&batchingRepo{countingRepo: counter}, fx.Runtime())
	initNonStrictTestKeg(t, k, context.Background())
	seedNodes(t, k, nodeCount)

	counter.perNode = 0
	counter.batches = 0

	out, err := k.ListView(context.Background(), kegpkg.ListViewOptions{Fields: []string{"type"}})
	require.NoError(t, err)
	// Init seeds node 0, so the keg holds one more node than we created.
	require.Len(t, out.Rows, nodeCount+1)
	require.Equal(t, "kind0", out.Rows[1].Fields["type"])

	require.Equal(t, 1, counter.batches,
		"projecting a metadata column must issue exactly one batch read, got %d", counter.batches)
}

// TestListViewSortBatchesMetadataReads covers the more expensive path: sorting
// needs a key for every matching node, not just the returned page, so it is the
// case most likely to regress into a per-node loop.
func TestListViewSortBatchesMetadataReads(t *testing.T) {
	t.Parallel()
	const nodeCount = 12

	fx := NewSandbox(t)
	base := kegpkg.NewMemoryRepo(fx.Runtime())
	counter := &countingRepo{Repository: base}
	k := kegpkg.NewLocalKeg(&batchingRepo{countingRepo: counter}, fx.Runtime())
	initNonStrictTestKeg(t, k, context.Background())
	seedNodes(t, k, nodeCount)

	counter.perNode = 0
	counter.batches = 0

	out, err := k.ListView(context.Background(), kegpkg.ListViewOptions{
		Fields: []string{"type"},
		Sort:   "type",
		Limit:  3,
	})
	require.NoError(t, err)
	require.Len(t, out.Rows, 3)
	require.Equal(t, nodeCount+1, out.TotalMatches, "TotalMatches counts before paging")

	// One batch to resolve the sort key across all entries, one to project the
	// returned page. Never one per node.
	require.Equal(t, 2, counter.batches,
		"sorting then projecting must issue two batch reads, got %d", counter.batches)

	// Ascending by type, so the page is non-decreasing. Node 0 carries no
	// type at all and sorts first, which is the empty-value contract.
	for i := 1; i < len(out.Rows); i++ {
		require.LessOrEqual(t, out.Rows[i-1].Fields["type"], out.Rows[i].Fields["type"],
			"rows must be ordered by the sort selector")
	}
	require.Equal(t, "", out.Rows[0].Fields["type"])
}

// TestListViewFallsBackWithoutBatchCapability proves a repository that cannot
// batch -- a plain filesystem keg -- still works, just node by node.
func TestListViewFallsBackWithoutBatchCapability(t *testing.T) {
	t.Parallel()
	const nodeCount = 6

	fx := NewSandbox(t)
	base := kegpkg.NewMemoryRepo(fx.Runtime())
	counter := &countingRepo{Repository: base}
	k := kegpkg.NewLocalKeg(counter, fx.Runtime())
	initNonStrictTestKeg(t, k, context.Background())
	seedNodes(t, k, nodeCount)

	counter.perNode = 0

	out, err := k.ListView(context.Background(), kegpkg.ListViewOptions{Fields: []string{"type"}})
	require.NoError(t, err)
	require.Len(t, out.Rows, nodeCount+1)
	require.Equal(t, "kind0", out.Rows[1].Fields["type"])
	require.Equal(t, nodeCount+1, counter.perNode,
		"without the capability the fallback reads each node once")
}

// TestListViewIntrinsicsReadNothing confirms the common case stays free: a
// listing naming only intrinsics and index timestamps must touch no node.
func TestListViewIntrinsicsReadNothing(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)
	base := kegpkg.NewMemoryRepo(fx.Runtime())
	counter := &countingRepo{Repository: base}
	k := kegpkg.NewLocalKeg(&batchingRepo{countingRepo: counter}, fx.Runtime())
	initNonStrictTestKeg(t, k, context.Background())
	seedNodes(t, k, 5)

	counter.perNode = 0
	counter.batches = 0

	out, err := k.ListView(context.Background(), kegpkg.ListViewOptions{
		Fields: []string{"id", "title", ".updated"},
		Sort:   ".created",
	})
	require.NoError(t, err)
	require.Len(t, out.Rows, 6)
	require.Equal(t, 0, counter.batches, "intrinsics must not trigger any read")
	require.Equal(t, 0, counter.perNode, "intrinsics must not trigger any read")
}
