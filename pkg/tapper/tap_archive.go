package tapper

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

const kegArchiveFormat = "keg-archive/v1"

type ExportOptions struct {
	KegTargetOptions
	NodeIDs     []string
	WithHistory bool
	OutputPath  string
}

type ImportOptions struct {
	KegTargetOptions
	Input string
}

type archiveManifest struct {
	Format      string                `json:"format"`
	Source      string                `json:"source,omitempty"`
	ExportedAt  time.Time             `json:"exported_at"`
	WithHistory bool                  `json:"with_history,omitempty"`
	Nodes       []archiveManifestNode `json:"nodes"`
}

type archiveManifestNode struct {
	SourceID      string `json:"source_id"`
	RevisionCount int    `json:"revision_count,omitempty"`
}

func (t *Tap) Export(ctx context.Context, opts ExportOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}

	nodeIDs, err := exportNodeIDs(ctx, k, opts.NodeIDs)
	if err != nil {
		return "", err
	}

	manifest := archiveManifest{
		Format:      kegArchiveFormat,
		ExportedAt:  t.Runtime.Clock().Now().UTC(),
		WithHistory: opts.WithHistory,
	}
	if k.Target != nil {
		manifest.Source = k.Target.String()
	}

	var snapshotRepo keg.RepositorySnapshots
	if opts.WithHistory {
		var ok bool
		snapshotRepo, ok = k.Repo.(keg.RepositorySnapshots)
		if !ok {
			return "", keg.ErrNotSupported
		}
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, id := range nodeIDs {
		content, err := k.Repo.ReadContent(ctx, id)
		if err != nil {
			return "", fmt.Errorf("unable to read node %s content: %w", id.Path(), err)
		}
		meta, err := readOptionalNodeMeta(ctx, k.Repo, id)
		if err != nil {
			return "", fmt.Errorf("unable to read node %s metadata: %w", id.Path(), err)
		}
		stats, err := readOptionalNodeStats(ctx, k.Repo, id)
		if err != nil {
			return "", fmt.Errorf("unable to read node %s stats: %w", id.Path(), err)
		}

		base := filepath.ToSlash(filepath.Join("keg-archive", "nodes", id.Path()))
		if err := writeTarFile(tw, base+"/README.md", content); err != nil {
			return "", err
		}
		if err := writeTarFile(tw, base+"/meta.yaml", meta); err != nil {
			return "", err
		}
		if err := writeTarFile(tw, base+"/stats.json", stats); err != nil {
			return "", err
		}

		entry := archiveManifestNode{SourceID: id.Path()}
		if opts.WithHistory {
			history, err := snapshotRepo.ListSnapshots(ctx, id)
			if err != nil {
				return "", fmt.Errorf("unable to list snapshots for node %s: %w", id.Path(), err)
			}
			entry.RevisionCount = len(history)
			if len(history) > 0 {
				exportHistory := make([]keg.Snapshot, 0, len(history))
				for _, snap := range history {
					_, snapContent, snapMeta, snapStats, err := snapshotRepo.GetSnapshot(ctx, id, snap.ID, keg.SnapshotReadOptions{ResolveContent: true})
					if err != nil {
						return "", fmt.Errorf("unable to load snapshot %d for node %s: %w", snap.ID, id.Path(), err)
					}
					snap.IsCheckpoint = true
					exportHistory = append(exportHistory, snap)

					statsBytes, err := snapStats.ToJSON()
					if err != nil {
						return "", fmt.Errorf("unable to encode snapshot %d stats for node %s: %w", snap.ID, id.Path(), err)
					}
					snapBase := base + "/snapshots/" + fmt.Sprintf("%d", snap.ID)
					if err := writeTarFile(tw, snapBase+".full", snapContent); err != nil {
						return "", err
					}
					if err := writeTarFile(tw, snapBase+".meta", snapMeta); err != nil {
						return "", err
					}
					if err := writeTarFile(tw, snapBase+".stats", statsBytes); err != nil {
						return "", err
					}
				}
				rawIndex, err := json.MarshalIndent(exportHistory, "", "  ")
				if err != nil {
					return "", fmt.Errorf("unable to encode snapshot index for node %s: %w", id.Path(), err)
				}
				if err := writeTarFile(tw, base+"/snapshots/index.json", rawIndex); err != nil {
					return "", err
				}
			}
		}

		manifest.Nodes = append(manifest.Nodes, entry)
	}

	rawManifest, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("unable to encode archive manifest: %w", err)
	}
	if err := writeTarFile(tw, "keg-archive/manifest.json", rawManifest); err != nil {
		return "", err
	}
	if err := tw.Close(); err != nil {
		return "", fmt.Errorf("unable to finalize archive: %w", err)
	}
	if err := gz.Close(); err != nil {
		return "", fmt.Errorf("unable to finalize archive compression: %w", err)
	}

	output, err := expandArchivePath(t.Runtime, opts.OutputPath)
	if err != nil {
		return "", err
	}
	if err := t.Runtime.Mkdir(filepath.Dir(output), 0o755, true); err != nil {
		return "", err
	}
	if err := t.Runtime.AtomicWriteFile(output, buf.Bytes(), 0o644); err != nil {
		return "", err
	}
	return output, nil
}

