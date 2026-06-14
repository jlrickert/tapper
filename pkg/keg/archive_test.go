package keg_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func readArchiveEntriesForTest(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	gzr, err := gzip.NewReader(bytes.NewReader(archive))
	require.NoError(t, err)
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	entries := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if hdr.FileInfo().IsDir() {
			continue
		}
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		entries[hdr.Name] = data
	}
	return entries
}

func mustExportArchive(t *testing.T, k *keg.LocalKeg, opts keg.ExportNodesOptions) []byte {
	t.Helper()
	rc, err := k.ExportNodes(t.Context(), opts)
	require.NoError(t, err)
	archive, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	return archive
}

func TestArchiveExportUsesAssetsDirectoryAndIncludesConfigForFullBackup(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, src.Init(ctx))
	id, err := src.Create(ctx, &keg.CreateOptions{Title: "asset node", Body: []byte("# asset node\n")})
	require.NoError(t, err)
	require.NoError(t, src.WriteFile(ctx, id, "doc.txt", []byte("doc bytes")))
	require.NoError(t, src.WriteImage(ctx, id, "diagram.png", []byte("png bytes")))

	entries := readArchiveEntriesForTest(t, mustExportArchive(t, src, keg.ExportNodesOptions{WithAssets: true}))

	var manifest struct {
		Format     string `json:"format"`
		WithConfig bool   `json:"with_config"`
	}
	require.NoError(t, json.Unmarshal(entries["keg-archive/manifest.json"], &manifest))
	require.Equal(t, "keg-archive/v3", manifest.Format)
	require.True(t, manifest.WithConfig)
	require.Contains(t, entries, "keg-archive/keg.yaml")
	require.Contains(t, entries, "keg-archive/nodes/"+id.Path()+"/assets/doc.txt")
	require.Contains(t, entries, "keg-archive/nodes/"+id.Path()+"/images/diagram.png")
	for name := range entries {
		require.NotContains(t, name, "/files/")
	}
}

func TestArchiveImportRestoresKegConfigForFullBackup(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, src.Init(ctx))
	_, err := src.Create(ctx, &keg.CreateOptions{Title: "indexed", Body: []byte("# indexed\n"), Tags: []string{"restored"}})
	require.NoError(t, err)
	searchEnabled := false
	require.NoError(t, src.UpdateConfig(ctx, func(cfg *keg.Config) {
		cfg.Title = "Restored Title"
		cfg.Summary = "Restored summary"
		cfg.Timezone = "America/Chicago"
		cfg.Links = []keg.LinkEntry{{Alias: "docs", URL: "https://example.com/docs"}}
		cfg.Tags = map[string]string{"restored": "Restored tag"}
		cfg.Entities = map[string]keg.EntityEntry{"thing": {ID: 1, Summary: "Restored entity"}}
		cfg.Indexes = append(cfg.UserIndexEntries(), keg.IndexEntry{File: "restored.md", Summary: "Restored nodes", Query: "restored"})
		cfg.Doctor = &keg.DoctorConfig{TagCheck: true}
		cfg.Site = &keg.SiteConfig{Title: "Restored Site", BaseURL: "/restored/", Search: &searchEnabled}
	}))

	archive := mustExportArchive(t, src, keg.ExportNodesOptions{WithAssets: true})

	dst := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, dst.Init(ctx))
	require.NoError(t, dst.UpdateConfig(ctx, func(cfg *keg.Config) {
		cfg.Title = "Target Title"
		cfg.Summary = "Target summary"
		cfg.Timezone = "UTC"
	}))

	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{})
	require.NoError(t, err)

	got, err := dst.Config(ctx)
	require.NoError(t, err)
	require.Equal(t, "Restored Title", got.Title)
	require.Equal(t, "Restored summary", got.Summary)
	require.Equal(t, "America/Chicago", got.Timezone)
	require.Equal(t, []keg.LinkEntry{{Alias: "docs", URL: "https://example.com/docs"}}, got.Links)
	require.Equal(t, "Restored tag", got.Tags["restored"])
	require.Equal(t, keg.EntityEntry{ID: 1, Summary: "Restored entity"}, got.Entities["thing"])
	require.True(t, got.Doctor.TagCheck)
	require.NotNil(t, got.Site)
	require.Equal(t, "Restored Site", got.Site.Title)
	require.NotNil(t, got.Site.Search)
	require.False(t, *got.Site.Search)

	rawIndex, err := dst.ReadIndex(ctx, "restored.md")
	require.NoError(t, err)
	require.Contains(t, string(rawIndex), "indexed")
}

func TestArchiveImportNodeSubsetDoesNotRestoreKegConfig(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, src.Init(ctx))
	id, err := src.Create(ctx, &keg.CreateOptions{Title: "partial", Body: []byte("# partial\n")})
	require.NoError(t, err)
	require.NoError(t, src.UpdateConfig(ctx, func(cfg *keg.Config) {
		cfg.Title = "Source Title"
		cfg.Summary = "Source summary"
	}))

	archive := mustExportArchive(t, src, keg.ExportNodesOptions{NodeIDs: []keg.NodeId{id}, WithAssets: true})
	entries := readArchiveEntriesForTest(t, archive)
	require.NotContains(t, entries, "keg-archive/keg.yaml")
	var manifest struct {
		WithConfig bool `json:"with_config"`
	}
	require.NoError(t, json.Unmarshal(entries["keg-archive/manifest.json"], &manifest))
	require.False(t, manifest.WithConfig)

	dst := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, dst.Init(ctx))
	require.NoError(t, dst.UpdateConfig(ctx, func(cfg *keg.Config) {
		cfg.Title = "Target Title"
		cfg.Summary = "Target summary"
	}))

	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{})
	require.NoError(t, err)
	got, err := dst.Config(ctx)
	require.NoError(t, err)
	require.Equal(t, "Target Title", got.Title)
	require.Equal(t, "Target summary", got.Summary)
}

func TestArchiveImportRejectsNestedAssetNameBeforeWritingNodes(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, src.Init(ctx))
	id, err := src.Create(ctx, &keg.CreateOptions{Title: "asset node", Body: []byte("# asset node\n")})
	require.NoError(t, err)
	require.NoError(t, src.WriteFile(ctx, id, "doc.txt", []byte("doc bytes")))
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{WithAssets: true})

	broken := retarWithRenamedEntry(
		t,
		archive,
		"keg-archive/nodes/"+id.Path()+"/assets/doc.txt",
		"keg-archive/nodes/"+id.Path()+"/assets/nested/doc.txt",
	)

	dst := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, dst.Init(ctx))
	_, err = dst.ImportNodes(ctx, bytes.NewReader(broken), keg.ImportNodesOptions{})
	require.ErrorIs(t, err, keg.ErrInvalidAssetName)
	exists, err := dst.NodeExists(ctx, id)
	require.NoError(t, err)
	require.False(t, exists)
}
