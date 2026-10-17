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
	V            int                   `json:"v"`
	OccurredAt   string                `json:"occurred_at"`
	Node         string                `json:"node"`
	Revision     int64                 `json:"revision"`
	Parent       int64                 `json:"parent"`
	Schema       string                `json:"schema"`
	Title        string                `json:"title"`
	Message      string                `json:"message"`
	ContentHash  string                `json:"content_hash"`
	Links        []timelineNodeRef     `json:"links"`
	Backlinks    []timelineNodeRef     `json:"backlinks"`
	Omega        *float64              `json:"omega"`
	OmegaUpdates []timelineOmegaUpdate `json:"omega_updates"`
}

type dirtyIndexRow struct {
	V                int    `json:"v"`
	Node             string `json:"node"`
	CurrentHash      string `json:"current_hash"`
	CurrentMetaHash  string `json:"current_meta_hash,omitempty"`
	SnapshotRevision int64  `json:"snapshot_revision"`
	SnapshotHash     string `json:"snapshot_hash"`
	SnapshotMetaHash string `json:"snapshot_meta_hash,omitempty"`
	Title            string `json:"title"`
}

type timelineSnapshotState struct {
	snapshot Snapshot
	schema   string
	title    string
	links    []NodeId
	meta     *NodeMeta
	stats    *NodeStats
}

// snapshotTimeline is the keg's snapshot history and omega schemas, loaded
// once for a batch of appends instead of once per append. It loads on first
// use, and each append adds the record of the snapshot it wrote, so it stays
// what a fresh load would return. It is valid only within one keg write
// operation, where nothing else appends snapshots or changes schemas.
type snapshotTimeline struct {
	loaded  bool
	records []timelineSnapshotState
	schemas map[string]*SchemaDefinition
}

func (t *snapshotTimeline) load(ctx context.Context, k *LocalKeg) error {
	if t.loaded {
		return nil
	}
	records, err := k.loadTimelineSnapshotStates(ctx)
	if err != nil {
		return err
	}
	schemas, err := k.loadOmegaSchemas(ctx)
	if err != nil {
		return err
	}
	t.records, t.schemas, t.loaded = records, schemas, true
	return nil
}

// add records snapshot snap of node id, which the caller just appended.
func (t *snapshotTimeline) add(ctx context.Context, k *LocalKeg, id NodeId, snap Snapshot) error {
	if !t.loaded {
		return nil
	}
	snapshots, ok := repoSnapshots(k.Repo)
	if !ok {
		return nil
	}
	state, err := k.loadTimelineSnapshotState(ctx, snapshots, id, snap.ID)
	if err != nil {
		return err
	}
	t.records = append(t.records, state)
	sortTimelineSnapshotStates(t.records)
	return nil
}

func (k *LocalKeg) refreshSnapshotGeneratedIndexes(ctx context.Context) error {
	return k.refreshSnapshotGeneratedIndexesFrom(ctx, nil)
}

