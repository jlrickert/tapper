package keg

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Archive format identifiers. v3 adds optional keg settings, optional keg schemas,
// and stores file attachments under assets/ to match the on-disk/web node layout.
//
// v3 has two manifest spellings for the keg document. It was written as
// `with_config` before the settings rename and `with_settings` after, and the
// format identifier was not bumped between them, so both are accepted on read
// and normalized in importNodes. The entry itself has always been
// keg-archive/keg.yaml.
const (
	kegArchiveFormatV3 = "keg-archive/v3"
)

type archiveManifest struct {
	Format       string    `json:"format"`
	Source       string    `json:"source,omitempty"`
	ExportedAt   time.Time `json:"exported_at"`
	WithHistory  bool      `json:"with_history,omitempty"`
	WithSettings bool      `json:"with_settings,omitempty"`
	// WithConfig is the pre-rename spelling of WithSettings. An archive is a
	// stored artifact that outlives the code that wrote it, and v3 tarballs
	// carrying this spelling are already in users' hands, so it stays readable.
	// Read-only: export never writes it, and importNodes folds it into
	// WithSettings immediately after unmarshalling.
	WithConfig  bool                  `json:"with_config,omitempty"`
	WithSchemas bool                  `json:"with_schemas,omitempty"`
	Schemas     []string              `json:"schemas,omitempty"`
	Nodes       []archiveManifestNode `json:"nodes"`
}

type archiveManifestNode struct {
	SourceID      string `json:"source_id"`
	SourceHash    string `json:"source_hash"`
	RevisionCount int    `json:"revision_count,omitempty"`
}

// ExportNodes returns a reader for a keg-archive (gzip tar) captured from one
// coherent read snapshot. LocalKeg materializes the archive before returning,
// so the operation boundary is released while the caller reads the artifact
// and a slow download does not block same-keg writers.
//
// When opts.Spool is set, ExportNodes owns it: on any error the spool has
// been discarded, so the caller must not reuse it.
func (k *LocalKeg) ExportNodes(ctx context.Context, opts ExportNodesOptions) (rc io.ReadCloser, err error) {
	if opts.Spool != nil {
		defer func() {
			if err == nil {
				return
			}
			if rc != nil {
				// The read boundary failed after the spool's reader was
				// opened. Closing it releases the spool; Discard below is
				// idempotent per the ArchiveSpool contract.
				err = errors.Join(err, rc.Close())
				rc = nil
			}
			err = errors.Join(err, opts.Spool.Discard())
		}()
	}
	return withKegReadValue(ctx, k, func(ctx context.Context) (io.ReadCloser, error) {
		return k.exportNodes(ctx, opts)
	})
}

func (k *LocalKeg) exportNodes(ctx context.Context, opts ExportNodesOptions) (io.ReadCloser, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to export nodes: %w", err)
	}

	ids := append([]NodeId(nil), opts.NodeIDs...)
	if q := strings.TrimSpace(opts.Query); q != "" {
		entries, err := k.Query(ctx, QueryOptions{Expr: q})
		if err != nil {
			return nil, err
		}
		seen := map[string]struct{}{}
		for _, id := range ids {
			seen[id.Path()] = struct{}{}
		}
		for _, entry := range entries {
			if id, e := ParseNode(entry.ID); e == nil && id != nil {
				if _, ok := seen[id.Path()]; !ok {
					ids = append(ids, *id)
					seen[id.Path()] = struct{}{}
				}
			}
		}
	}
	if len(ids) == 0 && strings.TrimSpace(opts.Query) == "" {
		var err error
		ids, err = k.Repo.ListNodes(ctx)
		if err != nil {
			return nil, fmt.Errorf("unable to list nodes: %w", err)
		}
	}
	if opts.SkipZeroNode {
		filtered := ids[:0]
		for _, id := range ids {
			if id.ID != 0 {
				filtered = append(filtered, id)
			}
		}
		ids = filtered
	}
	ids = append([]NodeId(nil), ids...)
	slices.SortFunc(ids, func(a, b NodeId) int { return a.Compare(b) })

	var snapshotRepo RepositorySnapshots
	if opts.WithHistory {
		var ok bool
		snapshotRepo, ok = repoSnapshots(k.Repo)
		if !ok && opts.HistoryIfSupported {
			opts.WithHistory = false
		} else if !ok {
			return nil, ErrNotSupported
		}
	}

	if opts.Spool == nil {
		var artifact bytes.Buffer
		if err := k.writeArchive(ctx, &artifact, ids, snapshotRepo, opts); err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(artifact.Bytes())), nil
	}
	// ExportNodes discards the spool on every error return, this one included.
	if err := k.writeArchive(ctx, opts.Spool, ids, snapshotRepo, opts); err != nil {
		return nil, err
	}
	return opts.Spool.Reader()
}

