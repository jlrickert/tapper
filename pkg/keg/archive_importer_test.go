package keg_test

import (
	"bytes"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// Importing is a capability of a storage engine, not of every keg: a keg
// reached over a hub's API cannot import, while an engine keg restores a
// whole-keg export through NodeImporter and still restores from its trash.
func TestNodeImporterRestoresAWholeKegExport(t *testing.T) {
	t.Parallel()
	var remote keg.Keg = keg.NewRemoteKeg("http://hub.invalid", "token", NewSandbox(t).Runtime())
	_, ok := remote.(keg.NodeImporter)
	require.False(t, ok, "a remote keg has no node import")

	src, ctx := limitsTestKeg(t, 3)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{WithHistory: true, WithAssets: true})

	dst, _, _ := newTrashingKeg(t)
	// The destination already holds more nodes than the archive, so the
	// last one sits on an id the archive does not carry.
	var stray keg.CreateResult
	for range 4 {
		var err error
		stray, err = dst.Create(ctx, &keg.CreateOptions{Body: []byte("# Stray\n\nnot in the archive\n")})
		require.NoError(t, err)
	}
	require.Equal(t, 4, stray.ID.ID)

	var engine keg.Keg = dst
	importer, ok := engine.(keg.NodeImporter)
	require.True(t, ok, "an engine keg imports archives")
	imported, err := importer.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{ReplaceAll: true})
	require.NoError(t, err)
	require.Len(t, imported, 4, "node 0 and the three nodes")
	for _, node := range imported {
		want, err := src.ReadNode(ctx, node.ID)
		require.NoError(t, err)
		got, err := dst.ReadNode(ctx, node.ID)
		require.NoError(t, err)
		require.Equal(t, want.Content, got.Content)
		files, err := dst.ListFiles(ctx, node.ID)
		require.NoError(t, err)
		if node.ID.ID > 0 {
			require.Contains(t, files, "notes.txt")
		}
	}
	exists, err := dst.NodeExists(ctx, stray.ID)
	require.NoError(t, err)
	require.False(t, exists, "a whole-keg restore removes nodes the archive does not carry")

	// Trash restore imports through the same engine path.
	view, err := dst.ReadNode(ctx, imported[1].ID)
	require.NoError(t, err)
	_, err = dst.Remove(ctx, keg.NodeRemoveOptions{ID: view.ID, ExpectedHash: view.Hash()})
	require.NoError(t, err)
	require.NoError(t, dst.RestoreTrashed(ctx, []keg.NodeId{view.ID}))
	back, err := dst.ReadNode(ctx, view.ID)
	require.NoError(t, err)
	require.Equal(t, view.Content, back.Content)
}
