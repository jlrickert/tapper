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

func replaceArchiveEntry(t *testing.T, archive []byte, path string, payload []byte) []byte {
	t.Helper()
	gzr, err := gzip.NewReader(bytes.NewReader(archive))
	require.NoError(t, err)
	defer gzr.Close()

	var raw bytes.Buffer
	tr := tar.NewReader(gzr)
	gzw := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gzw)
	replaced := false
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(tr)
		require.NoError(t, err)
		if header.Name == path {
			data = payload
			replaced = true
		}
		copyHeader := *header
		copyHeader.Size = int64(len(data))
		require.NoError(t, tw.WriteHeader(&copyHeader))
		_, err = tw.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gzw.Close())
	require.True(t, replaced, "expected to replace entry %q", path)
	return raw.Bytes()
}

type repoWithoutSchemas struct {
	keg.Repository
}

func markZeroAsTask(t *testing.T, k *keg.LocalKeg) {
	t.Helper()
	require.NoError(t, k.SetContent(t.Context(), keg.NodeId{ID: 0}, []byte("---\ntype: task\n---\n# Zero\n")))
}

var archiveTaskSchema = []byte(`type: task
meta:
  type: object
  required: ["type"]
  properties:
    type:
      const: task
markdown:
  requireTitle: true
`)

func TestArchiveExportUsesAssetsDirectoryAndIncludesConfigForFullBackup(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, src.Init(ctx))
	id, err := src.Create(ctx, &keg.CreateOptions{Title: "asset node", Body: []byte("# asset node\n")})
	require.NoError(t, err)
	require.NoError(t, src.WriteFile(ctx, id, "doc.txt", []byte("doc bytes")))
	require.NoError(t, src.WriteImage(ctx, id, "diagram.png", tinyPNG(t)))

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

func TestArchiveExportFullBackupIncludesSchemas(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, src.Init(ctx))
	require.NoError(t, src.WriteSchema(ctx, "task", archiveTaskSchema))

	entries := readArchiveEntriesForTest(t, mustExportArchive(t, src, keg.ExportNodesOptions{}))

	var manifest struct {
		WithSchemas bool     `json:"with_schemas"`
		Schemas     []string `json:"schemas"`
	}
	require.NoError(t, json.Unmarshal(entries["keg-archive/manifest.json"], &manifest))
	require.True(t, manifest.WithSchemas)
	require.Equal(t, []string{"task"}, manifest.Schemas)
	require.Equal(t, archiveTaskSchema, entries["keg-archive/schemas/task.schema.yaml"])
}

func TestArchiveImportRestoresKegConfigForFullBackup(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, src.Init(ctx))
	_, err := src.Create(ctx, &keg.CreateOptions{Title: "indexed", Body: []byte("# indexed\n"), Tags: []string{"restored"}})
	require.NoError(t, err)
	require.NoError(t, src.UpdateConfig(ctx, func(cfg *keg.Config) {
		cfg.Title = "Restored Title"
		cfg.URL = "https://example.com/restored"
		cfg.Creator = "restorer"
		cfg.State = "archived"
		cfg.Summary = "Restored summary"
		cfg.Instructions = "Restore carefully."
		cfg.Timezone = "America/Chicago"
		cfg.Links = []keg.LinkEntry{{Alias: "docs", URL: "https://example.com/docs"}}
		cfg.Indexes = append(cfg.UserIndexEntries(), keg.IndexEntry{File: "restored.md", Summary: "Restored nodes", Query: "restored"})
		cfg.Snapshots = &keg.SnapshotConfig{Mode: keg.SnapshotModeOff, IdleAfter: "2h"}
		cfg.SchemaPolicy = &keg.SchemaPolicy{
			Default: keg.ValidationModeWarn,
			Agent:   keg.ValidationModeBlock,
		}
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
	require.Equal(t, "https://example.com/restored", got.URL)
	require.Equal(t, "restorer", got.Creator)
	require.Equal(t, "archived", got.State)
	require.Equal(t, "Restored summary", got.Summary)
	require.Equal(t, "Restore carefully.", got.Instructions)
	require.Equal(t, "America/Chicago", got.Timezone)
	require.Equal(t, []keg.LinkEntry{{Alias: "docs", URL: "https://example.com/docs"}}, got.Links)
	require.Equal(t, &keg.SnapshotConfig{Mode: keg.SnapshotModeOff, IdleAfter: "2h"}, got.Snapshots)
	require.Equal(t, &keg.SchemaPolicy{Default: keg.ValidationModeWarn, Agent: keg.ValidationModeBlock}, got.SchemaPolicy)

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

func TestArchiveExportNodeSubsetOmitsSchemas(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, src.Init(ctx))
	require.NoError(t, src.WriteSchema(ctx, "task", archiveTaskSchema))
	id, err := src.Create(ctx, &keg.CreateOptions{
		Body: []byte("---\ntype: task\n---\n# Partial\n"),
	})
	require.NoError(t, err)

	entries := readArchiveEntriesForTest(t, mustExportArchive(t, src, keg.ExportNodesOptions{NodeIDs: []keg.NodeId{id}}))

	var manifest struct {
		WithSchemas bool     `json:"with_schemas"`
		Schemas     []string `json:"schemas"`
	}
	require.NoError(t, json.Unmarshal(entries["keg-archive/manifest.json"], &manifest))
	require.False(t, manifest.WithSchemas)
	require.Empty(t, manifest.Schemas)
	require.NotContains(t, entries, "keg-archive/schemas/task.schema.yaml")
}

func TestArchiveImportRestoresArchivedSchemas(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	archivedSchema := []byte(`type: task
summary: Archived tasks
meta:
  type: object
  required: ["type"]
  properties:
    type:
      const: task
markdown:
  requireTitle: true
`)
	targetSchema := []byte(`type: task
summary: Target tasks
markdown:
  requireTitle: true
`)
	targetOnlySchema := []byte(`type: decision
summary: Target decisions
`)

	src := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, src.Init(ctx))
	markZeroAsTask(t, src)
	require.NoError(t, src.WriteSchema(ctx, "task", archivedSchema))
	_, err := src.Create(ctx, &keg.CreateOptions{
		Body: []byte("---\ntype: task\n---\n# Imported Task\n"),
	})
	require.NoError(t, err)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{})

	dst := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, dst.Init(ctx))
	require.NoError(t, dst.WriteSchema(ctx, "task", targetSchema))
	require.NoError(t, dst.WriteSchema(ctx, "decision", targetOnlySchema))

	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{})
	require.NoError(t, err)

	gotTask, err := dst.ReadSchema(ctx, "task")
	require.NoError(t, err)
	require.Equal(t, archivedSchema, gotTask)
	gotDecision, err := dst.ReadSchema(ctx, "decision")
	require.NoError(t, err)
	require.Equal(t, targetOnlySchema, gotDecision)
}