// writeArchive writes the complete keg-archive representation to w. The caller
// decides whether w is a materialized buffer or a streaming destination.
func (k *LocalKeg) writeArchive(ctx context.Context, w io.Writer, ids []NodeId, snapshotRepo RepositorySnapshots, opts ExportNodesOptions) error {
	compressed := &countingWriter{w: w}
	gz := gzip.NewWriter(compressed)
	aw := newArchiveWriter(gz, opts.Limits)
	withSettings := len(opts.NodeIDs) == 0 && strings.TrimSpace(opts.Query) == "" && !opts.SkipZeroNode

	manifest := archiveManifest{
		Format:       kegArchiveFormatV3,
		Source:       opts.Source,
		ExportedAt:   k.Runtime.Clock().Now().UTC(),
		WithHistory:  opts.WithHistory,
		WithSettings: withSettings,
	}

	if withSettings {
		cfg, err := k.Repo.ReadSettings(ctx)
		if err != nil {
			return fmt.Errorf("unable to read keg settings for archive: %w", err)
		}
		rawSettings, err := cfg.ToYAML()
		if err != nil {
			return fmt.Errorf("unable to encode keg settings for archive: %w", err)
		}
		if err := aw.writeFile("keg-archive/keg.yaml", rawSettings); err != nil {
			return err
		}
		if err := k.writeArchiveSchemas(ctx, aw, &manifest); err != nil {
			return err
		}
	}

	for _, id := range ids {
		content, err := k.Repo.ReadContent(ctx, id)
		if err != nil {
			return fmt.Errorf("unable to read node %s content: %w", id.Path(), err)
		}
		meta, err := k.Repo.ReadMeta(ctx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return fmt.Errorf("unable to read node %s metadata: %w", id.Path(), err)
		}
		stats, err := k.Repo.ReadStats(ctx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return fmt.Errorf("unable to read node %s stats: %w", id.Path(), err)
		}
		if stats == nil {
			stats = &NodeStats{}
		}
		statsBytes, err := stats.ToJSON()
		if err != nil {
			return fmt.Errorf("unable to encode node %s stats: %w", id.Path(), err)
		}

		base := filepath.ToSlash(filepath.Join("keg-archive", "nodes", id.Path()))
		if err := aw.writeFile(base+"/README.md", content); err != nil {
			return err
		}
		if err := aw.writeFile(base+"/meta.yaml", meta); err != nil {
			return err
		}
		if err := aw.writeFile(base+"/stats.json", statsBytes); err != nil {
			return err
		}

		if opts.WithAssets {
			if err := k.writeArchiveAssets(ctx, aw, base, id); err != nil {
				return err
			}
		}

		entry := archiveManifestNode{SourceID: id.Path(), SourceHash: stats.Hash()}
		if opts.WithHistory {
			count, err := k.writeArchiveHistory(ctx, aw, base, id, snapshotRepo)
			if err != nil {
				return err
			}
			entry.RevisionCount = count
		}
		manifest.Nodes = append(manifest.Nodes, entry)
		if err := checkCompressedSize(compressed.n, opts.Limits); err != nil {
			return err
		}
	}

	rawManifest, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("unable to encode archive manifest: %w", err)
	}
	if err := aw.writeFile(archiveManifestName, rawManifest); err != nil {
		return err
	}
	if err := aw.close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("unable to finalize archive compression: %w", err)
	}
	return checkCompressedSize(compressed.n, opts.Limits)
}

// checkCompressedSize fails an export whose gzip stream has outgrown the
// compressed ceiling. Mid-export the count trails what gzip has buffered, so
// only the final check is exact.
func checkCompressedSize(n int64, lim ArchiveLimits) error {
	if lim.MaxCompressedBytes > 0 && n > lim.MaxCompressedBytes {
		return &ArchiveLimitError{Limit: ArchiveLimitCompressedBytes, Max: lim.MaxCompressedBytes, Actual: n}
	}
	return nil
}

// writeArchiveSchemas adds keg-level schema definitions to a full archive.
func (k *LocalKeg) writeArchiveSchemas(ctx context.Context, aw *archiveWriter, manifest *archiveManifest) error {
	store, ok := repoSchemas(k.Repo)
	if !ok {
		return nil
	}
	types, err := store.ListSchemas(ctx)
	if err != nil {
		return fmt.Errorf("unable to list schemas for archive: %w", err)
	}
	types = append([]string(nil), types...)
	slices.Sort(types)
	for _, typeName := range types {
		typeName = strings.TrimSpace(typeName)
		filename, err := SchemaFilename(typeName)
		if err != nil {
			return fmt.Errorf("invalid schema type %q from repository: %w", typeName, err)
		}
		rawSchema, err := store.ReadSchema(ctx, typeName)
		if err != nil {
			return fmt.Errorf("unable to read schema %q for archive: %w", typeName, err)
		}
		if _, err := validateSchemaDefinitionForType(typeName, rawSchema); err != nil {
			return fmt.Errorf("schema %q is invalid: %w", typeName, err)
		}
		if err := aw.writeFile("keg-archive/schemas/"+filename, rawSchema); err != nil {
			return err
		}
		manifest.Schemas = append(manifest.Schemas, typeName)
	}
	manifest.WithSchemas = len(manifest.Schemas) > 0
	return nil
}

