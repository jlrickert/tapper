package keg_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync/atomic"
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

type failSecondNextRepo struct {
	keg.Repository
	calls atomic.Int32
}

func (r *failSecondNextRepo) Next(ctx context.Context) (keg.NodeId, error) {
	if r.calls.Add(1) == 2 {
		return keg.NodeId{}, errors.New("injected allocation failure")
	}
	return r.Repository.Next(ctx)
}

func TestArchiveManifestRecordsSourceHash(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	created, err := src.Create(ctx, &keg.CreateOptions{Body: []byte("# Source\n\nbody\n")})
	require.NoError(t, err)
	view, err := src.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{NodeIDs: []keg.NodeId{created.ID}})
	entries := readArchiveEntriesForTest(t, archive)
	var manifest struct {
		Nodes []struct {
			SourceID   string `json:"source_id"`
			SourceHash string `json:"source_hash"`
		} `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal(entries["keg-archive/manifest.json"], &manifest))
	require.Equal(t, created.ID.Path(), manifest.Nodes[0].SourceID)
	require.Equal(t, view.Stats.Hash(), manifest.Nodes[0].SourceHash)
}

func TestImportHistoryIfSupportedFallsBackWithoutSnapshots(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	created, err := src.Create(ctx, &keg.CreateOptions{Body: []byte("# Source\n\nbody\n")})
	require.NoError(t, err)
	require.NoError(t, src.Commit(ctx, created.ID))
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{NodeIDs: []keg.NodeId{created.ID}, WithHistory: true})

	dst := keg.NewLocalKeg(&repoWithoutSchemas{Repository: newTestMemoryRepo(fx.Runtime())}, fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{HistoryIfSupported: true})
	require.NoError(t, err)
	view, err := dst.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	require.Contains(t, string(view.Content), "# Source")
}

func TestImportCleansUnusedIDReservationsAfterAllocationFailure(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	one, err := src.Create(ctx, &keg.CreateOptions{Body: []byte("# One\n")})
	require.NoError(t, err)
	two, err := src.Create(ctx, &keg.CreateOptions{Body: []byte("# Two\n")})
	require.NoError(t, err)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{NodeIDs: []keg.NodeId{one.ID, two.ID}})

	base := newTestMemoryRepo(fx.Runtime())
	dst := keg.NewLocalKeg(base, fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	failing := keg.NewLocalKeg(&failSecondNextRepo{Repository: base}, fx.Runtime())
	_, err = failing.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{AssignNewIDs: true})
	require.ErrorContains(t, err, "injected allocation failure")
	ids, err := base.ListNodes(ctx)
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{{ID: 0}}, ids)
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

	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	id, err := src.Create(ctx, &keg.CreateOptions{Body: []byte("# asset node\n")})
	require.NoError(t, err)
	require.NoError(t, src.WriteFile(ctx, id.ID, "doc.txt", []byte("doc bytes")))
	require.NoError(t, src.WriteImage(ctx, id.ID, "diagram.png", tinyPNG(t)))

	entries := readArchiveEntriesForTest(t, mustExportArchive(t, src, keg.ExportNodesOptions{WithAssets: true}))

	var manifest struct {
		Format       string `json:"format"`
		WithSettings bool   `json:"with_settings"`
	}
	require.NoError(t, json.Unmarshal(entries["keg-archive/manifest.json"], &manifest))
	require.Equal(t, "keg-archive/v3", manifest.Format)
	require.True(t, manifest.WithSettings)
	// The importer accepts the pre-rename `with_config` spelling; the writer
	// must never emit it, or the legacy alias would outlive the archives that
	// justify it.
	require.NotContains(t, string(entries["keg-archive/manifest.json"]), "with_config")
	require.Contains(t, entries, "keg-archive/keg.yaml")
	require.Contains(t, entries, "keg-archive/nodes/"+id.ID.Path()+"/assets/doc.txt")
	require.Contains(t, entries, "keg-archive/nodes/"+id.ID.Path()+"/images/diagram.png")
	for name := range entries {
		require.NotContains(t, name, "/files/")
	}
}

func TestArchiveExportFullBackupIncludesSchemas(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	require.NoError(t, src.CreateSchema(ctx, "task", archiveTaskSchema))

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

func TestArchiveImportRestoresKegSettingsForFullBackup(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	_, err := src.Create(ctx, &keg.CreateOptions{Body: []byte("# indexed\n"), Meta: []byte("tags:\n  - restored\n")})
	require.NoError(t, err)
	require.NoError(t, src.UpdateSettings(ctx, func(cfg *keg.Settings) {
		cfg.Title = "Restored Title"
		cfg.URL = "https://example.com/restored"
		cfg.Creator = "restorer"
		cfg.State = "archived"
		cfg.Summary = "Restored summary"
		cfg.Instructions = "Restore carefully."
		cfg.Timezone = "America/Chicago"
		cfg.Links = []keg.LinkEntry{{Alias: "docs", URL: "https://example.com/docs"}}
		cfg.Indexes = append(cfg.UserIndexEntries(), keg.IndexEntry{File: "restored.md", Summary: "Restored nodes", Query: "restored"})
		cfg.Snapshots = &keg.SnapshotSettings{Mode: keg.SnapshotModeOff, IdleAfter: "2h"}
		cfg.SchemaPolicy = &keg.SchemaPolicy{
			Human: keg.ValidationModeWarn,
			Agent: keg.ValidationModeBlock,
			API:   keg.ValidationModeBlock,
		}
	}))

	archive := mustExportArchive(t, src, keg.ExportNodesOptions{WithAssets: true})

	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	require.NoError(t, dst.UpdateSettings(ctx, func(cfg *keg.Settings) {
		cfg.Title = "Target Title"
		cfg.Summary = "Target summary"
		cfg.Timezone = "UTC"
	}))

	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{})
	require.NoError(t, err)

	got, err := dst.Settings(ctx)
	require.NoError(t, err)
	require.Equal(t, "Restored Title", got.Title)
	require.Equal(t, "https://example.com/restored", got.URL)
	require.Equal(t, "restorer", got.Creator)
	require.Equal(t, "archived", got.State)
	require.Equal(t, "Restored summary", got.Summary)
	require.Equal(t, "Restore carefully.", got.Instructions)
	require.Equal(t, "America/Chicago", got.Timezone)
	require.Equal(t, []keg.LinkEntry{{Alias: "docs", URL: "https://example.com/docs"}}, got.Links)
	require.Equal(t, &keg.SnapshotSettings{Mode: keg.SnapshotModeOff, IdleAfter: "2h"}, got.Snapshots)
	require.Equal(t, &keg.SchemaPolicy{Human: keg.ValidationModeWarn, Agent: keg.ValidationModeBlock, API: keg.ValidationModeBlock}, got.SchemaPolicy)

	rawIndex, err := dst.ReadIndex(ctx, "restored.md")
	require.NoError(t, err)
	require.Contains(t, string(rawIndex), "indexed")
}

// legacyConfigManifest rewrites a v3 manifest to the pre-rename spelling,
// producing the archive an older Tapper would have written. The format
// identifier is deliberately left alone: it was never bumped for the rename,
// which is exactly why these archives still reach the importer.
func legacyConfigManifest(t *testing.T, rawManifest []byte) []byte {
	t.Helper()
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(rawManifest, &manifest))
	withSettings, ok := manifest["with_settings"]
	require.True(t, ok, "fixture expects a manifest that carries keg settings")
	delete(manifest, "with_settings")
	manifest["with_config"] = withSettings
	rewritten, err := json.MarshalIndent(manifest, "", "  ")
	require.NoError(t, err)
	return rewritten
}

// A keg archive is a stored artifact that outlives the code that wrote it. The
// settings rename changed the manifest key from `with_config` to
// `with_settings` without bumping the format identifier, so a pre-rename
// archive still passes the version gate. Before the importer normalized the
// two spellings, it read WithSettings as false and silently dropped the keg
// document -- title, summary, instructions, and custom indexes -- while
// reporting a successful restore.
func TestArchiveImportRestoresKegSettingsFromLegacyConfigManifest(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	_, err := src.Create(ctx, &keg.CreateOptions{Body: []byte("# indexed\n"), Meta: []byte("tags:\n  - restored\n")})
	require.NoError(t, err)
	require.NoError(t, src.UpdateSettings(ctx, func(cfg *keg.Settings) {
		cfg.Title = "Legacy Title"
		cfg.Summary = "Legacy summary"
		cfg.Instructions = "Legacy instructions."
		cfg.Timezone = "America/Chicago"
		cfg.Indexes = append(cfg.UserIndexEntries(), keg.IndexEntry{File: "restored.md", Summary: "Restored nodes", Query: "restored"})
	}))

	archive := mustExportArchive(t, src, keg.ExportNodesOptions{WithAssets: true})
	entries := readArchiveEntriesForTest(t, archive)
	legacy := legacyConfigManifest(t, entries["keg-archive/manifest.json"])
	require.Contains(t, string(legacy), "with_config")
	require.NotContains(t, string(legacy), "with_settings")
	archive = replaceArchiveEntry(t, archive, "keg-archive/manifest.json", legacy)

	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	require.NoError(t, dst.UpdateSettings(ctx, func(cfg *keg.Settings) {
		cfg.Title = "Target Title"
		cfg.Summary = "Target summary"
		cfg.Instructions = "Target instructions."
		cfg.Timezone = "UTC"
	}))

	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{})
	require.NoError(t, err)

	// Each of these differs from what the destination was seeded with, so
	// matching the source proves the archived document was applied rather than
	// the import taking the "carried no settings" branch and leaving the
	// target's own values in place.
	got, err := dst.Settings(ctx)
	require.NoError(t, err)
	require.Equal(t, "Legacy Title", got.Title)
	require.Equal(t, "Legacy summary", got.Summary)
	require.Equal(t, "Legacy instructions.", got.Instructions)
	require.Equal(t, "America/Chicago", got.Timezone)

	rawIndex, err := dst.ReadIndex(ctx, "restored.md")
	require.NoError(t, err)
	require.Contains(t, string(rawIndex), "indexed")
}

func TestArchiveImportNodeSubsetDoesNotRestoreKegSettings(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	id, err := src.Create(ctx, &keg.CreateOptions{Body: []byte("# partial\n")})
	require.NoError(t, err)
	require.NoError(t, src.UpdateSettings(ctx, func(cfg *keg.Settings) {
		cfg.Title = "Source Title"
		cfg.Summary = "Source summary"
	}))

	archive := mustExportArchive(t, src, keg.ExportNodesOptions{NodeIDs: []keg.NodeId{id.ID}, WithAssets: true})
	entries := readArchiveEntriesForTest(t, archive)
	require.NotContains(t, entries, "keg-archive/keg.yaml")
	var manifest struct {
		WithSettings bool `json:"with_settings"`
	}
	require.NoError(t, json.Unmarshal(entries["keg-archive/manifest.json"], &manifest))
	require.False(t, manifest.WithSettings)

	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	require.NoError(t, dst.UpdateSettings(ctx, func(cfg *keg.Settings) {
		cfg.Title = "Target Title"
		cfg.Summary = "Target summary"
	}))

	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{})
	require.NoError(t, err)
	got, err := dst.Settings(ctx)
	require.NoError(t, err)
	require.Equal(t, "Target Title", got.Title)
	require.Equal(t, "Target summary", got.Summary)
}

func TestArchiveExportNodeSubsetOmitsSchemas(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	require.NoError(t, src.CreateSchema(ctx, "task", archiveTaskSchema))
	id, err := src.Create(ctx, &keg.CreateOptions{
		Body: []byte("# Partial\n"), Meta: []byte("type: task\n"),
	})
	require.NoError(t, err)

	entries := readArchiveEntriesForTest(t, mustExportArchive(t, src, keg.ExportNodesOptions{NodeIDs: []keg.NodeId{id.ID}}))

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

	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	markZeroAsTask(t, src)
	require.NoError(t, src.CreateSchema(ctx, "task", archivedSchema))
	_, err := src.Create(ctx, &keg.CreateOptions{
		Body: []byte("# Imported Task\n"), Meta: []byte("type: task\n"),
	})
	require.NoError(t, err)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{})

	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	require.NoError(t, dst.CreateSchema(ctx, "task", targetSchema))
	require.NoError(t, dst.CreateSchema(ctx, "decision", targetOnlySchema))

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

	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	markZeroAsTask(t, src)
	require.NoError(t, src.CreateSchema(ctx, "task", archiveTaskSchema))
	id, err := src.Create(ctx, &keg.CreateOptions{
		Body: []byte("# Accepted By Archive Schema\n"), Meta: []byte("type: task\n"),
	})
	require.NoError(t, err)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{})

	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	require.NoError(t, dst.CreateSchema(ctx, "task", targetSchema))

	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{})
	require.NoError(t, err)
	exists, err := dst.NodeExists(ctx, id.ID)
	require.NoError(t, err)
	require.True(t, exists)
}

func TestArchiveImportSkipsSchemaEnforcementEvenWithBlockOverride(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	require.NoError(t, src.CreateSchema(ctx, "task", archiveTaskSchema))
	humanCtx := keg.WithValidationActor(ctx, keg.ValidationActorHuman)
	id, err := src.Create(humanCtx, &keg.CreateOptions{Body: []byte("# Missing Type\n")})
	require.NoError(t, err)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{})

	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	blockCtx := keg.WithValidationMode(ctx, keg.ValidationModeBlock)
	_, err = dst.ImportNodes(blockCtx, bytes.NewReader(archive), keg.ImportNodesOptions{})
	require.NoError(t, err)
	result, err := dst.ValidateNode(ctx, id.ID)
	require.NoError(t, err)
	require.True(t, result.Valid)
}

func TestArchiveImportDropsLegacySchemaPolicyFields(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{})
	legacyConfig := []byte(`kegv: "2025-07"
title: Legacy policy
schemaPolicy:
  default: off
  human: warn
  agent: off
  api: block
  import: block
  restore: warn
`)
	archive = replaceArchiveEntry(t, archive, "keg-archive/keg.yaml", legacyConfig)

	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	_, err := dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{})
	require.NoError(t, err)
	cfg, err := dst.Settings(ctx)
	require.NoError(t, err)
	require.Equal(t, &keg.SchemaPolicy{
		Human: keg.ValidationModeWarn,
		Agent: keg.ValidationModeOff,
		API:   keg.ValidationModeBlock,
	}, cfg.SchemaPolicy)
	yamlOut, err := cfg.ToYAML()
	require.NoError(t, err)
	require.NotContains(t, string(yamlOut), "  default:")
	require.NotContains(t, string(yamlOut), "  import:")
	require.NotContains(t, string(yamlOut), "  restore:")
}

func TestArchiveImportRejectsMalformedSchemaBeforeWritingNodes(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	markZeroAsTask(t, src)
	require.NoError(t, src.CreateSchema(ctx, "task", archiveTaskSchema))
	id, err := src.Create(ctx, &keg.CreateOptions{
		Body: []byte("# Imported Task\n"), Meta: []byte("type: task\n"),
	})
	require.NoError(t, err)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{})
	broken := replaceArchiveEntry(t, archive, "keg-archive/schemas/task.schema.yaml", []byte("type: ["))

	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	_, err = dst.ImportNodes(ctx, bytes.NewReader(broken), keg.ImportNodesOptions{})
	require.Error(t, err)
	exists, err := dst.NodeExists(ctx, id.ID)
	require.NoError(t, err)
	require.False(t, exists)
	_, err = dst.ReadSchema(ctx, "task")
	require.ErrorIs(t, err, keg.ErrNotExist)
}

func TestArchiveImportRejectsSchemasWhenTargetDoesNotSupportThemBeforeWritingNodes(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	markZeroAsTask(t, src)
	require.NoError(t, src.CreateSchema(ctx, "task", archiveTaskSchema))
	id, err := src.Create(ctx, &keg.CreateOptions{
		Body: []byte("# Imported Task\n"), Meta: []byte("type: task\n"),
	})
	require.NoError(t, err)
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{})

	dstRepo := &repoWithoutSchemas{Repository: newTestMemoryRepo(fx.Runtime())}
	dst := keg.NewLocalKeg(dstRepo, fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	_, err = dst.ImportNodes(ctx, bytes.NewReader(archive), keg.ImportNodesOptions{})
	require.ErrorIs(t, err, keg.ErrNotSupported)
	exists, err := dst.NodeExists(ctx, id.ID)
	require.NoError(t, err)
	require.False(t, exists)
}

func TestArchiveImportRejectsNestedAssetNameBeforeWritingNodes(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	src := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	id, err := src.Create(ctx, &keg.CreateOptions{Body: []byte("# asset node\n")})
	require.NoError(t, err)
	require.NoError(t, src.WriteFile(ctx, id.ID, "doc.txt", []byte("doc bytes")))
	archive := mustExportArchive(t, src, keg.ExportNodesOptions{WithAssets: true})

	broken := retarWithRenamedEntry(
		t,
		archive,
		"keg-archive/nodes/"+id.ID.Path()+"/assets/doc.txt",
		"keg-archive/nodes/"+id.ID.Path()+"/assets/nested/doc.txt",
	)

	dst := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	_, err = dst.ImportNodes(ctx, bytes.NewReader(broken), keg.ImportNodesOptions{})
	require.ErrorIs(t, err, keg.ErrInvalidAssetName)
	exists, err := dst.NodeExists(ctx, id.ID)
	require.NoError(t, err)
	require.False(t, exists)
}