// refreshSnapshotGeneratedIndexesFrom is refreshSnapshotGeneratedIndexes over
// a batch's timeline; a nil timeline loads a fresh one.
func (k *LocalKeg) refreshSnapshotGeneratedIndexesFrom(ctx context.Context, history *snapshotTimeline) error {
	timeline, replay, err := k.buildTimelineIndexDataFrom(ctx, history)
	if err != nil {
		return err
	}
	if err := k.Repo.WriteIndex(ctx, TimelineIndexName, timeline); err != nil {
		return fmt.Errorf("write %s index: %w", TimelineIndexName, err)
	}
	if err := k.persistSnapshotOmegaStats(ctx, replay); err != nil {
		return fmt.Errorf("persist replayed omega stats: %w", err)
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
	timeline, _, err := k.buildTimelineIndexDataWithReplay(ctx)
	return timeline, err
}

func (k *LocalKeg) buildTimelineIndexDataWithReplay(ctx context.Context) ([]byte, snapshotTimelineReplay, error) {
	return k.buildTimelineIndexDataFrom(ctx, nil)
}

func (k *LocalKeg) buildTimelineIndexDataFrom(ctx context.Context, history *snapshotTimeline) ([]byte, snapshotTimelineReplay, error) {
	if history == nil {
		history = &snapshotTimeline{}
	}
	if err := history.load(ctx, k); err != nil {
		return nil, snapshotTimelineReplay{}, err
	}
	records := history.records
	replay := replaySnapshotTimelineWith(history.schemas, records)

	rows := make([]timelineIndexRow, 0, len(replay.Steps))
	latestLinks := make(map[string][]NodeId, len(records))
	for _, step := range replay.Steps {
		rec := step.Record
		nodeKey := rec.snapshot.Node.Path()
		latestLinks[nodeKey] = normalizeNodeIDList(rec.links)
		rows = append(rows, timelineIndexRow{
			V:            1,
			OccurredAt:   formatIndexTime(rec.snapshot.CreatedAt),
			Node:         nodeKey,
			Revision:     int64(rec.snapshot.ID),
			Parent:       int64(rec.snapshot.Parent),
			Schema:       rec.schema,
			Title:        rec.title,
			Message:      rec.snapshot.Message,
			ContentHash:  rec.snapshot.ContentHash,
			Links:        timelineRefs(rec.links),
			Backlinks:    timelineRefs(backlinksAtSnapshot(latestLinks, rec.snapshot.Node)),
			Omega:        omegaUpdateForNode(step.Updates, rec.snapshot.Node),
			OmegaUpdates: timelineOmegaUpdates(step.Updates),
		})
	}

	timeline, err := jsonLines(rows)
	if err != nil {
		return nil, snapshotTimelineReplay{}, err
	}
	return timeline, replay, nil
}

func (k *LocalKeg) loadTimelineSnapshotStates(ctx context.Context) ([]timelineSnapshotState, error) {
	snapshots, ok := repoSnapshots(k.Repo)
	if !ok {
		return nil, nil
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
			state, err := k.loadTimelineSnapshotState(ctx, snapshots, id, snap.ID)
			if err != nil {
				return nil, err
			}
			records = append(records, state)
		}
	}

	sortTimelineSnapshotStates(records)
	return records, nil
}

func (k *LocalKeg) loadTimelineSnapshotState(ctx context.Context, snapshots RepositorySnapshots, id NodeId, rev RevisionID) (timelineSnapshotState, error) {
	loaded, content, meta, stats, err := snapshots.GetSnapshot(ctx, id, rev, SnapshotReadOptions{ResolveContent: true})
	if err != nil && errors.Is(err, ErrConflict) {
		if unchecked, ok := k.Repo.(interface {
			readContentAtUnchecked(context.Context, NodeId, RevisionID) ([]byte, error)
		}); ok {
			loaded, _, meta, stats, err = snapshots.GetSnapshot(ctx, id, rev, SnapshotReadOptions{})
			if err == nil {
				content, err = unchecked.readContentAtUnchecked(ctx, id, rev)
			}
		}
	}
	if err != nil {
		return timelineSnapshotState{}, fmt.Errorf("read snapshot node %s rev %d: %w", id.Path(), rev, err)
	}
	loaded.Node = id
	state, err := timelineSnapshotStateFromPayload(ctx, k.Runtime, loaded, content, meta, stats)
	if err != nil {
		return timelineSnapshotState{}, fmt.Errorf("parse snapshot node %s rev %d: %w", id.Path(), rev, err)
	}
	return state, nil
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
		meta:     meta,
		stats:    stats,
	}, nil
}