// writeArchiveAssets adds assets/ and images/ entries for one node.
func (k *LocalKeg) writeArchiveAssets(ctx context.Context, aw *archiveWriter, base string, id NodeId) error {
	if files, ok := k.Repo.(RepositoryFiles); ok {
		names, err := files.ListFiles(ctx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return fmt.Errorf("unable to list files for node %s: %w", id.Path(), err)
		}
		for _, name := range names {
			data, err := files.ReadFile(ctx, id, name)
			if err != nil {
				return fmt.Errorf("unable to read file %s for node %s: %w", name, id.Path(), err)
			}
			if err := aw.writeFile(base+"/assets/"+name, data); err != nil {
				return err
			}
		}
	}
	if images, ok := k.Repo.(RepositoryImages); ok {
		names, err := images.ListImages(ctx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return fmt.Errorf("unable to list images for node %s: %w", id.Path(), err)
		}
		for _, name := range names {
			data, err := images.ReadImage(ctx, id, name)
			if err != nil {
				return fmt.Errorf("unable to read image %s for node %s: %w", name, id.Path(), err)
			}
			if err := aw.writeFile(base+"/images/"+name, data); err != nil {
				return err
			}
		}
	}
	if videos, ok := k.Repo.(RepositoryVideos); ok {
		names, err := videos.ListVideos(ctx, id)
		if err != nil {
			return err
		}
		for _, name := range names {
			data, err := videos.ReadVideo(ctx, id, name)
			if err != nil {
				return err
			}
			if err := aw.writeFile(base+"/videos/"+name, data); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeArchiveHistory adds snapshot payloads and the per-node snapshot index
// for one node, returning the revision count.
func (k *LocalKeg) writeArchiveHistory(ctx context.Context, aw *archiveWriter, base string, id NodeId, snapshotRepo RepositorySnapshots) (int, error) {
	history, err := snapshotRepo.ListSnapshots(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("unable to list snapshots for node %s: %w", id.Path(), err)
	}
	if len(history) == 0 {
		return 0, nil
	}

	exportHistory := make([]Snapshot, 0, len(history))
	for _, snap := range history {
		_, snapContent, snapMeta, snapStats, err := snapshotRepo.GetSnapshot(ctx, id, snap.ID, SnapshotReadOptions{ResolveContent: true})
		if err != nil {
			return 0, fmt.Errorf("unable to load snapshot %d for node %s: %w", snap.ID, id.Path(), err)
		}
		snap.IsCheckpoint = true
		exportHistory = append(exportHistory, snap)

		statsBytes, err := snapStats.ToJSON()
		if err != nil {
			return 0, fmt.Errorf("unable to encode snapshot %d stats for node %s: %w", snap.ID, id.Path(), err)
		}
		snapBase := base + "/snapshots/" + fmt.Sprintf("%d", snap.ID)
		if err := aw.writeFile(snapBase+".full", snapContent); err != nil {
			return 0, err
		}
		if err := aw.writeFile(snapBase+".meta", snapMeta); err != nil {
			return 0, err
		}
		if err := aw.writeFile(snapBase+".stats", statsBytes); err != nil {
			return 0, err
		}
	}
	rawIndex, err := json.MarshalIndent(exportHistory, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("unable to encode snapshot index for node %s: %w", id.Path(), err)
	}
	if err := aw.writeFile(base+"/snapshots/index.json", rawIndex); err != nil {
		return 0, err
	}
	return len(history), nil
}

// ImportNodes loads a keg-archive stream into the keg. Nodes land on their
// archive ids, replacing existing nodes (whose assets are preserved unless the
// archive carries its own). Derived state (dex, settings updated stamp) is
// rebuilt once after all nodes import.
func (k *LocalKeg) ImportNodes(ctx context.Context, r io.Reader, opts ImportNodesOptions) ([]ImportedNode, error) {
	return withKegAtomicWriteValue(ctx, k, func(ctx context.Context) ([]ImportedNode, error) {
		return k.importNodes(ctx, r, opts)
	})
}

func (k *LocalKeg) importNodes(ctx context.Context, r io.Reader, opts ImportNodesOptions) ([]ImportedNode, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to import nodes: %w", err)
	}
	if opts.ReplaceAll && opts.AssignNewIDs {
		return nil, fmt.Errorf("import cannot both replace the keg and assign new ids: %w", ErrInvalid)
	}
	progress := func(phase string, done, total int) {
		if opts.Progress != nil {
			opts.Progress(ImportProgress{Phase: phase, Done: done, Total: total})
		}
	}

	entries, err := openArchiveEntries(ctx, r, opts)
	if err != nil {
		return nil, err
	}

	rawManifest, ok, err := entries.Read("keg-archive/manifest.json")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("archive manifest missing: %w", ErrInvalid)
	}

	var manifest archiveManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return nil, fmt.Errorf("unable to parse archive manifest: %w", err)
	}
	if manifest.Format != kegArchiveFormatV3 {
		return nil, fmt.Errorf("unsupported archive format %q: %w", manifest.Format, ErrInvalid)
	}
	// Fold the pre-rename spelling forward before anything reads WithSettings.
	// Every later decision -- validating the document, restoring it, and the
	// inverted branch that stamps settings-updated when an archive carried no
	// document -- reads this one value, so normalizing here is what keeps a
	// legacy archive from importing as though it had no keg settings at all.
	if manifest.WithConfig {
		manifest.WithSettings = true
	}
	// Only a full-keg export carries keg settings, so WithSettings is what
	// marks an archive as holding the whole keg. Replacing a keg from a node
	// selection would prune every node the selection left out.
	if opts.ReplaceAll && !manifest.WithSettings {
		return nil, fmt.Errorf("replacing a keg needs a full-keg archive; this one was exported with a node selection: %w", ErrInvalid)
	}

	archivedSchemas, err := readArchiveSchemas(entries, manifest)
	if err != nil {
		return nil, err
	}
	if archivedSchemas != nil {
		if _, ok := repoSchemas(k.Repo); !ok {
			return nil, fmt.Errorf("archive contains schemas: %w", ErrNotSupported)
		}
	}

	snapshotRepo, hasSnapshots := repoSnapshots(k.Repo)
	importHistory := manifest.WithHistory
	if manifest.WithHistory && !hasSnapshots {
		if !opts.HistoryIfSupported {
			return nil, ErrNotSupported
		}
		importHistory = false
	}

	mapping, ordered, err := resolveImportedNodeIDs(manifest.Nodes)
	if err != nil {
		return nil, err
	}
	if opts.restoreTo != nil {
		if len(ordered) != 1 {
			return nil, fmt.Errorf("a trash bundle holds one node, not %d: %w", len(ordered), ErrInvalid)
		}
		mapping[ordered[0]] = *opts.restoreTo
	}
	if opts.restoreFromTrash {
		if err := k.mapPurgedLinks(ctx, entries, ordered, mapping); err != nil {
			return nil, err
		}
	}
	if opts.AssignNewIDs && len(ordered) > 0 {
		reserved := make([]NodeId, 0, len(ordered))
		defer func() { k.cleanupUnusedImportReservations(ctx, reserved) }()
		for _, sourceID := range ordered {
			id, nextErr := k.Repo.Next(ctx)
			if nextErr != nil {
				return nil, fmt.Errorf("unable to allocate node ids for import: %w", nextErr)
			}
			mapping[sourceID] = id
			reserved = append(reserved, id)
		}
	}
	manifestNodes := make(map[string]archiveManifestNode, len(manifest.Nodes))
	for _, node := range manifest.Nodes {
		manifestNodes[node.SourceID] = node
	}

	if manifest.WithSettings {
		rawSettings, err := readRequiredArchiveEntry(entries, "keg-archive/keg.yaml")
		if err != nil {
			return nil, fmt.Errorf("archive missing keg settings: %w", err)
		}
		if _, err := ParseKegSettingsStrict(rawSettings); err != nil {
			return nil, fmt.Errorf("archive keg settings is invalid: %w", err)
		}
	}
	if err := validateArchiveAssetEntries(entries); err != nil {
		return nil, err
	}

	for i, sourceID := range ordered {
		progress(ImportPhaseValidating, i, len(ordered))
		newID := mapping[sourceID]
		base := filepath.ToSlash(filepath.Join("keg-archive", "nodes", sourceID))
		content, err := readRequiredArchiveEntry(entries, base+"/README.md")
		if err != nil {
			return nil, fmt.Errorf("archive node %s missing README.md: %w", sourceID, err)
		}
		meta, err := readRequiredArchiveEntry(entries, base+"/meta.yaml")
		if err != nil {
			return nil, fmt.Errorf("archive node %s missing meta.yaml: %w", sourceID, err)
		}
		statsBytes, err := readRequiredArchiveEntry(entries, base+"/stats.json")
		if err != nil {
			return nil, fmt.Errorf("archive node %s missing stats.json: %w", sourceID, err)
		}

		content = rewriteArchiveLinks(content, opts.SourceAlias, opts.TargetAlias, mapping)
		parsedContent, err := ParseContent(k.Runtime, content, MarkdownContentFilename)
		if err != nil {
			return nil, fmt.Errorf("unable to parse imported content for node %s: %w", sourceID, err)
		}
		parsedMeta, err := ParseMeta(ctx, meta)
		if err != nil {
			return nil, fmt.Errorf("unable to parse imported metadata for node %s: %w", sourceID, err)
		}
		stats, err := ParseStats(ctx, statsBytes)
		if err != nil {
			return nil, fmt.Errorf("unable to parse imported stats for node %s: %w", sourceID, err)
		}
		remapStatsLinks(stats, mapping)
		proposed := &NodeData{ID: newID, Content: parsedContent, Meta: parsedMeta, Stats: stats}
		if err := proposed.updateMeta(ctx, k.Runtime, nil); err != nil {
			return nil, fmt.Errorf("unable to update imported metadata for node %s: %w", sourceID, err)
		}
		if archivedSchemas != nil {
			err = k.validateForWriteWithSchemas(ctx, schemaWriteImport, newID, proposed, archivedSchemas)
		} else {
			err = k.validateForWrite(ctx, schemaWriteImport, newID, proposed)
		}
		if err != nil {
			return nil, fmt.Errorf("imported node %s: %w", sourceID, err)
		}
	}

	// An archive always lands on its own ids. A trashed node holding one
	// of them moves to a fresh id first, so the trashed copy stays
	// restorable rather than being lost or blocking the import.
	if !opts.AssignNewIDs && !opts.restoreFromTrash {
		landing := make(map[NodeId]bool, len(ordered))
		highest := NodeId{}
		for _, sourceID := range ordered {
			id := mapping[sourceID]
			landing[id] = true
			if id.Code == "" && id.ID > highest.ID {
				highest = id
			}
		}
		// Raise the counter past every id the archive lands on before
		// displacing anything, so no displaced entry is handed one of them.
		if counter, ok := k.Repo.(RepositoryNodeCounter); ok && len(ordered) > 0 {
			if err := counter.RaiseNodeCounter(ctx, highest); err != nil {
				return nil, fmt.Errorf("unable to reserve the archive's node ids: %w", err)
			}
		}
		for _, sourceID := range ordered {
			if _, _, err := k.displaceTrashed(ctx, mapping[sourceID], landing); err != nil {
				return nil, err
			}
		}
	}
	// A trash restore never replaces a live node: refuse before anything
	// is written.
	if opts.restoreFromTrash {
		for _, sourceID := range ordered {
			exists, err := k.nodeExistsWithContent(ctx, mapping[sourceID])
			if err != nil {
				return nil, err
			}
			if exists {
				return nil, fmt.Errorf("node %s exists, so a trashed copy cannot be restored over it: %w", mapping[sourceID].Path(), ErrDestinationExists)
			}
		}
	}

	// Preserve assets of nodes about to be replaced, then delete them so the
	// archive payload lands on clean state.
	preservedAssets := make(map[string]importedNodeAssets, len(ordered))
	for _, sourceID := range ordered {
		newID := mapping[sourceID]
		exists, err := k.nodeExistsWithContent(ctx, newID)
		if err != nil {
			return nil, fmt.Errorf("unable to check existing node %s before import: %w", sourceID, err)
		}
		if !exists {
			continue
		}

		assets, err := readImportedNodeAssets(ctx, k.Repo, newID)
		if err != nil {
			return nil, fmt.Errorf("unable to read existing assets for node %s: %w", sourceID, err)
		}
		preservedAssets[sourceID] = assets

		if err := k.Repo.DeleteNode(ctx, newID); err != nil {
			return nil, fmt.Errorf("unable to replace imported node %s: %w", sourceID, err)
		}
	}

	for i, sourceID := range ordered {
		progress(ImportPhaseImporting, i, len(ordered))
		newID := mapping[sourceID]
		nodeManifest := manifestNodes[sourceID]
		base := filepath.ToSlash(filepath.Join("keg-archive", "nodes", sourceID))

		content, err := readRequiredArchiveEntry(entries, base+"/README.md")
		if err != nil {
			return nil, fmt.Errorf("archive node %s missing README.md: %w", sourceID, err)
		}
		meta, err := readRequiredArchiveEntry(entries, base+"/meta.yaml")
		if err != nil {
			return nil, fmt.Errorf("archive node %s missing meta.yaml: %w", sourceID, err)
		}
		statsBytes, err := readRequiredArchiveEntry(entries, base+"/stats.json")
		if err != nil {
			return nil, fmt.Errorf("archive node %s missing stats.json: %w", sourceID, err)
		}

		content = rewriteArchiveLinks(content, opts.SourceAlias, opts.TargetAlias, mapping)
		stats, err := ParseStats(ctx, statsBytes)
		if err != nil {
			return nil, fmt.Errorf("unable to parse imported stats for node %s: %w", sourceID, err)
		}
		remapStatsLinks(stats, mapping)

		if err := k.Repo.WriteContent(ctx, newID, content); err != nil {
			return nil, fmt.Errorf("unable to write imported content for node %s: %w", sourceID, err)
		}
		if err := k.Repo.WriteMeta(ctx, newID, meta); err != nil {
			return nil, fmt.Errorf("unable to write imported metadata for node %s: %w", sourceID, err)
		}
		if err := k.Repo.WriteStats(ctx, newID, stats); err != nil {
			return nil, fmt.Errorf("unable to write imported stats for node %s: %w", sourceID, err)
		}

		if importHistory {
			if err := k.importNodeHistory(ctx, entries, base, sourceID, newID, nodeManifest, mapping, snapshotRepo, opts); err != nil {
				return nil, err
			}
		}

		if assets, ok := preservedAssets[sourceID]; ok {
			if err := restoreImportedNodeAssets(ctx, k.Repo, newID, assets); err != nil {
				return nil, fmt.Errorf("unable to restore existing assets for node %s: %w", sourceID, err)
			}
		}
		// Archive-carried assets land last so they win over preserved ones.
		if err := writeArchiveEntriesAssets(ctx, k.Repo, entries, base, newID); err != nil {
			return nil, fmt.Errorf("unable to write archived assets for node %s: %w", sourceID, err)
		}
	}

	if archivedSchemas != nil {
		if err := k.restoreArchiveSchemas(ctx, archivedSchemas); err != nil {
			return nil, err
		}
	}
	if manifest.WithSettings {
		rawSettings, err := readRequiredArchiveEntry(entries, "keg-archive/keg.yaml")
		if err != nil {
			return nil, fmt.Errorf("archive missing keg settings: %w", err)
		}
		if err := k.replaceSettings(ctx, rawSettings); err != nil {
			return nil, fmt.Errorf("unable to restore keg settings after import: %w", err)
		}
	}
	var removed int64
	if opts.ReplaceAll {
		progress(ImportPhasePruning, 0, 0)
		keep := make([]NodeId, 0, len(ordered)+1)
		for _, sourceID := range ordered {
			keep = append(keep, mapping[sourceID])
		}
		removed, err = k.pruneNodesExcept(ctx, keep)
		if err != nil {
			return nil, fmt.Errorf("unable to remove nodes the archive does not carry: %w", err)
		}
	}
	// A trash restore updates the dex itself, once for its whole batch,
	// rather than rebuilding it from every node for each restored one.
	if !opts.restoreFromTrash {
		progress(ImportPhaseIndexing, 0, 0)
		if err := k.rebuildDexFromRepo(ctx); err != nil {
			return nil, err
		}
		if !manifest.WithSettings {
			if err := k.touchSettingsUpdated(ctx, k.Runtime.Clock().Now()); err != nil {
				return nil, fmt.Errorf("unable to update keg settings after import: %w", err)
			}
		}
	}

	imported := make([]ImportedNode, 0, len(ordered))
	for _, sourceID := range ordered {
		imported = append(imported, ImportedNode{SourceID: sourceID, SourceHash: manifestNodes[sourceID].SourceHash, ID: mapping[sourceID]})
	}
	if opts.Report != nil {
		*opts.Report = ImportReport{Removed: removed}
	}
	return imported, nil
}