func (t *Tap) Import(ctx context.Context, opts ImportOptions) ([]keg.NodeId, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}

	archiveBytes, err := readArchiveInput(ctx, t.Runtime, opts.Input)
	if err != nil {
		return nil, err
	}
	entries, err := readArchiveEntries(archiveBytes)
	if err != nil {
		return nil, err
	}

	rawManifest, ok := entries["keg-archive/manifest.json"]
	if !ok {
		return nil, fmt.Errorf("archive manifest missing: %w", keg.ErrInvalid)
	}

	var manifest archiveManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return nil, fmt.Errorf("unable to parse archive manifest: %w", err)
	}
	if manifest.Format != kegArchiveFormat {
		return nil, fmt.Errorf("unsupported archive format %q: %w", manifest.Format, keg.ErrInvalid)
	}

	snapshotRepo, hasSnapshots := k.Repo.(keg.RepositorySnapshots)
	if manifest.WithHistory && !hasSnapshots {
		return nil, keg.ErrNotSupported
	}

	mapping, ordered, err := resolveImportedNodeIDs(manifest.Nodes)
	if err != nil {
		return nil, err
	}
	manifestNodes := make(map[string]archiveManifestNode, len(manifest.Nodes))
	for _, node := range manifest.Nodes {
		manifestNodes[node.SourceID] = node
	}

	preservedAssets := make(map[string]importedNodeAssets, len(ordered))
	for _, sourceID := range ordered {
		newID := mapping[sourceID]
		exists, err := k.Repo.HasNode(ctx, newID)
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

	for _, sourceID := range ordered {
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

		content = rewriteImportedLinks(content, mapping)
		stats, err := keg.ParseStats(ctx, statsBytes)
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

		indexPath := base + "/snapshots/index.json"
		if manifest.WithHistory {
			rawIndex, ok := entries[indexPath]
			if ok {
				var history []keg.Snapshot
				if err := json.Unmarshal(rawIndex, &history); err != nil {
					return nil, fmt.Errorf("unable to parse snapshot history for node %s: %w", sourceID, err)
				}
				if nodeManifest.RevisionCount > 0 && len(history) != nodeManifest.RevisionCount {
					return nil, fmt.Errorf("archive snapshot history count mismatch for node %s: expected %d, got %d: %w",
						sourceID, nodeManifest.RevisionCount, len(history), keg.ErrInvalid)
				}

				var expectedParent keg.RevisionID
				for _, snap := range history {
					content, err := readRequiredArchiveEntry(entries, base+"/snapshots/"+fmt.Sprintf("%d.full", snap.ID))
					if err != nil {
						return nil, fmt.Errorf("archive snapshot %d for node %s missing .full payload: %w", snap.ID, sourceID, err)
					}
					content = rewriteImportedLinks(content, mapping)
					meta, err := readRequiredArchiveEntry(entries, base+"/snapshots/"+fmt.Sprintf("%d.meta", snap.ID))
					if err != nil {
						return nil, fmt.Errorf("archive snapshot %d for node %s missing .meta payload: %w", snap.ID, sourceID, err)
					}
					statsBytes, err := readRequiredArchiveEntry(entries, base+"/snapshots/"+fmt.Sprintf("%d.stats", snap.ID))
					if err != nil {
						return nil, fmt.Errorf("archive snapshot %d for node %s missing .stats payload: %w", snap.ID, sourceID, err)
					}
					stats, err := keg.ParseStats(ctx, statsBytes)
					if err != nil {
						return nil, fmt.Errorf("unable to parse snapshot %d stats for node %s: %w", snap.ID, sourceID, err)
					}
					remapStatsLinks(stats, mapping)

					imported, err := snapshotRepo.AppendSnapshot(ctx, newID, keg.SnapshotWrite{
						ExpectedParent: expectedParent,
						Message:        snap.Message,
						Meta:           meta,
						Stats:          stats,
						Content: keg.SnapshotContentWrite{
							Kind: keg.SnapshotContentKindFull,
							Base: expectedParent,
							Data: content,
						},
					})
					if err != nil {
						return nil, fmt.Errorf("unable to import snapshot %d for node %s: %w", snap.ID, sourceID, err)
					}
					expectedParent = imported.ID
				}
			} else if nodeManifest.RevisionCount > 0 {
				return nil, fmt.Errorf("archive node %s missing snapshots/index.json: %w", sourceID, keg.ErrInvalid)
			}
		}
		if assets, ok := preservedAssets[sourceID]; ok {
			if err := restoreImportedNodeAssets(ctx, k.Repo, newID, assets); err != nil {
				return nil, fmt.Errorf("unable to restore existing assets for node %s: %w", sourceID, err)
			}
		}
	}

	if err := rebuildDexFromRepo(ctx, k); err != nil {
		return nil, err
	}
	if err := k.UpdateConfig(ctx, func(cfg *keg.Config) {
		cfg.Updated = t.Runtime.Clock().Now().UTC().Format(time.RFC3339)
	}); err != nil {
		return nil, fmt.Errorf("unable to update keg config after import: %w", err)
	}

	imported := make([]keg.NodeId, 0, len(ordered))
	for _, sourceID := range ordered {
		imported = append(imported, mapping[sourceID])
	}
	return imported, nil
}

