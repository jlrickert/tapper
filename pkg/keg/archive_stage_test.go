package keg_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

const stageTestManifest = `{"format":"keg-archive/v3","nodes":[]}`

type stageTestEntry struct {
	name string
	body []byte
	pax  map[string]string
	fmt  tar.Format
}

// stageTestTar builds a tar holding a manifest and then entries.
func stageTestTar(t *testing.T, entries ...stageTestEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	all := append([]stageTestEntry{{name: "keg-archive/manifest.json", body: []byte(stageTestManifest)}}, entries...)
	for _, e := range all {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: e.name, Mode: 0o644, Size: int64(len(e.body)), Typeflag: tar.TypeReg,
			PAXRecords: e.pax, Format: e.fmt,
		}))
		_, err := tw.Write(e.body)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	return buf.Bytes()
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, err := gz.Write(data)
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

// readCounter counts the bytes a reader hands out.
type readCounter struct {
	r io.Reader
	n int64
}

func (c *readCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// writeCounter counts the bytes written to it.
type writeCounter struct{ n int64 }

func (c *writeCounter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

// One extended header record far larger than any name is refused while it is
// being read, for both PAX and GNU long-name records.
func TestStageArchiveRefusesOversizedHeaderRecords(t *testing.T) {
	t.Parallel()
	huge := strings.Repeat("x", 768<<10)
	cases := map[string]stageTestEntry{
		"pax record":    {name: "keg-archive/nodes/1/README.md", body: []byte("# x\n"), pax: map[string]string{"comment": huge}, fmt: tar.FormatPAX},
		"gnu long name": {name: "keg-archive/nodes/1/assets/" + huge, body: []byte("x"), fmt: tar.FormatGNU},
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tarball := stageTestTar(t, entry)
			in := &readCounter{r: bytes.NewReader(tarball)}
			_, err := keg.StageArchive(t.Context(), in, io.Discard, keg.ArchiveLimits{})
			require.ErrorIs(t, err, keg.ErrInvalid)
			require.Less(t, len(err.Error()), 1024, "the error does not carry the record")
			require.Less(t, in.n, int64(128<<10), "the record is refused before it is read in full")
		})
	}
}

// Extended headers count toward MaxTarBytes even though staging drops them:
// a small gzip whose PAX records expand past the limit is refused.
func TestStageArchiveCountsExtendedHeadersTowardTarBytes(t *testing.T) {
	t.Parallel()
	var entries []stageTestEntry
	for i := range 64 {
		entries = append(entries, stageTestEntry{
			name: fmt.Sprintf("keg-archive/nodes/1/assets/%02d.txt", i),
			pax:  map[string]string{"comment": strings.Repeat("c", 32<<10)},
			fmt:  tar.FormatPAX,
		})
	}
	archive := gzipBytes(t, stageTestTar(t, entries...))
	require.Less(t, len(archive), 64<<10, "the upload itself is small")

	lim := keg.ArchiveLimits{MaxTarBytes: 1 << 20}
	var staged writeCounter
	in := &readCounter{r: bytes.NewReader(archive)}
	_, err := keg.StageArchive(t.Context(), in, &staged, lim)
	requireLimitError(t, err, keg.ArchiveLimitTarBytes)
	require.Less(t, staged.n, int64(64<<10), "the staged output never got near the limit")

	stats, err := stageStats(t, archive, keg.ArchiveLimits{})
	require.NoError(t, err, "without a limit the same archive stages")
	require.Equal(t, 65, stats.Entries)
}

// Names long enough to need PAX or GNU long-name records stage under a limit
// set to the archive's exact size.
func TestStageArchiveAcceptsLongNamesAtTheLimit(t *testing.T) {
	t.Parallel()
	prefix := "keg-archive/nodes/1/assets/"
	var entries []stageTestEntry
	for i, n := range []int{150, 1000, 4096 - len(prefix) - 8} {
		format := tar.FormatPAX
		if i%2 == 1 {
			format = tar.FormatGNU
		}
		entries = append(entries, stageTestEntry{
			name: fmt.Sprintf("%s%04d-%s", prefix, i, strings.Repeat("n", n-5)),
			body: bytes.Repeat([]byte("b"), 700*(i+1)),
			fmt:  format,
		})
	}
	tarball := stageTestTar(t, entries...)
	lim := keg.ArchiveLimits{MaxTarBytes: int64(len(tarball))}
	for _, input := range [][]byte{tarball, gzipBytes(t, tarball)} {
		var staged bytes.Buffer
		stats, err := keg.StageArchive(t.Context(), bytes.NewReader(input), &staged, lim)
		require.NoError(t, err)
		require.Equal(t, 4, stats.Entries)
		require.LessOrEqual(t, stats.TarBytes, lim.MaxTarBytes)

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
		require.Len(t, names, 4)
		for _, e := range entries {
			require.Contains(t, names, e.name)
		}
	}
}