// pruneNodesExcept deletes every node not in keep, in bulk when the
// repository supports it. Nodes go through the repository rather than
// RemoveNodes so links in the surviving, just-imported content stay as the
// archive wrote them.
//
// Node 0 is always kept: it is the keg's landing node, and removing it makes
// the keg read as uninitialized.
func (k *LocalKeg) pruneNodesExcept(ctx context.Context, keep []NodeId) (int64, error) {
	keep = append(keep[:len(keep):len(keep)], NodeId{ID: 0})
	if pruner, ok := k.Repo.(RepositoryNodePruner); ok {
		return pruner.PruneNodesExcept(ctx, keep)
	}
	kept := make(map[string]struct{}, len(keep))
	for _, id := range keep {
		kept[id.Path()] = struct{}{}
	}
	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return 0, err
	}
	var removed int64
	for _, id := range ids {
		if _, ok := kept[id.Path()]; ok {
			continue
		}
		if err := k.Repo.DeleteNode(ctx, id); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// openArchiveEntries indexes the archive an import reads. A caller-staged
// Source is indexed in place; a stream is first staged into memory as a plain
// tar, which costs the archive's uncompressed size but never a second copy of
// every payload.
func openArchiveEntries(ctx context.Context, r io.Reader, opts ImportNodesOptions) (archiveEntries, error) {
	if opts.Source != nil {
		if r != nil {
			return nil, fmt.Errorf("import takes a reader or a staged source, not both: %w", ErrInvalid)
		}
		return indexArchive(ctx, opts.Source, opts.Limits)
	}
	if r == nil {
		return nil, fmt.Errorf("import has no archive: %w", ErrInvalid)
	}
	var staged bytes.Buffer
	if _, err := StageArchive(ctx, r, &staged, opts.Limits); err != nil {
		return nil, err
	}
	// StageArchive has already enforced the limits on this tar.
	return indexArchive(ctx, bytes.NewReader(staged.Bytes()), ArchiveLimits{})
}

func (k *LocalKeg) cleanupUnusedImportReservations(ctx context.Context, ids []NodeId) {
	for _, id := range ids {
		content, err := k.Repo.ReadContent(ctx, id)
		if errors.Is(err, ErrNotExist) || (err == nil && content == nil) {
			_ = k.Repo.DeleteNode(ctx, id)
		}
	}
}

// importNodeHistory replays a node's archived snapshot revisions.
func (k *LocalKeg) importNodeHistory(
	ctx context.Context,
	entries archiveEntries,
	base, sourceID string,
	newID NodeId,
	nodeManifest archiveManifestNode,
	mapping map[string]NodeId,
	snapshotRepo RepositorySnapshots,
	opts ImportNodesOptions,
) error {
	rawIndex, ok, err := entries.Read(base + "/snapshots/index.json")
	if err != nil {
		return err
	}
	if !ok {
		if nodeManifest.RevisionCount > 0 {
			return fmt.Errorf("archive node %s missing snapshots/index.json: %w", sourceID, ErrInvalid)
		}
		return nil
	}

	var history []Snapshot
	if err := json.Unmarshal(rawIndex, &history); err != nil {
		return fmt.Errorf("unable to parse snapshot history for node %s: %w", sourceID, err)
	}
	if nodeManifest.RevisionCount > 0 && len(history) != nodeManifest.RevisionCount {
		return fmt.Errorf("archive snapshot history count mismatch for node %s: expected %d, got %d: %w",
			sourceID, nodeManifest.RevisionCount, len(history), ErrInvalid)
	}

	var expectedParent RevisionID
	for _, snap := range history {
		content, err := readRequiredArchiveEntry(entries, base+"/snapshots/"+fmt.Sprintf("%d.full", snap.ID))
		if err != nil {
			return fmt.Errorf("archive snapshot %d for node %s missing .full payload: %w", snap.ID, sourceID, err)
		}
		content = rewriteArchiveLinks(content, opts.SourceAlias, opts.TargetAlias, mapping)
		meta, err := readRequiredArchiveEntry(entries, base+"/snapshots/"+fmt.Sprintf("%d.meta", snap.ID))
		if err != nil {
			return fmt.Errorf("archive snapshot %d for node %s missing .meta payload: %w", snap.ID, sourceID, err)
		}
		statsBytes, err := readRequiredArchiveEntry(entries, base+"/snapshots/"+fmt.Sprintf("%d.stats", snap.ID))
		if err != nil {
			return fmt.Errorf("archive snapshot %d for node %s missing .stats payload: %w", snap.ID, sourceID, err)
		}
		stats, err := ParseStats(ctx, statsBytes)
		if err != nil {
			return fmt.Errorf("unable to parse snapshot %d stats for node %s: %w", snap.ID, sourceID, err)
		}
		remapStatsLinks(stats, mapping)

		imported, err := snapshotRepo.AppendSnapshot(ctx, newID, SnapshotWrite{
			ExpectedParent: expectedParent,
			Message:        snap.Message,
			CreatedAt:      snap.CreatedAt,
			Meta:           meta,
			Stats:          stats,
			Content: SnapshotContentWrite{
				Kind: SnapshotContentKindFull,
				Base: expectedParent,
				Data: content,
				Hash: hashSnapshotBytes(k.Runtime, content),
			},
		})
		if err != nil {
			return fmt.Errorf("unable to import snapshot %d for node %s: %w", snap.ID, sourceID, err)
		}
		expectedParent = imported.ID
	}
	return nil
}

// rebuildDexFromRepo clears the dex and re-adds every node from repository
// state, then persists the result. Used after bulk imports where incremental
// dex updates would be wasteful.
func (k *LocalKeg) rebuildDexFromRepo(ctx context.Context) error {
	k.InvalidateDex()
	dex, err := k.ensureDexFresh(ctx)
	if err != nil {
		return fmt.Errorf("unable to load dex after import: %w", err)
	}
	dex.Clear(ctx)

	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return fmt.Errorf("unable to list nodes after import: %w", err)
	}

	for _, id := range ids {
		exists, err := k.nodeExistsWithContent(ctx, id)
		if err != nil {
			return fmt.Errorf("unable to inspect node %s for dex rebuild: %w", id.Path(), err)
		}
		if !exists {
			// Next reserves IDs in local repositories. A caller may therefore
			// leave a contentless reservation that is not yet a canonical node.
			continue
		}
		nodeData, err := k.loadNodeDataForDex(ctx, id)
		if err != nil {
			return fmt.Errorf("unable to read node %s for dex rebuild: %w", id.Path(), err)
		}
		if err := dex.Add(ctx, nodeData); err != nil {
			return fmt.Errorf("unable to add node %s to dex after import: %w", id.Path(), err)
		}
	}

	if err := dex.Write(ctx, k.Repo); err != nil {
		return fmt.Errorf("unable to write dex after import: %w", err)
	}
	k.dexMu.Lock()
	k.recordDexWrite()
	k.dexMu.Unlock()
	return nil
}

