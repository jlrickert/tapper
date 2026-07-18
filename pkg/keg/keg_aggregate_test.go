package keg_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestLocalKegAggregateOperations(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, k.Init(ctx))
	one, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# One\n\nlead\n"), Tags: []string{"alpha"}})
	require.NoError(t, err)
	two, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Two\n\n[One](../1)\n"), Tags: []string{"beta"}})
	require.NoError(t, err)

	listing, err := k.ListEntries(ctx, keg.ListEntriesOptions{Query: "alpha"})
	require.NoError(t, err)
	require.Len(t, listing.Entries, 1)
	require.Equal(t, 3, listing.NodeCount)
	require.Contains(t, listing.Tags, "alpha")
	views, err := k.ReadNodes(ctx, keg.ReadNodesOptions{NodeIDs: []keg.NodeId{two.ID, one.ID}, Touch: true})
	require.NoError(t, err)
	require.Equal(t, []string{"2", "1"}, []string{views[0].ID.Path(), views[1].ID.Path()})
	related, err := k.RelatedNodes(ctx, keg.RelatedNodesOptions{NodeIDs: []keg.NodeId{two.ID}, Direction: keg.RelatedLinks})
	require.NoError(t, err)
	require.Equal(t, "1", related[0].ID)
	graph, err := k.Graph(ctx)
	require.NoError(t, err)
	require.Len(t, graph.Nodes, 3)
	require.NotEmpty(t, graph.Edges)
	inspection, err := k.Inspect(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, inspection.Summary.NodeCount)
	results, err := k.ValidateNodes(ctx, keg.ValidateNodesOptions{NodeIDs: []keg.NodeId{one.ID, two.ID}})
	require.NoError(t, err)
	require.Len(t, results, 2)
}

type failOnceContentRepo struct {
	keg.Repository
	fail atomic.Bool
}

func (r *failOnceContentRepo) WriteContent(ctx context.Context, id keg.NodeId, data []byte) error {
	if r.fail.CompareAndSwap(true, false) {
		return errors.New("injected content write failure")
	}
	return r.Repository.WriteContent(ctx, id, data)
}

func TestUpdateNodeRejectsStaleHashAndReturnsNewHash(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, k.Init(ctx))
	created, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Original\n\nbody\n")})
	require.NoError(t, err)
	opened, err := k.OpenNode(ctx, keg.NodeOpenOptions{ID: created.ID, Touch: true})
	require.NoError(t, err)
	require.Positive(t, opened.Stats.AccessCount())
	originalHash := opened.Stats.Hash()
	require.NotEmpty(t, originalHash)

	updated, err := k.UpdateNode(ctx, keg.NodeUpdateOptions{
		ID: created.ID, Content: []byte("# Updated\n\nbody\n"), ExpectedHash: originalHash,
	})
	require.NoError(t, err)
	require.NotEmpty(t, updated.Hash)
	require.NotEqual(t, originalHash, updated.Hash)

	_, err = k.UpdateNode(ctx, keg.NodeUpdateOptions{
		ID: created.ID, Content: []byte("# Stale\n\nbody\n"), ExpectedHash: originalHash,
	})
	require.ErrorIs(t, err, keg.ErrConflict)
	view, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	require.Contains(t, string(view.Content), "# Updated")
	require.Equal(t, updated.Hash, view.Stats.Hash())
}

func TestUpdateNodeRollsBackMemoryWritesOnFailure(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	base := keg.NewMemoryRepo(fx.Runtime())
	repo := &failOnceContentRepo{Repository: base}
	k := keg.NewLocalKeg(repo, fx.Runtime())
	require.NoError(t, k.Init(ctx))
	created, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Original\n\nbody\n"), Tags: []string{"original"}})
	require.NoError(t, err)
	before, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	beforeIndexes, err := k.DexArtifacts(ctx)
	require.NoError(t, err)

	repo.fail.Store(true)
	_, err = k.UpdateNode(ctx, keg.NodeUpdateOptions{
		ID: created.ID, Content: []byte("# Changed\n\nbody\n"), Meta: []byte("tags: [changed]\n"), HasMeta: true, ExpectedHash: before.Stats.Hash(),
	})
	require.ErrorContains(t, err, "injected content write failure")
	after, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, before.Content, after.Content)
	require.Equal(t, before.Meta, after.Meta)
	require.Equal(t, before.Stats.Hash(), after.Stats.Hash())
	afterIndexes, err := k.DexArtifacts(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeIndexes.Indexes, afterIndexes.Indexes)
}

