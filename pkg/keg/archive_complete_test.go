package keg_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// A replacing import prunes every node the archive does not carry, so it must
// refuse an archive that was exported with a node selection: such an archive
// would otherwise wipe the rest of the keg, node 0 included.
func TestImportNodesReplaceAllRefusesPartialArchive(t *testing.T) {
	t.Parallel()
	src, ctx := limitsTestKeg(t, 3)
	partials := map[string]keg.ExportNodesOptions{
		"node ids":  {NodeIDs: []keg.NodeId{{ID: 1}}},
		"query":     {Query: "golang"},
		"skip zero": {SkipZeroNode: true},
	}
	for name, opts := range partials {
		t.Run(name, func(t *testing.T) {
			archive := mustExportArchive(t, src, opts)

			fx := NewSandbox(t)
			dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
			initNonStrictTestKeg(t, dst, ctx)
			for range 4 {
				_, err := dst.Create(ctx, &keg.CreateOptions{Body: []byte("# Extra\n")})
				require.NoError(t, err)
			}
			before, err := dst.Repo.ListNodes(ctx)
			require.NoError(t, err)

			_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{ReplaceAll: true})
			require.ErrorIs(t, err, keg.ErrInvalid)

			after, err := dst.Repo.ListNodes(ctx)
			require.NoError(t, err)
			require.Equal(t, before, after)
			require.Contains(t, after, keg.NodeId{ID: 0})
			view, err := dst.ReadNode(ctx, keg.NodeId{ID: 1})
			require.NoError(t, err)
			require.Contains(t, string(view.Content), "# Extra")
		})
	}

	t.Run("full archive", func(t *testing.T) {
		archive := mustExportArchive(t, src, keg.ExportNodesOptions{})
		fx := NewSandbox(t)
		dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
		initNonStrictTestKeg(t, dst, ctx)
		for range 5 {
			_, err := dst.Create(ctx, &keg.CreateOptions{Body: []byte("# Extra\n")})
			require.NoError(t, err)
		}
		_, err := dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{ReplaceAll: true})
		require.NoError(t, err)
		ids, err := dst.Repo.ListNodes(ctx)
		require.NoError(t, err)
		require.Equal(t, []keg.NodeId{{ID: 0}, {ID: 1}, {ID: 2}, {ID: 3}}, ids)
		view, err := dst.ReadNode(ctx, keg.NodeId{ID: 1})
		require.NoError(t, err)
		require.Contains(t, string(view.Content), "# Node 0")
	})
}

// A full archive that somehow lacks node 0 still never removes the landing
// node when it replaces a keg.
func TestImportNodesReplaceAllKeepsNodeZero(t *testing.T) {
	t.Parallel()
	src, ctx := limitsTestKeg(t, 1)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{})
	entries := readArchiveEntriesForTest(t, archive)
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(entries["keg-archive/manifest.json"], &manifest))
	var nodes []any
	for _, n := range manifest["nodes"].([]any) {
		if n.(map[string]any)["source_id"] != "0" {
			nodes = append(nodes, n)
		}
	}
	require.Len(t, nodes, 1)
	manifest["nodes"] = nodes
	rawManifest, err := json.Marshal(manifest)
	require.NoError(t, err)
	archive = replaceArchiveEntry(t, archive, "keg-archive/manifest.json", rawManifest)

	fx := NewSandbox(t)
	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	for range 3 {
		_, err := dst.Create(ctx, &keg.CreateOptions{Body: []byte("# Extra\n")})
		require.NoError(t, err)
	}
	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{ReplaceAll: true})
	require.NoError(t, err)
	ids, err := dst.Repo.ListNodes(ctx)
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{{ID: 0}, {ID: 1}}, ids)
}