// loadNodeDataForDex assembles NodeData from repository state, tolerating
// missing meta/stats.
func (k *LocalKeg) loadNodeDataForDex(ctx context.Context, id NodeId) (*NodeData, error) {
	contentBytes, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		return nil, err
	}
	content, err := ParseContent(k.Runtime, contentBytes, FormatMarkdown)
	if err != nil {
		return nil, err
	}

	metaBytes, err := k.Repo.ReadMeta(ctx, id)
	if err != nil && !errors.Is(err, ErrNotExist) {
		return nil, err
	}
	var meta *NodeMeta
	if errors.Is(err, ErrNotExist) {
		meta = NewMeta(ctx, time.Time{})
	} else {
		meta, err = ParseMeta(ctx, metaBytes)
		if err != nil {
			return nil, err
		}
	}

	stats, err := k.Repo.ReadStats(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			stats = &NodeStats{}
		} else {
			return nil, err
		}
	}

	return &NodeData{
		ID:      id,
		Content: content,
		Meta:    meta,
		Stats:   stats,
	}, nil
}

// -- archive entry helpers

const (
	archiveSchemasDir    = "keg-archive/schemas"
	archiveSchemasPrefix = archiveSchemasDir + "/"
)

type archiveSchemaStore struct {
	schemas map[string][]byte
}

