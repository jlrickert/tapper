package keg_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// limitsTestKeg returns a keg with history and assets, so its archive holds
// every kind of entry export writes.
func limitsTestKeg(t *testing.T, nodes int) (*keg.LocalKeg, context.Context) {
	t.Helper()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	for i := range nodes {
		node, err := k.Create(ctx, &keg.CreateOptions{Body: fmt.Appendf(nil, "# Node %d\n\nsee ../0\n", i)})
		require.NoError(t, err)
		require.NoError(t, k.WriteFile(ctx, node.ID, "notes.txt", bytes.Repeat([]byte("n"), 100*(i+1))))
		_, err = k.AppendSnapshot(ctx, node.ID, "rev")
		require.NoError(t, err)
	}
	return k, ctx
}

func stageStats(t *testing.T, archive []byte, lim keg.ArchiveLimits) (keg.ArchiveStageStats, error) {
	t.Helper()
	return keg.StageArchive(t.Context(), bytes.NewReader(archive), io.Discard, lim)
}

func largestEntry(t *testing.T, archive []byte) int64 {
	t.Helper()
	var largest int64
	for _, data := range readArchiveEntriesForTest(t, archive) {
		largest = max(largest, int64(len(data)))
	}
	return largest
}

func requireLimitError(t *testing.T, err error, limit string) {
	t.Helper()
	var limitErr *keg.ArchiveLimitError
	require.ErrorAs(t, err, &limitErr)
	require.Equal(t, limit, limitErr.Limit)
	require.ErrorIs(t, err, keg.ErrArchiveTooLarge)
}

// Every export must be restorable: for each ceiling, export and import accept
// an archive at exactly the measured value and refuse it one below, naming the
// same limit.
func TestArchiveLimitsAgreeBetweenExportAndImport(t *testing.T) {
	t.Parallel()
	k, ctx := limitsTestKeg(t, 6)
	opts := keg.ExportNodesOptions{WithHistory: true, WithAssets: true}
	archive := mustExportArchive(t, k, opts)
	stats, err := stageStats(t, archive, keg.ArchiveLimits{})
	require.NoError(t, err)
	require.Equal(t, len(readArchiveEntriesForTest(t, archive)), stats.Entries)
	require.Equal(t, int64(len(archive)), stats.CompressedBytes)

	measured := map[string]keg.ArchiveLimits{
		keg.ArchiveLimitEntries:         {MaxEntries: stats.Entries},
		keg.ArchiveLimitEntryBytes:      {MaxEntryBytes: largestEntry(t, archive)},
		keg.ArchiveLimitTarBytes:        {MaxTarBytes: stats.TarBytes},
		keg.ArchiveLimitCompressedBytes: {MaxCompressedBytes: int64(len(archive))},
	}
	below := func(lim keg.ArchiveLimits) keg.ArchiveLimits {
		switch {
		case lim.MaxEntries > 0:
			lim.MaxEntries--
		case lim.MaxEntryBytes > 0:
			lim.MaxEntryBytes--
		case lim.MaxTarBytes > 0:
			lim.MaxTarBytes--
		default:
			lim.MaxCompressedBytes--
		}
		return lim
	}
	for limit, lim := range measured {
		t.Run(limit, func(t *testing.T) {
			atLimit := opts
			atLimit.Limits = lim
			again := mustExportArchive(t, k, atLimit)
			_, err := stageStats(t, again, lim)
			require.NoError(t, err)

			over := opts
			over.Limits = below(lim)
			_, err = k.ExportNodes(ctx, over)
			requireLimitError(t, err, limit)
			_, err = stageStats(t, archive, below(lim))
			requireLimitError(t, err, limit)
		})
	}
}

func TestImportNodesEnforcesLimits(t *testing.T) {
	t.Parallel()
	src, ctx := limitsTestKeg(t, 3)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{WithAssets: true})
	fx := NewSandbox(t)
	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)

	_, err := dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{Limits: keg.ArchiveLimits{MaxEntries: 2}})
	requireLimitError(t, err, keg.ArchiveLimitEntries)

	var staged bytes.Buffer
	_, err = keg.StageArchive(ctx, bytes.NewReader(archive), &staged, keg.ArchiveLimits{})
	require.NoError(t, err)
	_, err = dst.ImportNodes(ctx, nil, keg.ImportNodesOptions{
		Source: bytes.NewReader(staged.Bytes()),
		Limits: keg.ArchiveLimits{MaxEntryBytes: 10},
	})
	requireLimitError(t, err, keg.ArchiveLimitEntryBytes)
}