func TestArchiveImportValidatesNodesAgainstArchivedSchemas(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	targetSchema := []byte(`type: task
markdown:
  requireTitle: true
  sections:
    - heading: Target Required
      level: 2
      required: true
`)

	src := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, src.Init(ctx))
	markZeroAsTask(t, src)
	require.NoError(t, src.WriteSchema(ctx, "task", archiveTaskSchema))
	id, err := src.Create(ctx, &keg.CreateOptions{
		Body: []byte("---\ntype: task\n---\n# Accepted By Archive Schema\n"),
	})
	require.NoError(t, err)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{})

	dst := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, dst.Init(ctx))
	require.NoError(t, dst.WriteSchema(ctx, "task", targetSchema))

	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{})
	require.NoError(t, err)
	exists, err := dst.NodeExists(ctx, id)
	require.NoError(t, err)
	require.True(t, exists)
}

func TestArchiveImportRejectsMalformedSchemaBeforeWritingNodes(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, src.Init(ctx))
	markZeroAsTask(t, src)
	require.NoError(t, src.WriteSchema(ctx, "task", archiveTaskSchema))
	id, err := src.Create(ctx, &keg.CreateOptions{
		Body: []byte("---\ntype: task\n---\n# Imported Task\n"),
	})
	require.NoError(t, err)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{})
	broken := replaceArchiveEntry(t, archive, "keg-archive/schemas/task.schema.yaml", []byte("type: ["))

	dst := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, dst.Init(ctx))
	_, err = dst.ImportNodes(ctx, bytes.NewReader(broken), keg.ImportNodesOptions{})
	require.Error(t, err)
	exists, err := dst.NodeExists(ctx, id)
	require.NoError(t, err)
	require.False(t, exists)
	_, err = dst.ReadSchema(ctx, "task")
	require.ErrorIs(t, err, keg.ErrNotExist)
}

func TestArchiveImportRejectsSchemasWhenTargetDoesNotSupportThemBeforeWritingNodes(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, src.Init(ctx))
	markZeroAsTask(t, src)
	require.NoError(t, src.WriteSchema(ctx, "task", archiveTaskSchema))
	id, err := src.Create(ctx, &keg.CreateOptions{
		Body: []byte("---\ntype: task\n---\n# Imported Task\n"),
	})
	require.NoError(t, err)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{})

	dstRepo := &repoWithoutSchemas{Repository: keg.NewMemoryRepo(fx.Runtime())}
	dst := keg.NewLocalKeg(dstRepo, fx.Runtime())
	require.NoError(t, dst.Init(ctx))
	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{})
	require.ErrorIs(t, err, keg.ErrNotSupported)
	exists, err := dst.NodeExists(ctx, id)
	require.NoError(t, err)
	require.False(t, exists)
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