func readArchiveSchemas(entries archiveEntries, manifest archiveManifest) (*archiveSchemaStore, error) {
	manifestSchemas := make(map[string]struct{}, len(manifest.Schemas))
	for _, typeName := range manifest.Schemas {
		typeName = strings.TrimSpace(typeName)
		if err := ValidSchemaStorageName(typeName); err != nil {
			return nil, fmt.Errorf("invalid archive manifest schema %q: %w", typeName, err)
		}
		if _, exists := manifestSchemas[typeName]; exists {
			return nil, fmt.Errorf("duplicate archive manifest schema %q: %w", typeName, ErrInvalid)
		}
		manifestSchemas[typeName] = struct{}{}
	}

	store := &archiveSchemaStore{schemas: map[string][]byte{}}
	for _, name := range entries.WithPrefix(archiveSchemasDir) {
		if name == archiveSchemasDir {
			return nil, fmt.Errorf("invalid archive schema entry %q: %w", name, ErrInvalid)
		}
		if !strings.HasPrefix(name, archiveSchemasPrefix) {
			continue
		}

		data, err := readRequiredArchiveEntry(entries, name)
		if err != nil {
			return nil, err
		}
		typeName, err := archiveSchemaTypeFromEntry(name)
		if err != nil {
			return nil, err
		}
		if _, exists := store.schemas[typeName]; exists {
			return nil, fmt.Errorf("duplicate archive schema %q: %w", typeName, ErrInvalid)
		}
		if _, err := validateSchemaDefinitionForType(typeName, data); err != nil {
			return nil, fmt.Errorf("archive schema %q is invalid: %w", typeName, err)
		}
		store.schemas[typeName] = cloneBytes(data)
	}

	if manifest.WithSchemas && len(store.schemas) == 0 {
		return nil, fmt.Errorf("archive manifest declares schemas but no schema entries exist: %w", ErrInvalid)
	}
	if len(manifestSchemas) > 0 {
		for typeName := range manifestSchemas {
			if _, ok := store.schemas[typeName]; !ok {
				return nil, fmt.Errorf("archive manifest schema %q missing entry: %w", typeName, ErrInvalid)
			}
		}
		for typeName := range store.schemas {
			if _, ok := manifestSchemas[typeName]; !ok {
				return nil, fmt.Errorf("archive schema %q missing from manifest: %w", typeName, ErrInvalid)
			}
		}
	}

	if len(store.schemas) == 0 {
		return nil, nil
	}
	return store, nil
}