func TestImportNodesFromStagedSource(t *testing.T) {
	t.Parallel()
	src, ctx := limitsTestKeg(t, 2)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{WithHistory: true, WithAssets: true})
	var staged bytes.Buffer
	stats, err := keg.StageArchive(ctx, bytes.NewReader(archive), &staged, keg.ArchiveLimits{})
	require.NoError(t, err)
	require.Equal(t, int64(staged.Len()), stats.TarBytes)
	require.Equal(t, 3, stats.Nodes)

	fx := NewSandbox(t)
	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	imported, err := dst.ImportNodes(ctx, nil, keg.ImportNodesOptions{Source: bytes.NewReader(staged.Bytes())})
	require.NoError(t, err)
	require.Len(t, imported, 3)
	data, err := dst.ReadFile(ctx, keg.NodeId{ID: 2}, "notes.txt")
	require.NoError(t, err)
	require.Len(t, data, 200)

	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{Source: bytes.NewReader(staged.Bytes())})
	require.ErrorIs(t, err, keg.ErrInvalid)
}

func TestStageArchiveRejectsUnreadableArchives(t *testing.T) {
	t.Parallel()
	src, ctx := limitsTestKeg(t, 1)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{})
	cases := map[string][]byte{
		"junk":      []byte("not an archive"),
		"truncated": archive[:len(archive)/2],
		"empty":     nil,
		"unsafe":    tarWith(t, map[string]string{"keg-archive/manifest.json": `{"format":"keg-archive/v3"}`, "keg-archive/nodes/1/assets/../../../x": "x"}),
		"v2":        tarWith(t, map[string]string{"keg-archive/manifest.json": `{"format":"keg-archive/v2"}`}),
		"missing":   tarWith(t, map[string]string{"keg-archive/nodes/1/README.md": "# x"}),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := keg.StageArchive(ctx, bytes.NewReader(data), io.Discard, keg.ArchiveLimits{})
			require.ErrorIs(t, err, keg.ErrInvalid)
		})
	}
}

func tarWith(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range entries {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

// A tar may carry a name twice; the last entry wins, as it always has.
func TestImportNodesUsesLastDuplicateEntry(t *testing.T) {
	t.Parallel()
	src, ctx := limitsTestKeg(t, 1)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{NodeIDs: []keg.NodeId{{ID: 1}}})
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	require.NoError(t, err)
	raw, err := io.ReadAll(gz)
	require.NoError(t, err)
	// Drop the end-of-archive marker and append a second README.
	var buf bytes.Buffer
	buf.Write(raw[:len(raw)-1024])
	tw := tar.NewWriter(&buf)
	body := []byte("# Replaced\n")
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "keg-archive/nodes/1/README.md", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}))
	_, err = tw.Write(body)
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	fx := NewSandbox(t)
	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	_, err = dst.ImportNodes(ctx, bytes.NewReader(buf.Bytes()), keg.ImportNodesOptions{})
	require.NoError(t, err)
	view, err := dst.ReadNode(ctx, keg.NodeId{ID: 1})
	require.NoError(t, err)
	require.Contains(t, string(view.Content), "# Replaced")
}

func TestImportNodesReplaceAllRemovesNodesNotInArchive(t *testing.T) {
	t.Parallel()
	src, ctx := limitsTestKeg(t, 2)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{WithAssets: true})

	fx := NewSandbox(t)
	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	for range 4 {
		_, err := dst.Create(ctx, &keg.CreateOptions{Body: []byte("# Extra\n")})
		require.NoError(t, err)
	}

	var report keg.ImportReport
	var phases []string
	_, err := dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{
		ReplaceAll: true,
		Report:     &report,
		Progress: func(p keg.ImportProgress) {
			if len(phases) == 0 || phases[len(phases)-1] != p.Phase {
				phases = append(phases, p.Phase)
			}
		},
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), report.Removed)
	require.Equal(t, []string{keg.ImportPhaseValidating, keg.ImportPhaseImporting, keg.ImportPhasePruning, keg.ImportPhaseIndexing}, phases)
	ids, err := dst.Repo.ListNodes(ctx)
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{{ID: 0}, {ID: 1}, {ID: 2}}, ids)

	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{ReplaceAll: true, AssignNewIDs: true})
	require.ErrorIs(t, err, keg.ErrInvalid)
}

type recordingSpool struct {
	bytes.Buffer
	read, discarded bool
}

func (s *recordingSpool) Reader() (io.ReadCloser, error) {
	s.read = true
	return io.NopCloser(bytes.NewReader(s.Bytes())), nil
}

func (s *recordingSpool) Discard() error {
	s.discarded = true
	return nil
}