// An archive bomb is refused before staging writes past MaxTarBytes, whether
// it is one huge entry or many that each fit.
func TestStageArchiveRefusesABombBeforeWritingPastTheLimit(t *testing.T) {
	t.Parallel()
	const limit = 1 << 20
	one := stageTestTar(t, stageTestEntry{name: "keg-archive/nodes/1/assets/bomb.bin", body: make([]byte, 16<<20)})
	var many []stageTestEntry
	for i := range 64 {
		many = append(many, stageTestEntry{name: fmt.Sprintf("keg-archive/nodes/1/assets/%02d.bin", i), body: make([]byte, 60<<10)})
	}
	cases := map[string][]byte{"one entry": one, "many entries": stageTestTar(t, many...)}
	for name, tarball := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var staged writeCounter
			_, err := keg.StageArchive(t.Context(), bytes.NewReader(gzipBytes(t, tarball)), &staged, keg.ArchiveLimits{MaxTarBytes: limit})
			requireLimitError(t, err, keg.ArchiveLimitTarBytes)
			require.LessOrEqual(t, staged.n, int64(limit), "nothing past the limit reaches staging")
		})
	}
}

// A large archive under the limit stages in full.
func TestStageArchiveStagesALargeArchiveUnderTheLimit(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewPCG(1, 2))
	var entries []stageTestEntry
	for i := range 48 {
		body := make([]byte, 96<<10)
		for j := range body {
			body[j] = byte(rng.UintN(256))
		}
		entries = append(entries, stageTestEntry{name: fmt.Sprintf("keg-archive/nodes/%d/assets/blob.bin", i), body: body})
	}
	tarball := stageTestTar(t, entries...)
	require.Greater(t, len(tarball), 4<<20)

	var staged bytes.Buffer
	stats, err := keg.StageArchive(t.Context(), bytes.NewReader(gzipBytes(t, tarball)), &staged, keg.ArchiveLimits{MaxTarBytes: int64(len(tarball))})
	require.NoError(t, err)
	require.Equal(t, 49, stats.Entries)
	require.Equal(t, int64(staged.Len()), stats.TarBytes)
	require.Equal(t, tarball, staged.Bytes(), "a normalized archive stages byte for byte")
}

// An attachment entry whose name import would refuse to write is refused by
// staging, as an invalid asset name.
func TestStageArchiveRefusesHostileAssetNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		`keg-archive/nodes/1/assets/a\b.txt`,
		"keg-archive/nodes/1/assets/./doc.txt",
		"keg-archive/nodes/1/assets/sub/doc.txt",
		"keg-archive/nodes/1/images/nested/pic.png",
		"keg-archive/nodes/1/videos/./clip.mp4",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tarball := stageTestTar(t, stageTestEntry{name: name, body: []byte("x")})
			var staged writeCounter
			_, err := keg.StageArchive(t.Context(), bytes.NewReader(tarball), &staged, keg.ArchiveLimits{})
			require.ErrorIs(t, err, keg.ErrInvalidAssetName)
			require.ErrorIs(t, err, keg.ErrInvalid)
		})
	}
}

// Ordinary asset names, dots and dashes included, stage and import.
func TestStageArchiveKeepsOrdinaryAssetNames(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	node, err := src.Create(ctx, &keg.CreateOptions{Body: []byte("# Assets\n")})
	require.NoError(t, err)
	names := []string{"diagram.v2-final.txt", ".hidden-notes", "a..b.txt", "x-y_z.tar.gz"}
	for _, name := range names {
		require.NoError(t, src.WriteFile(ctx, node.ID, name, []byte(name)))
	}
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{WithAssets: true})

	var staged bytes.Buffer
	_, err = keg.StageArchive(ctx, bytes.NewReader(archive), &staged, keg.ArchiveLimits{})
	require.NoError(t, err)

	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	_, err = dst.ImportNodes(ctx, nil, keg.ImportNodesOptions{Source: bytes.NewReader(staged.Bytes())})
	require.NoError(t, err)
	files, err := dst.ListFiles(ctx, node.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, names, files)
}