func archiveSchemaTypeFromEntry(name string) (string, error) {
	filename := strings.TrimPrefix(name, archiveSchemasPrefix)
	if filename == "" || strings.Contains(filename, "/") {
		return "", fmt.Errorf("invalid archive schema entry %q: %w", name, ErrInvalid)
	}
	if !strings.HasSuffix(filename, SchemaFileSuffix) {
		return "", fmt.Errorf("invalid archive schema filename %q: %w", filename, ErrInvalid)
	}
	typeName := strings.TrimSuffix(filename, SchemaFileSuffix)
	expected, err := SchemaFilename(typeName)
	if err != nil {
		return "", fmt.Errorf("invalid archive schema filename %q: %w", filename, err)
	}
	if filename != expected {
		return "", fmt.Errorf("invalid archive schema filename %q: %w", filename, ErrInvalid)
	}
	return typeName, nil
}

func (s *archiveSchemaStore) ListSchemas(ctx context.Context) ([]string, error) {
	_ = ctx
	names := make([]string, 0, len(s.schemas))
	for typeName := range s.schemas {
		names = append(names, typeName)
	}
	slices.Sort(names)
	return names, nil
}

func (s *archiveSchemaStore) ReadSchema(ctx context.Context, typeName string) ([]byte, error) {
	_ = ctx
	if err := ValidSchemaStorageName(typeName); err != nil {
		return nil, err
	}
	data, ok := s.schemas[strings.TrimSpace(typeName)]
	if !ok {
		return nil, ErrNotExist
	}
	return cloneBytes(data), nil
}

func (s *archiveSchemaStore) WriteSchema(ctx context.Context, typeName string, data []byte) error {
	_ = ctx
	typeName = strings.TrimSpace(typeName)
	if _, err := validateSchemaDefinitionForType(typeName, data); err != nil {
		return err
	}
	if s.schemas == nil {
		s.schemas = map[string][]byte{}
	}
	s.schemas[typeName] = cloneBytes(data)
	return nil
}

func (s *archiveSchemaStore) CreateSchema(ctx context.Context, typeName string, data []byte) error {
	typeName = strings.TrimSpace(typeName)
	if _, exists := s.schemas[typeName]; exists {
		return ErrExist
	}
	return s.WriteSchema(ctx, typeName, data)
}

func (s *archiveSchemaStore) DeleteSchema(ctx context.Context, typeName string) error {
	_ = ctx
	typeName = strings.TrimSpace(typeName)
	if err := ValidSchemaStorageName(typeName); err != nil {
		return err
	}
	if _, ok := s.schemas[typeName]; !ok {
		return ErrNotExist
	}
	delete(s.schemas, typeName)
	return nil
}

func (k *LocalKeg) restoreArchiveSchemas(ctx context.Context, schemas *archiveSchemaStore) error {
	store, ok := repoSchemas(k.Repo)
	if !ok {
		return ErrNotSupported
	}
	names, err := schemas.ListSchemas(ctx)
	if err != nil {
		return fmt.Errorf("unable to list archived schemas: %w", err)
	}
	for _, typeName := range names {
		rawSchema, err := schemas.ReadSchema(ctx, typeName)
		if err != nil {
			return fmt.Errorf("unable to read archived schema %q: %w", typeName, err)
		}
		if _, err := validateSchemaDefinitionForType(typeName, rawSchema); err != nil {
			return fmt.Errorf("archived schema %q is invalid: %w", typeName, err)
		}
		// The archive is already inside one atomic KEG boundary. Install the
		// complete schema set before validating the complete resulting state so
		// strict imports cannot fail on a transient, partially replaced set.
		if err := store.WriteSchema(ctx, typeName, rawSchema); err != nil {
			return fmt.Errorf("unable to restore schema %q after import: %w", typeName, err)
		}
	}
	return nil
}

func resolveImportedNodeIDs(nodes []archiveManifestNode) (map[string]NodeId, []string, error) {
	mapping := make(map[string]NodeId, len(nodes))
	ordered := make([]string, 0, len(nodes))
	for _, node := range nodes {
		// SourceID comes from the archive manifest, so it is always a bare id.
		id, err := ParseNode(node.SourceID)
		if err != nil || id == nil {
			return nil, nil, fmt.Errorf("invalid archive source node %q: %w", node.SourceID, ErrInvalid)
		}
		if _, exists := mapping[node.SourceID]; exists {
			return nil, nil, fmt.Errorf("duplicate archive source node %q: %w", node.SourceID, ErrInvalid)
		}
		mapping[node.SourceID] = *id
		ordered = append(ordered, node.SourceID)
	}
	return mapping, ordered, nil
}

// archiveRelLinkRE matches relative ../N node links in content.
var archiveRelLinkRE = regexp.MustCompile(`\.\./\s*([0-9]+)([[:space:]\)\]\}\>\.,;:!?'\"#]|$)`)

// archiveKegLinkRE matches keg:ALIAS/N cross-keg links anywhere in content.
var archiveKegLinkRE = regexp.MustCompile(`keg:([a-zA-Z0-9][a-zA-Z0-9_-]*)/([0-9]+)`)

// rewriteArchiveLinks rewrites node links in imported content:
//
//  1. ../N (imported)           → ../NEW_ID
//  2. ../N (not imported)       → keg:srcAlias/N  (only when srcAlias is set)
//  3. keg:tgtAlias/N            → ../N            (only when tgtAlias is set)
//  4. keg:srcAlias/N (imported) → ../NEW_ID       (only when srcAlias is set)
//  5. keg:srcAlias/N (other)    → unchanged
//  6. keg:otherAlias/N          → unchanged
//
// Relative links rewrite first, then cross-keg links, so pass-2 output is not
// re-processed by pass-1.
func rewriteArchiveLinks(raw []byte, srcAlias, tgtAlias string, mapping map[string]NodeId) []byte {
	if len(raw) == 0 {
		return raw
	}
	s := string(raw)

	s = archiveRelLinkRE.ReplaceAllStringFunc(s, func(match string) string {
		parts := archiveRelLinkRE.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		nodeNum, suffix := parts[1], parts[2]
		if dst, ok := mapping[nodeNum]; ok {
			return "../" + dst.Path() + suffix
		}
		if srcAlias != "" {
			return "keg:" + srcAlias + "/" + nodeNum + suffix
		}
		return match
	})

	if srcAlias != "" || tgtAlias != "" {
		s = archiveKegLinkRE.ReplaceAllStringFunc(s, func(match string) string {
			parts := archiveKegLinkRE.FindStringSubmatch(match)
			if len(parts) != 3 {
				return match
			}
			alias, nodeNum := parts[1], parts[2]
			if tgtAlias != "" && alias == tgtAlias {
				return "../" + nodeNum
			}
			if srcAlias != "" && alias == srcAlias {
				if dst, ok := mapping[nodeNum]; ok {
					return "../" + dst.Path()
				}
				return match
			}
			return match
		})
	}

	if s == string(raw) {
		return raw
	}
	return []byte(s)
}