func TestRemoveNodesReturnsCompletedItemsAndFailure(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, k.Init(ctx))
	created, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Remove me\n")})
	require.NoError(t, err)

	result, err := k.RemoveNodes(ctx, keg.RemoveNodesOptions{NodeIDs: []keg.NodeId{created.ID, {ID: 99}}})
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{created.ID}, []keg.NodeId{result.Removed[0].ID})
	require.NotNil(t, result.Failure)
	require.Equal(t, 99, result.Failure.NodeID.ID)
	require.ErrorIs(t, result.Failure.Err(), keg.ErrNotExist)
}

func TestReplaceNodesWithRedirectsReturnsCompletedItemsAndStaleFailure(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, k.Init(ctx))
	one, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# One\n")})
	require.NoError(t, err)
	two, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Two\n")})
	require.NoError(t, err)
	oneView, err := k.ReadNode(ctx, one.ID)
	require.NoError(t, err)
	twoView, err := k.ReadNode(ctx, two.ID)
	require.NoError(t, err)
	require.NoError(t, k.SetContent(ctx, two.ID, []byte("# Two changed\n")))

	result, err := k.ReplaceNodesWithRedirects(ctx, []keg.NodeRedirect{
		{ID: one.ID, Target: "keg:target", TargetID: keg.NodeId{ID: 10}, ExpectedHash: oneView.Stats.Hash()},
		{ID: two.ID, Target: "keg:target", TargetID: keg.NodeId{ID: 11}, ExpectedHash: twoView.Stats.Hash()},
	})
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{one.ID}, result.Replaced)
	require.NotNil(t, result.Failure)
	require.Equal(t, two.ID, result.Failure.NodeID)
	require.ErrorIs(t, result.Failure.Err(), keg.ErrConflict)
	twoAfter, err := k.ReadNode(ctx, two.ID)
	require.NoError(t, err)
	require.Contains(t, string(twoAfter.Content), "Two changed")
}

func TestRemoteAggregateMethodsUseOneRequest(t *testing.T) {
	cases := []struct {
		name, method, path, body string
		call                     func(context.Context, *keg.RemoteKeg) error
	}{
		{"list", http.MethodPost, "/list", `{"entries":[],"tags":[],"indexed_count":0,"node_count":0}`, func(ctx context.Context, k *keg.RemoteKeg) error {
			_, err := k.ListEntries(ctx, keg.ListEntriesOptions{})
			return err
		}},
		{"doctor", http.MethodGet, "/doctor", `[]`, func(ctx context.Context, k *keg.RemoteKeg) error { _, err := k.Doctor(ctx); return err }},
		{"graph", http.MethodGet, "/graph", `{"nodes":[],"edges":[]}`, func(ctx context.Context, k *keg.RemoteKeg) error { _, err := k.Graph(ctx); return err }},
		{"read", http.MethodPost, "/nodes/read", `[]`, func(ctx context.Context, k *keg.RemoteKeg) error {
			_, err := k.ReadNodes(ctx, keg.ReadNodesOptions{NodeIDs: []keg.NodeId{{ID: 1}}, Touch: true})
			return err
		}},
		{"open", http.MethodPost, "/nodes/1/open", `{"id":"1","content":"# One\n","meta":"","stats":null,"assets":[],"images":[]}`, func(ctx context.Context, k *keg.RemoteKeg) error {
			_, err := k.OpenNode(ctx, keg.NodeOpenOptions{ID: keg.NodeId{ID: 1}, Touch: true})
			return err
		}},
		{"update", http.MethodPut, "/nodes/1", `{"hash":"updated"}`, func(ctx context.Context, k *keg.RemoteKeg) error {
			_, err := k.UpdateNode(ctx, keg.NodeUpdateOptions{ID: keg.NodeId{ID: 1}, Content: []byte("# One updated\n")})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var count atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				require.Equal(t, tc.method, r.Method)
				require.Equal(t, tc.path, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			fx := NewSandbox(t)
			remote := keg.NewRemoteKeg(srv.URL, "token", fx.Runtime())
			require.NoError(t, tc.call(fx.Context(), remote))
			require.Equal(t, int32(1), count.Load())
		})
	}
}
