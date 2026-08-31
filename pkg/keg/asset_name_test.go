package keg_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// badAssetNames are names that must never be accepted: they would let
// filepath.Join resolve outside the node's asset directory (CWE-22).
var badAssetNames = []string{
	"../escape.txt",
	"../../escape.txt",
	"sub/dir.txt",
	`..\windows.txt`,
	"..",
	".",
	"",
}

// TestAssetName_RejectsTraversal pins that the repository asset boundary rejects
// traversing/separator names while ordinary single-component names round-trip.
func TestAssetName_RejectsTraversal(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	rt := fx.Runtime()

	r := newTestMemoryRepo(rt)
	id := keg.NodeId{ID: 0}
	require.NoError(t, r.WriteContent(ctx, id, []byte("# zero\n")))

	for _, name := range badAssetNames {
		require.ErrorIsf(t, r.WriteImage(ctx, id, name, []byte("x")),
			keg.ErrInvalidAssetName, "WriteAsset(%q)", name)
		_, err := r.ReadImage(ctx, id, name)
		require.ErrorIsf(t, err, keg.ErrInvalidAssetName, "ReadImage(%q)", name)
		_, err = r.ReadFile(ctx, id, name)
		require.ErrorIsf(t, err, keg.ErrInvalidAssetName, "ReadFile(%q)", name)
		require.ErrorIsf(t, r.DeleteImage(ctx, id, name),
			keg.ErrInvalidAssetName, "DeleteAsset(%q)", name)
	}

	// Ordinary names still work.
	for _, repo := range []interface {
		WriteImage(c context.Context, id keg.NodeId, name string, data []byte) error
		ReadImage(c context.Context, id keg.NodeId, name string) ([]byte, error)
		DeleteImage(c context.Context, id keg.NodeId, name string) error
	}{r} {
		require.NoError(t, repo.WriteImage(ctx, id, "a.png", []byte("png")))
		got, err := repo.ReadImage(ctx, id, "a.png")
		require.NoError(t, err)
		require.Equal(t, []byte("png"), got)
		require.NoError(t, repo.DeleteImage(ctx, id, "a.png"))
	}
}

// retarWithRenamedEntry rebuilds a gzip-tar archive, renaming the single entry
// named from to to. Used to plant a traversal path inside a valid keg archive.
func retarWithRenamedEntry(t *testing.T, archive []byte, from, to string) []byte {
	t.Helper()
	gzr, err := gzip.NewReader(bytes.NewReader(archive))
	require.NoError(t, err)
	tr := tar.NewReader(gzr)

	var out bytes.Buffer
	gzw := gzip.NewWriter(&out)
	tw := tar.NewWriter(gzw)
	renamed := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		name := hdr.Name
		if name == from {
			name = to
			renamed = true
		}
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg,
		}))
		_, err = tw.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gzw.Close())
	require.True(t, renamed, "expected to rename entry %q", from)
	return out.Bytes()
}

// TestImport_RejectsZipSlipArchive pins that a keg archive whose asset entry
// name traverses upward is rejected wholesale on import, with nothing written
// outside the target keg root — while a clean archive still imports.
func TestImport_RejectsZipSlipArchive(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	rt := fx.Runtime()

	// Build a legitimate archive (valid manifest/meta/stats) with one attachment.
	src := keg.NewLocalKeg(newTestMemoryRepo(rt), rt)
	initNonStrictTestKeg(t, src, ctx)
	nid, err := src.Create(ctx, &keg.CreateOptions{Title: "x", Body: []byte("# x\n")})
	require.NoError(t, err)
	require.NoError(t, src.WriteFile(ctx, nid.ID, "doc.txt", []byte("benign")))
	rc, err := src.ExportNodes(ctx, keg.ExportNodesOptions{WithAssets: true})
	require.NoError(t, err)
	clean, err := io.ReadAll(rc)
	require.NoError(t, rc.Close())
	require.NoError(t, err)

	base := t.TempDir()

	// Clean import still works (no regression).
	okKeg := keg.NewLocalKeg(newTestMemoryRepo(rt), rt)
	initNonStrictTestKeg(t, okKeg, ctx)
	_, err = okKeg.ImportNodes(ctx, bytes.NewReader(clean), keg.ImportNodesOptions{})
	require.NoError(t, err, "a clean archive must still import")

	// Tampered import is rejected, and nothing escapes the keg root.
	from := "keg-archive/nodes/" + nid.ID.Path() + "/assets/doc.txt"
	to := "keg-archive/nodes/" + nid.ID.Path() + "/assets/../../../PWNED.txt"
	evil := retarWithRenamedEntry(t, clean, from, to)

	evilKeg := keg.NewLocalKeg(newTestMemoryRepo(rt), rt)
	initNonStrictTestKeg(t, evilKeg, ctx)
	_, err = evilKeg.ImportNodes(ctx, bytes.NewReader(evil), keg.ImportNodesOptions{})
	require.Error(t, err, "a hostile archive must be rejected")

	_, statErr := rt.Stat(filepath.Join(base, "PWNED.txt"), false)
	require.True(t, os.IsNotExist(statErr), "nothing may be written outside the keg root")
}