func remapStatsLinks(stats *NodeStats, mapping map[string]NodeId) {
	if stats == nil || len(mapping) == 0 {
		return
	}
	links := stats.Links()
	for i := range links {
		if dst, ok := mapping[links[i].Path()]; ok {
			links[i] = dst
		}
	}
	stats.SetLinks(links)
}

type importedNodeAssets struct {
	files  map[string][]byte
	images map[string][]byte
	videos map[string][]byte
}

func readImportedNodeAssets(ctx context.Context, repo Repository, id NodeId) (importedNodeAssets, error) {
	assets := importedNodeAssets{
		files:  map[string][]byte{},
		images: map[string][]byte{},
		videos: map[string][]byte{},
	}

	if filesRepo, ok := repo.(RepositoryFiles); ok {
		names, err := filesRepo.ListFiles(ctx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return importedNodeAssets{}, err
		}
		for _, name := range names {
			data, err := filesRepo.ReadFile(ctx, id, name)
			if err != nil {
				return importedNodeAssets{}, err
			}
			assets.files[name] = append([]byte(nil), data...)
		}
	}

	if imagesRepo, ok := repo.(RepositoryImages); ok {
		names, err := imagesRepo.ListImages(ctx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return importedNodeAssets{}, err
		}
		for _, name := range names {
			data, err := imagesRepo.ReadImage(ctx, id, name)
			if err != nil {
				return importedNodeAssets{}, err
			}
			assets.images[name] = append([]byte(nil), data...)
		}
	}

	if videos, ok := repo.(RepositoryVideos); ok {
		names, err := videos.ListVideos(ctx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return importedNodeAssets{}, err
		}
		for _, name := range names {
			data, err := videos.ReadVideo(ctx, id, name)
			if err != nil {
				return importedNodeAssets{}, err
			}
			assets.videos[name] = append([]byte(nil), data...)
		}
	}

	return assets, nil
}

func restoreImportedNodeAssets(ctx context.Context, repo Repository, id NodeId, assets importedNodeAssets) error {
	if filesRepo, ok := repo.(RepositoryFiles); ok {
		for name, data := range assets.files {
			if err := filesRepo.WriteFile(ctx, id, name, data); err != nil {
				return err
			}
		}
	}

	if imagesRepo, ok := repo.(RepositoryImages); ok {
		for name, data := range assets.images {
			if err := imagesRepo.WriteImage(ctx, id, name, data); err != nil {
				return err
			}
		}
	}

	if videos, ok := repo.(RepositoryVideos); ok {
		for name, data := range assets.videos {
			if err := videos.WriteVideo(ctx, id, name, data); err != nil {
				return err
			}
		}
	}

	return nil
}

// writeArchiveEntriesAssets writes a v3 archive's assets/, images/, and
// videos/ payloads for one node onto the repository.
func writeArchiveEntriesAssets(ctx context.Context, repo Repository, entries archiveEntries, base string, id NodeId) error {
	filesRepo, hasFiles := repo.(RepositoryFiles)
	imagesRepo, hasImages := repo.(RepositoryImages)

	videoPrefix := base + "/videos/"
	for _, name := range entries.WithPrefix(videoPrefix) {
		videos, ok := repo.(RepositoryVideos)
		if !ok {
			return ErrNotSupported
		}
		assetName := strings.TrimPrefix(name, videoPrefix)
		if err := validAssetName(assetName); err != nil {
			return err
		}
		data, err := readRequiredArchiveEntry(entries, name)
		if err != nil {
			return err
		}
		if err := ValidateVideo(data); err != nil {
			return err
		}
		if err := videos.WriteVideo(ctx, id, assetName, data); err != nil {
			return err
		}
	}

	filePrefix := base + "/assets/"
	for _, name := range entries.WithPrefix(filePrefix) {
		if name == filePrefix {
			continue
		}
		if !hasFiles {
			return ErrNotSupported
		}
		assetName := name[len(filePrefix):]
		if err := validAssetName(assetName); err != nil {
			return err
		}
		data, err := readRequiredArchiveEntry(entries, name)
		if err != nil {
			return err
		}
		if err := filesRepo.WriteFile(ctx, id, assetName, data); err != nil {
			return err
		}
	}

	imagePrefix := base + "/images/"
	for _, name := range entries.WithPrefix(imagePrefix) {
		if name == imagePrefix {
			continue
		}
		if !hasImages {
			return ErrNotSupported
		}
		assetName := name[len(imagePrefix):]
		if err := validAssetName(assetName); err != nil {
			return err
		}
		data, err := readRequiredArchiveEntry(entries, name)
		if err != nil {
			return err
		}
		if err := imagesRepo.WriteImage(ctx, id, assetName, data); err != nil {
			return err
		}
	}
	return nil
}

func validateArchiveAssetEntries(entries archiveEntries) error {
	for _, name := range entries.WithPrefix("keg-archive/nodes/") {
		if err := checkArchiveAssetEntry(name); err != nil {
			return err
		}
	}
	return nil
}

// checkArchiveAssetEntry rejects a node attachment entry (under a node's
// assets/, images/, or videos/) whose name import would refuse to write, so
// staging and import agree on which archives are valid. The error wraps both
// ErrInvalidAssetName and ErrInvalid: the archive, not the keg, is at fault.
// Other entries pass.
func checkArchiveAssetEntry(name string) error {
	rest, ok := strings.CutPrefix(name, "keg-archive/nodes/")
	if !ok {
		return nil
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return nil
	}
	switch parts[1] {
	case "assets", "images", "videos":
		if err := validAssetName(strings.Join(parts[2:], "/")); err != nil {
			return fmt.Errorf("archive asset entry %q: %w: %w", name, err, ErrInvalid)
		}
	}
	return nil
}