func exportNodeIDs(ctx context.Context, k *keg.Keg, raw []string) ([]keg.NodeId, error) {
	if len(raw) == 0 {
		return k.Repo.ListNodes(ctx)
	}
	out := make([]keg.NodeId, 0, len(raw))
	for _, value := range raw {
		id, err := parseNodeID(value)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	slices.SortFunc(out, func(a, b keg.NodeId) int {
		return a.Compare(b)
	})
	return out, nil
}

func readOptionalNodeMeta(ctx context.Context, repo keg.Repository, id keg.NodeId) ([]byte, error) {
	_ = ctx
	data, err := repo.ReadMeta(ctx, id)
	if err != nil && !errors.Is(err, keg.ErrNotExist) {
		return nil, err
	}
	if errors.Is(err, keg.ErrNotExist) {
		return nil, nil
	}
	return data, nil
}

func readOptionalNodeStats(ctx context.Context, repo keg.Repository, id keg.NodeId) ([]byte, error) {
	stats, err := repo.ReadStats(ctx, id)
	if err != nil && !errors.Is(err, keg.ErrNotExist) {
		return nil, err
	}
	if errors.Is(err, keg.ErrNotExist) || stats == nil {
		stats = &keg.NodeStats{}
	}
	return stats.ToJSON()
}

func writeTarFile(tw *tar.Writer, name string, data []byte) error {
	header := &tar.Header{
		Name:     filepath.ToSlash(name),
		Mode:     0o644,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("unable to write archive header for %s: %w", name, err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("unable to write archive payload for %s: %w", name, err)
	}
	return nil
}

func expandArchivePath(rt *toolkit.Runtime, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("output path is required: %w", keg.ErrInvalid)
	}
	path := toolkit.ExpandEnv(rt, raw)
	if expanded, err := toolkit.ExpandPath(rt, path); err == nil {
		path = expanded
	}
	return filepath.Clean(path), nil
}

func readArchiveInput(ctx context.Context, rt *toolkit.Runtime, input string) ([]byte, error) {
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, input, nil)
		if err != nil {
			return nil, fmt.Errorf("unable to create archive request: %w", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("unable to download archive: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("unable to download archive: status %d", resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("unable to read archive download: %w", err)
		}
		return data, nil
	}

	path, err := expandArchivePath(rt, input)
	if err != nil {
		return nil, err
	}
	resolved, err := rt.ResolvePath(path, false)
	if err != nil {
		return nil, fmt.Errorf("unable to resolve archive path %s: %w", path, err)
	}
	if _, err := rt.Stat(resolved, false); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("archive not found: %s: %w", resolved, err)
		}
		return nil, fmt.Errorf("unable to stat archive %s: %w", resolved, err)
	}
	data, err := rt.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("unable to read archive %s: %w", resolved, err)
	}
	return data, nil
}

func readArchiveEntries(data []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err == nil {
		defer gz.Close()
		return readTarEntries(tar.NewReader(gz))
	}

	entries, tarErr := readTarEntries(tar.NewReader(bytes.NewReader(data)))
	if tarErr == nil {
		return entries, nil
	}

	return nil, fmt.Errorf("unable to open archive stream: gzip=%v; tar=%v", err, tarErr)
}

func readTarEntries(tr *tar.Reader) (map[string][]byte, error) {
	entries := map[string][]byte{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("unable to read archive entry: %w", err)
		}
		if header.FileInfo().IsDir() {
			continue
		}
		payload, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("unable to read archive payload %s: %w", header.Name, err)
		}
		entries[filepath.ToSlash(header.Name)] = payload
	}
	return entries, nil
}

