package keg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

const (
	TimelineIndexName = "timeline"
	DirtyIndexName    = "dirty"
)

type timelineNodeRef struct {
	Node string `json:"node"`
}

type timelineIndexRow struct {
	V           int               `json:"v"`
	OccurredAt  string            `json:"occurred_at"`
	Node        string            `json:"node"`
	Revision    int64             `json:"revision"`
	Parent      int64             `json:"parent"`
	Schema      string            `json:"schema"`
	Title       string            `json:"title"`
	Message     string            `json:"message"`
	ContentHash string            `json:"content_hash"`
	Links       []timelineNodeRef `json:"links"`
	Backlinks   []timelineNodeRef `json:"backlinks"`
}

type dirtyIndexRow struct {
	V                int    `json:"v"`
	Node             string `json:"node"`
	CurrentHash      string `json:"current_hash"`
	SnapshotRevision int64  `json:"snapshot_revision"`
	SnapshotHash     string `json:"snapshot_hash"`
	Title            string `json:"title"`
}

type timelineSnapshotState struct {
	snapshot Snapshot
	schema   string
	title    string
	links    []NodeId
}

func (k *LocalKeg) refreshSnapshotGeneratedIndexes(ctx context.Context) error {
	timeline, err := k.buildTimelineIndexData(ctx)
	if err != nil {
		return err
	}
	if err := k.Repo.WriteIndex(ctx, TimelineIndexName, timeline); err != nil {
		return fmt.Errorf("write %s index: %w", TimelineIndexName, err)
	}
	return k.refreshDirtyIndex(ctx)
}

func (k *LocalKeg) refreshDirtyIndex(ctx context.Context) error {
	dirty, err := k.buildDirtyIndexData(ctx)
	if err != nil {
		return err
	}
	if err := k.Repo.WriteIndex(ctx, DirtyIndexName, dirty); err != nil {
		return fmt.Errorf("write %s index: %w", DirtyIndexName, err)
	}
	return nil
}

func (k *LocalKeg) buildTimelineIndexData(ctx context.Context) ([]byte, error) {
	snapshots, ok := repoSnapshots(k.Repo)
	if !ok {
		return []byte{}, nil
	}

	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list nodes for %s index: %w", TimelineIndexName, err)
	}
	slices.SortFunc(ids, func(a, b NodeId) int { return a.Compare(b) })

	records := make([]timelineSnapshotState, 0)
	for _, id := range ids {
		nodeSnapshots, err := snapshots.ListSnapshots(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("list snapshots for node %s: %w", id.Path(), err)
		}
		for _, snap := range nodeSnapshots {
			loaded, content, meta, stats, err := snapshots.GetSnapshot(ctx, id, snap.ID, SnapshotReadOptions{ResolveContent: true})
			if err != nil {
				return nil, fmt.Errorf("read snapshot node %s rev %d: %w", id.Path(), snap.ID, err)
			}
			loaded.Node = id
			state, err := timelineSnapshotStateFromPayload(ctx, k.Runtime, loaded, content, meta, stats)
			if err != nil {
				return nil, fmt.Errorf("parse snapshot node %s rev %d: %w", id.Path(), snap.ID, err)
			}
			records = append(records, state)
		}
	}

	slices.SortFunc(records, compareTimelineSnapshotState)

	rows := make([]timelineIndexRow, 0, len(records))
	latestLinks := make(map[string][]NodeId, len(records))
	for _, rec := range records {
		nodeKey := rec.snapshot.Node.Path()
		latestLinks[nodeKey] = normalizeNodeIDList(rec.links)
		rows = append(rows, timelineIndexRow{
			V:           1,
			OccurredAt:  formatIndexTime(rec.snapshot.CreatedAt),
			Node:        nodeKey,
			Revision:    int64(rec.snapshot.ID),
			Parent:      int64(rec.snapshot.Parent),
			Schema:      rec.schema,
			Title:       rec.title,
			Message:     rec.snapshot.Message,
			ContentHash: rec.snapshot.ContentHash,
			Links:       timelineRefs(rec.links),
			Backlinks:   timelineRefs(backlinksAtSnapshot(latestLinks, rec.snapshot.Node)),
		})
	}

	return jsonLines(rows)
}