func (k *LocalKeg) buildDirtyIndexData(ctx context.Context) ([]byte, error) {
	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("list nodes for %s index: %w", DirtyIndexName, err)
	}
	slices.SortFunc(ids, func(a, b NodeId) int { return a.Compare(b) })

	// Read each node's content once; a node without content has no row.
	type dirtyCandidate struct {
		id     NodeId
		hash   string
		title  string
		latest Snapshot
		hasRev bool
	}
	snapshots, supportsSnapshots := repoSnapshots(k.Repo)
	candidates := make([]dirtyCandidate, 0, len(ids))
	for _, id := range ids {
		contentBytes, err := k.Repo.ReadContent(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read node %s content for %s index: %w", id.Path(), DirtyIndexName, err)
		}
		content, err := ParseContent(k.Runtime, contentBytes, FormatMarkdown)
		if err != nil {
			return nil, fmt.Errorf("parse node %s content for %s index: %w", id.Path(), DirtyIndexName, err)
		}
		candidate := dirtyCandidate{id: id, hash: content.Hash, title: content.Title}
		if supportsSnapshots {
			nodeSnapshots, err := snapshots.ListSnapshots(ctx, id)
			if err != nil {
				if !errors.Is(err, ErrNotExist) {
					return nil, fmt.Errorf("list snapshots for node %s: %w", id.Path(), err)
				}
			}
			if len(nodeSnapshots) > 0 {
				candidate.latest = nodeSnapshots[len(nodeSnapshots)-1]
				candidate.hasRev = true
			}
		}
		candidates = append(candidates, candidate)
	}

	present := make([]NodeId, 0, len(candidates))
	for _, candidate := range candidates {
		present = append(present, candidate.id)
	}
	metas, err := k.readDirtyIndexMeta(ctx, present)
	if err != nil {
		return nil, err
	}

	rows := make([]dirtyIndexRow, 0)
	var untitled []NodeId
	for _, candidate := range candidates {
		currentMetaHash := hashSnapshotBytes(k.Runtime, metas[candidate.id.Path()])
		if candidate.hasRev && candidate.latest.ContentHash == candidate.hash && candidate.latest.MetaHash == currentMetaHash {
			continue
		}
		if candidate.title == "" {
			untitled = append(untitled, candidate.id)
		}
		rows = append(rows, dirtyIndexRow{
			V:                1,
			Node:             candidate.id.Path(),
			CurrentHash:      candidate.hash,
			CurrentMetaHash:  currentMetaHash,
			SnapshotRevision: int64(candidate.latest.ID),
			SnapshotHash:     candidate.latest.ContentHash,
			SnapshotMetaHash: candidate.latest.MetaHash,
			Title:            candidate.title,
		})
	}

	// A row whose content has no title falls back to the stats title.
	if len(untitled) > 0 {
		titles := k.readDirtyIndexStatsTitles(ctx, untitled)
		for i := range rows {
			if rows[i].Title == "" {
				rows[i].Title = titles[rows[i].Node]
			}
		}
	}

	return jsonLines(rows)
}

// readDirtyIndexMeta returns raw metadata keyed by node id path, leaving out
// nodes that have none. It reads the whole set at once when the repository
// supports batch reads.
func (k *LocalKeg) readDirtyIndexMeta(ctx context.Context, ids []NodeId) (map[string][]byte, error) {
	if batch, ok := repositoryBatchRead(k.Repo); ok {
		metas, err := batch.ReadMetaBatch(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("read node meta for %s index: %w", DirtyIndexName, err)
		}
		return metas, nil
	}
	metas := make(map[string][]byte, len(ids))
	for _, id := range ids {
		raw, err := k.Repo.ReadMeta(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read node %s meta for %s index: %w", id.Path(), DirtyIndexName, err)
		}
		metas[id.Path()] = raw
	}
	return metas, nil
}

// readDirtyIndexStatsTitles returns the stats title keyed by node id path. A
// node whose stats cannot be read has no title, as it would on a single read.
func (k *LocalKeg) readDirtyIndexStatsTitles(ctx context.Context, ids []NodeId) map[string]string {
	titles := make(map[string]string, len(ids))
	if batch, ok := repositoryBatchRead(k.Repo); ok {
		stats, err := batch.ReadStatsBatch(ctx, ids)
		if err == nil {
			for key, s := range stats {
				titles[key] = s.Title()
			}
			return titles
		}
	}
	for _, id := range ids {
		if stats, err := k.Repo.ReadStats(ctx, id); err == nil && stats != nil {
			titles[id.Path()] = stats.Title()
		}
	}
	return titles
}

func sortTimelineSnapshotStates(records []timelineSnapshotState) {
	slices.SortFunc(records, compareTimelineSnapshotState)
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