func TestExportNodesWritesToSpool(t *testing.T) {
	t.Parallel()
	k, ctx := limitsTestKeg(t, 2)
	spool := &recordingSpool{}
	rc, err := k.ExportNodes(ctx, keg.ExportNodesOptions{Spool: spool})
	require.NoError(t, err)
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.True(t, spool.read)
	require.Equal(t, spool.Bytes(), data)

	failed := &recordingSpool{}
	_, err = k.ExportNodes(ctx, keg.ExportNodesOptions{Spool: failed, Limits: keg.ArchiveLimits{MaxEntries: 1}})
	require.True(t, errors.Is(err, keg.ErrArchiveTooLarge))
	require.True(t, failed.discarded)
	require.False(t, failed.read)
}

// The archive that started this work held 10,990 entries. A larger keg with
// history and assets must survive export, a replacing import into a keg that
// already has other nodes, and a second export unchanged.
func TestArchiveRoundTripOfLargeKeg(t *testing.T) {
	if testing.Short() {
		t.Skip("large archive round trip")
	}
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	png := tinyPNG(t)
	ids := make([]keg.NodeId, 0, 540)
	for i := range 540 {
		node, err := src.Create(ctx, &keg.CreateOptions{Body: fmt.Appendf(nil, "# Node %d\n\nlinks ../%d\n", i, i/2)})
		require.NoError(t, err)
		for file := range 14 {
			require.NoError(t, src.WriteFile(ctx, node.ID, fmt.Sprintf("notes-%d.txt", file), fmt.Appendf(nil, "notes %d/%d", i, file)))
		}
		require.NoError(t, src.WriteImage(ctx, node.ID, "diagram.png", png))
		ids = append(ids, node.ID)
	}
	// Each new snapshot replays the keg's whole timeline, so the fixture gets
	// its entry count mostly from assets and takes one batched snapshot round.
	for rev := range 1 {
		for start := 0; start < len(ids); start += keg.MaxMutationBatchSize {
			batch := ids[start:min(start+keg.MaxMutationBatchSize, len(ids))]
			requests := make([]keg.NodeSnapshotRequest, 0, len(batch))
			for _, id := range batch {
				require.NoError(t, src.SetContent(ctx, id, fmt.Appendf(nil, "# Node %d\n\nrevision %d\n", id.ID, rev)))
				requests = append(requests, keg.NodeSnapshotRequest{ID: id, Message: "rev"})
			}
			_, err := src.AppendSnapshots(ctx, requests)
			require.NoError(t, err)
		}
	}
	opts := keg.ExportNodesOptions{WithHistory: true, WithAssets: true}
	first := readArchiveEntriesForTest(t, mustExportArchive(t, src, opts))
	require.Greater(t, len(first), 10_990)

	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	for range 600 {
		_, err := dst.Create(ctx, &keg.CreateOptions{Body: []byte("# Extra\n")})
		require.NoError(t, err)
	}
	var report keg.ImportReport
	_, err := dst.ImportNodes(ctx, bytes.NewReader(mustExportArchive(t, src, opts)), keg.ImportNodesOptions{ReplaceAll: true, Report: &report})
	require.NoError(t, err)
	require.Equal(t, int64(60), report.Removed)

	second := readArchiveEntriesForTest(t, mustExportArchive(t, dst, opts))
	require.Equal(t, len(first), len(second))
	for name, data := range first {
		other, ok := second[name]
		require.True(t, ok, "missing %s after round trip", name)
		if name == "keg-archive/manifest.json" {
			require.JSONEq(t, withoutExportedAt(t, data), withoutExportedAt(t, other))
			continue
		}
		require.Equal(t, data, other, "entry %s changed in round trip", name)
	}
}

func withoutExportedAt(t *testing.T, raw []byte) string {
	t.Helper()
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(raw, &manifest))
	delete(manifest, "exported_at")
	out, err := json.Marshal(manifest)
	require.NoError(t, err)
	return string(out)
}

// Directory entries are not part of an archive's contents, and a name too
// long for a plain tar header survives staging intact.
func TestStageArchiveSkipsDirectoriesAndKeepsLongNames(t *testing.T) {
	t.Parallel()
	long := "keg-archive/nodes/1/assets/" + strings.Repeat("n", 180) + ".txt"
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "keg-archive/", Mode: 0o755, Typeflag: tar.TypeDir}))
	for name, body := range map[string]string{"keg-archive/manifest.json": `{"format":"keg-archive/v3","nodes":[]}`, long: "pax"} {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())

	var staged bytes.Buffer
	stats, err := keg.StageArchive(t.Context(), bytes.NewReader(buf.Bytes()), &staged, keg.ArchiveLimits{})
	require.NoError(t, err)
	require.Equal(t, 2, stats.Entries)
	tr := tar.NewReader(&staged)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names = append(names, hdr.Name)
	}
	require.ElementsMatch(t, []string{"keg-archive/manifest.json", long}, names)
}