func readRequiredArchiveEntry(entries map[string][]byte, path string) ([]byte, error) {
	value, ok := entries[path]
	if !ok {
		return nil, keg.ErrInvalid
	}
	return value, nil
}

func resolveImportedNodeIDs(nodes []archiveManifestNode) (map[string]keg.NodeId, []string, error) {
	mapping := make(map[string]keg.NodeId, len(nodes))
	ordered := make([]string, 0, len(nodes))
	for _, node := range nodes {
		id, err := parseNodeID(node.SourceID)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid archive source node %q: %w", node.SourceID, err)
		}
		if _, exists := mapping[node.SourceID]; exists {
			return nil, nil, fmt.Errorf("duplicate archive source node %q: %w", node.SourceID, keg.ErrInvalid)
		}
		mapping[node.SourceID] = id
		ordered = append(ordered, node.SourceID)
	}
	return mapping, ordered, nil
}

var importedNodeLinkRE = regexp.MustCompile(`\.\./\s*([0-9]+)([[:space:]\)\]\}\>\.,;:!?'\"#]|$)`)

func rewriteImportedLinks(raw []byte, mapping map[string]keg.NodeId) []byte {
	if len(raw) == 0 || len(mapping) == 0 {
		return raw
	}
	rewritten := importedNodeLinkRE.ReplaceAllStringFunc(string(raw), func(match string) string {
		parts := importedNodeLinkRE.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		dst, ok := mapping[parts[1]]
		if !ok {
			return match
		}
		return "../" + dst.Path() + parts[2]
	})
	if rewritten == string(raw) {
		return raw
	}
	return []byte(rewritten)
}

func remapStatsLinks(stats *keg.NodeStats, mapping map[string]keg.NodeId) {
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
}

func readImportedNodeAssets(ctx context.Context, repo keg.Repository, id keg.NodeId) (importedNodeAssets, error) {
	assets := importedNodeAssets{
		files:  map[string][]byte{},
		images: map[string][]byte{},
	}

	if filesRepo, ok := repo.(keg.RepositoryFiles); ok {
		names, err := filesRepo.ListFiles(ctx, id)
		if err != nil && !errors.Is(err, keg.ErrNotExist) {
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

	if imagesRepo, ok := repo.(keg.RepositoryImages); ok {
		names, err := imagesRepo.ListImages(ctx, id)
		if err != nil && !errors.Is(err, keg.ErrNotExist) {
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

	return assets, nil
}

func restoreImportedNodeAssets(ctx context.Context, repo keg.Repository, id keg.NodeId, assets importedNodeAssets) error {
	if filesRepo, ok := repo.(keg.RepositoryFiles); ok {
		for name, data := range assets.files {
			if err := filesRepo.WriteFile(ctx, id, name, data); err != nil {
				return err
			}
		}
	}

	if imagesRepo, ok := repo.(keg.RepositoryImages); ok {
		for name, data := range assets.images {
			if err := imagesRepo.WriteImage(ctx, id, name, data); err != nil {
				return err
			}
		}
	}

	return nil
}

func rebuildDexFromRepo(ctx context.Context, k *keg.Keg) error {
	dex, err := k.Dex(ctx)
	if err != nil {
		return fmt.Errorf("unable to load dex after import: %w", err)
	}
	dex.Clear(ctx)

	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return fmt.Errorf("unable to list nodes after import: %w", err)
	}

	for _, id := range ids {
		nodeData, err := loadNodeDataForDex(ctx, k, id)
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
	return nil
}

func loadNodeDataForDex(ctx context.Context, k *keg.Keg, id keg.NodeId) (*keg.NodeData, error) {
	contentBytes, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		return nil, err
	}
	content, err := keg.ParseContent(k.Runtime, contentBytes, keg.FormatMarkdown)
	if err != nil {
		return nil, err
	}

	metaBytes, err := k.Repo.ReadMeta(ctx, id)
	if err != nil && !errors.Is(err, keg.ErrNotExist) {
		return nil, err
	}
	var meta *keg.NodeMeta
	if errors.Is(err, keg.ErrNotExist) {
		meta = keg.NewMeta(ctx, time.Time{})
	} else {
		meta, err = keg.ParseMeta(ctx, metaBytes)
		if err != nil {
			return nil, err
		}
	}

	stats, err := k.Repo.ReadStats(ctx, id)
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			stats = &keg.NodeStats{}
		} else {
			return nil, err
		}
	}

	return &keg.NodeData{
		ID:      id,
		Content: content,
		Meta:    meta,
		Stats:   stats,
	}, nil
}