func timelineSnapshotStateFromPayload(ctx context.Context, rt *toolkit.Runtime, snap Snapshot, contentBytes, metaBytes []byte, stats *NodeStats) (timelineSnapshotState, error) {
	content, err := ParseContent(rt, contentBytes, FormatMarkdown)
	if err != nil {
		return timelineSnapshotState{}, err
	}

	var meta *NodeMeta
	if len(bytes.TrimSpace(metaBytes)) > 0 {
		meta, err = ParseMeta(ctx, metaBytes)
		if err != nil {
			return timelineSnapshotState{}, err
		}
	}

	title := ""
	links := []NodeId(nil)
	if stats != nil {
		title = stats.Title()
		links = stats.Links()
	}
	if title == "" && content != nil {
		title = content.Title
	}
	if len(links) == 0 && content != nil && len(content.Links) > 0 {
		links = content.Links
	}

	schema := ""
	if meta != nil {
		schema, _ = meta.Get("type")
	}
	if schema == "" && content != nil {
		schema = frontmatterString(content.Frontmatter, "type")
	}

	return timelineSnapshotState{
		snapshot: snap,
		schema:   schema,
		title:    title,
		links:    normalizeNodeIDList(links),
	}, nil
}

func (k *LocalKeg) buildDirtyIndexData(ctx context.Context) ([]byte, error) {
	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list nodes for %s index: %w", DirtyIndexName, err)
	}
	slices.SortFunc(ids, func(a, b NodeId) int { return a.Compare(b) })

	snapshots, supportsSnapshots := repoSnapshots(k.Repo)
	rows := make([]dirtyIndexRow, 0)
	for _, id := range ids {
		exists, err := k.nodeExistsWithContent(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("check node %s for %s index: %w", id.Path(), DirtyIndexName, err)
		}
		if !exists {
			continue
		}

		contentBytes, err := k.Repo.ReadContent(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("read node %s content for %s index: %w", id.Path(), DirtyIndexName, err)
		}
		content, err := ParseContent(k.Runtime, contentBytes, FormatMarkdown)
		if err != nil {
			return nil, fmt.Errorf("parse node %s content for %s index: %w", id.Path(), DirtyIndexName, err)
		}

		var latest Snapshot
		hasSnapshot := false
		if supportsSnapshots {
			nodeSnapshots, err := snapshots.ListSnapshots(ctx, id)
			if err != nil {
				if !errors.Is(err, ErrNotExist) {
					return nil, fmt.Errorf("list snapshots for node %s: %w", id.Path(), err)
				}
			}
			if len(nodeSnapshots) > 0 {
				latest = nodeSnapshots[len(nodeSnapshots)-1]
				hasSnapshot = true
			}
		}

		currentHash := content.Hash
		if hasSnapshot && latest.ContentHash == currentHash {
			continue
		}

		title := content.Title
		if title == "" {
			if stats, err := k.Repo.ReadStats(ctx, id); err == nil && stats != nil {
				title = stats.Title()
			}
		}

		rows = append(rows, dirtyIndexRow{
			V:                1,
			Node:             id.Path(),
			CurrentHash:      currentHash,
			SnapshotRevision: int64(latest.ID),
			SnapshotHash:     latest.ContentHash,
			Title:            title,
		})
	}

	return jsonLines(rows)
}

func compareTimelineSnapshotState(a, b timelineSnapshotState) int {
	if !a.snapshot.CreatedAt.Equal(b.snapshot.CreatedAt) {
		if a.snapshot.CreatedAt.Before(b.snapshot.CreatedAt) {
			return -1
		}
		return 1
	}
	if cmp := a.snapshot.Node.Compare(b.snapshot.Node); cmp != 0 {
		return cmp
	}
	if a.snapshot.ID < b.snapshot.ID {
		return -1
	}
	if a.snapshot.ID > b.snapshot.ID {
		return 1
	}
	return 0
}

func backlinksAtSnapshot(latestLinks map[string][]NodeId, target NodeId) []NodeId {
	out := make([]NodeId, 0)
	for srcPath, links := range latestLinks {
		src, err := ParseNode(srcPath)
		if err != nil || src == nil {
			continue
		}
		for _, dst := range links {
			if dst.Equals(target) {
				out = append(out, *src)
				break
			}
		}
	}
	return normalizeNodeIDList(out)
}

func timelineRefs(nodes []NodeId) []timelineNodeRef {
	nodes = normalizeNodeIDList(nodes)
	out := make([]timelineNodeRef, 0, len(nodes))
	for _, node := range nodes {
		out = append(out, timelineNodeRef{Node: node.Path()})
	}
	return out
}

func frontmatterString(frontmatter map[string]any, key string) string {
	if len(frontmatter) == 0 {
		return ""
	}
	switch v := frontmatter[key].(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return ""
	}
}

func jsonLines[T any](rows []T) ([]byte, error) {
	if len(rows) == 0 {
		return []byte{}, nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func formatIndexTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func isSnapshotGeneratedIndex(name string) bool {
	return name == TimelineIndexName || name == DirtyIndexName
}

func hasIndexName(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