func TestStageArchiveReportsCompleteness(t *testing.T) {
	t.Parallel()
	src, _ := limitsTestKeg(t, 2)
	full := mustExportArchive(t, src, keg.ExportNodesOptions{})

	stats, err := stageStats(t, full, keg.ArchiveLimits{})
	require.NoError(t, err)
	require.True(t, stats.Complete, "full export")

	legacy := replaceArchiveEntry(t, full, "keg-archive/manifest.json",
		legacyConfigManifest(t, readArchiveEntriesForTest(t, full)["keg-archive/manifest.json"]))
	stats, err = stageStats(t, legacy, keg.ArchiveLimits{})
	require.NoError(t, err)
	require.True(t, stats.Complete, "legacy with_config manifest")

	partials := map[string]keg.ExportNodesOptions{
		"node ids":  {NodeIDs: []keg.NodeId{{ID: 1}}},
		"query":     {Query: "golang"},
		"skip zero": {SkipZeroNode: true},
	}
	for name, opts := range partials {
		stats, err := stageStats(t, mustExportArchive(t, src, opts), keg.ArchiveLimits{})
		require.NoError(t, err, name)
		require.False(t, stats.Complete, name)
	}
}

// ExportNodes owns a caller's spool: every failed export releases it exactly
// once, whether the failure came before, during, or after the write.
func TestExportNodesDiscardsSpoolOnEveryFailure(t *testing.T) {
	t.Parallel()
	k, ctx := limitsTestKeg(t, 1)

	t.Run("missing keg", func(t *testing.T) {
		fx := NewSandbox(t)
		empty := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
		spool := &recordingSpool{}
		_, err := empty.ExportNodes(ctx, keg.ExportNodesOptions{Spool: spool})
		require.Error(t, err)
		require.Equal(t, 1, spool.discards)
	})
	t.Run("bad query", func(t *testing.T) {
		spool := &recordingSpool{}
		_, err := k.ExportNodes(ctx, keg.ExportNodesOptions{Spool: spool, Query: ".created>"})
		require.Error(t, err)
		require.Equal(t, 1, spool.discards)
	})
	t.Run("limit", func(t *testing.T) {
		spool := &recordingSpool{}
		_, err := k.ExportNodes(ctx, keg.ExportNodesOptions{Spool: spool, Limits: keg.ArchiveLimits{MaxEntries: 1}})
		require.ErrorIs(t, err, keg.ErrArchiveTooLarge)
		require.Equal(t, 1, spool.discards)
	})
	t.Run("reader", func(t *testing.T) {
		sentinel := errors.New("spool reader broke")
		spool := &recordingSpool{readerErr: sentinel}
		_, err := k.ExportNodes(ctx, keg.ExportNodesOptions{Spool: spool})
		require.ErrorIs(t, err, sentinel)
		require.Equal(t, 1, spool.discards)
	})
	t.Run("success", func(t *testing.T) {
		spool := &recordingSpool{}
		rc, err := k.ExportNodes(ctx, keg.ExportNodesOptions{Spool: spool})
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		require.Zero(t, spool.discards)
	})
}

// failingWriter accepts limit bytes, then fails every write with err.
type failingWriter struct {
	limit int
	n     int
	err   error
}

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.n+len(p) > w.limit {
		k := max(w.limit-w.n, 0)
		w.n += k
		return k, w.err
	}
	w.n += len(p)
	return len(p), nil
}

// A failure writing the staged copy is the destination's fault, not the
// archive's, so StageArchive must not report it as an invalid archive.
func TestStageArchiveReturnsSinkErrorsAsIs(t *testing.T) {
	t.Parallel()
	src, _ := limitsTestKeg(t, 2)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{WithAssets: true, WithHistory: true})
	sentinel := errors.New("disk full")
	for _, limit := range []int{0, 600, 4096, 20000} {
		_, err := keg.StageArchive(t.Context(), bytes.NewReader(archive), &failingWriter{limit: limit, err: sentinel}, keg.ArchiveLimits{})
		require.ErrorIs(t, err, sentinel, "limit %d", limit)
		require.NotErrorIs(t, err, keg.ErrInvalid, "limit %d", limit)
	}
}
