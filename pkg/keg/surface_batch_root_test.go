package keg_test

import (
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// The root can be restored from its own history, but never moved or removed.
func TestBatchMutationsAndTheRoot(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)

	root, err := k.ReadNode(ctx, keg.NodeId{ID: 0})
	require.NoError(t, err)
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{ID: keg.NodeId{ID: 0}, Content: []byte("# Root v1\n"), HasContent: true, ExpectedHash: root.Hash()}})
	require.NoError(t, err)
	snaps, err := k.AppendSnapshots(ctx, []keg.NodeSnapshotRequest{{ID: keg.NodeId{ID: 0}, Message: "v1"}})
	require.NoError(t, err)
	root, err = k.ReadNode(ctx, keg.NodeId{ID: 0})
	require.NoError(t, err)
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{ID: keg.NodeId{ID: 0}, Content: []byte("# Root v2\n"), HasContent: true, ExpectedHash: root.Hash()}})
	require.NoError(t, err)
	root, err = k.ReadNode(ctx, keg.NodeId{ID: 0})
	require.NoError(t, err)

	_, err = k.MoveBatch(ctx, []keg.MoveItem{{Source: 0, Destination: 9, ExpectedHash: root.Hash()}})
	require.ErrorIs(t, err, keg.ErrInvalid)
	_, err = k.RemoveBatch(ctx, []keg.RemoveItem{{ID: 0, ExpectedHash: root.Hash()}})
	require.ErrorIs(t, err, keg.ErrInvalid)

	_, err = k.RestoreBatch(ctx, []keg.RestoreItem{{ID: 0, Revision: snaps[0].ID, ExpectedHash: root.Hash()}})
	require.NoError(t, err)
	content, err := k.GetContent(ctx, keg.NodeId{ID: 0})
	require.NoError(t, err)
	require.Contains(t, string(content), "Root v1")
}
