package keg

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type timelineOmegaUpdate struct {
	Node  string   `json:"node"`
	Omega *float64 `json:"omega"`
}

type snapshotOmegaUpdate struct {
	ID    NodeId
	Omega *float64
}

type snapshotTimelineStep struct {
	Record  timelineSnapshotState
	Updates []snapshotOmegaUpdate
}

type snapshotTimelineReplay struct {
	Steps []snapshotTimelineStep
	Final map[string]*float64
}

type snapshotNodeState struct {
	schema string
	meta   *NodeMeta
	links  []NodeId
}

type snapshotOmegaReplayer struct {
	schemas     map[string]*SchemaDefinition
	latest      map[string]snapshotNodeState
	latestLinks map[string][]NodeId
}

func (k *LocalKeg) replaySnapshotTimeline(ctx context.Context, records []timelineSnapshotState) (snapshotTimelineReplay, error) {
	schemas, err := k.loadOmegaSchemas(ctx)
	if err != nil {
		return snapshotTimelineReplay{}, err
	}

	replayer := snapshotOmegaReplayer{
		schemas:     schemas,
		latest:      make(map[string]snapshotNodeState, len(records)),
		latestLinks: make(map[string][]NodeId, len(records)),
	}
	out := snapshotTimelineReplay{
		Steps: make([]snapshotTimelineStep, 0, len(records)),
		Final: make(map[string]*float64),
	}
	for _, rec := range records {
		step := replayer.apply(rec)
		for _, update := range step.Updates {
			out.Final[update.ID.Path()] = cloneOmega(update.Omega)
		}
		out.Steps = append(out.Steps, step)
	}
	return out, nil
}

func (k *LocalKeg) loadOmegaSchemas(ctx context.Context) (map[string]*SchemaDefinition, error) {
	store, ok := repoSchemas(k.Repo)
	if !ok {
		return nil, nil
	}
	names, err := store.ListSchemas(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*SchemaDefinition, len(names))
	for _, name := range names {
		raw, err := store.ReadSchema(ctx, name)
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				continue
			}
			return nil, err
		}
		parsed, err := ParseSchemaDefinition(raw)
		if err != nil {
			return nil, fmt.Errorf("parse schema %s for omega replay: %w", name, err)
		}
		typeName := strings.TrimSpace(parsed.Type)
		if typeName == "" {
			typeName = strings.TrimSpace(name)
		}
		if typeName != "" {
			out[typeName] = parsed
		}
	}
	return out, nil
}

func (r *snapshotOmegaReplayer) apply(rec timelineSnapshotState) snapshotTimelineStep {
	nodeKey := rec.snapshot.Node.Path()
	state := snapshotNodeState{
		schema: strings.TrimSpace(rec.schema),
		meta:   rec.meta,
		links:  normalizeNodeIDList(rec.links),
	}
	r.latest[nodeKey] = state
	r.latestLinks[nodeKey] = state.links

	candidates := omegaReplayCandidates(r.latestLinks, rec.snapshot.Node)
	updates := make([]snapshotOmegaUpdate, 0, len(candidates))
	for _, id := range candidates {
		if _, ok := r.latest[id.Path()]; !ok {
			continue
		}
		omega, ok := r.compute(id)
		update := snapshotOmegaUpdate{ID: id}
		if ok {
			update.Omega = float64Ptr(omega)
		}
		updates = append(updates, update)
	}
	return snapshotTimelineStep{Record: rec, Updates: updates}
}

func omegaReplayCandidates(latestLinks map[string][]NodeId, event NodeId) []NodeId {
	candidates := []NodeId{event}
	candidates = append(candidates, latestLinks[event.Path()]...)
	candidates = append(candidates, backlinksAtSnapshot(latestLinks, event)...)
	return normalizeNodeIDList(candidates)
}

func (r *snapshotOmegaReplayer) compute(id NodeId) (float64, bool) {
	state, ok := r.latest[id.Path()]
	if !ok {
		return 0, false
	}
	typeName := r.nodeType(state)
	if typeName == "" {
		return 0, false
	}
	schema := r.schemas[typeName]
	if schema == nil {
		return 0, false
	}

	var weightedSum float64
	var totalWeight float64
	for _, maturity := range schema.Maturity {
		attribute := strings.TrimSpace(maturity.Attribute)
		if attribute == "" || maturity.Weight <= 0 {
			continue
		}
		score := r.metadataScore(state.meta, maturity)
		weightedSum += score * maturity.Weight
		totalWeight += maturity.Weight
	}
	for _, rel := range schema.Relations {
		for _, maturity := range rel.Maturity {
			attribute := strings.TrimSpace(maturity.Attribute)
			if attribute == "" || maturity.Weight <= 0 {
				continue
			}
			score := r.relationScore(id, rel, maturity)
			weightedSum += score * maturity.Weight
			totalWeight += maturity.Weight
		}
	}
	if totalWeight <= 0 {
		return 0, false
	}
	return clampOmegaScore(weightedSum / totalWeight), true
}

func (r *snapshotOmegaReplayer) metadataScore(meta *NodeMeta, maturity MetadataMaturitySchema) float64 {
	if meta == nil {
		return 0
	}
	rawValue, ok := meta.Get(strings.TrimSpace(maturity.Attribute))
	if !ok {
		return 0
	}
	score, ok := omegaAttributeScore(maturity.Enum, rawValue)
	if !ok {
		return 0
	}
	return score
}

func (r *snapshotOmegaReplayer) relationScore(id NodeId, rel RelationSchema, maturity MaturityWeightSchema) float64 {
	targets := r.relationTargets(id, maturity)
	if len(targets) == 0 {
		return 0
	}

	attribute := strings.TrimSpace(maturity.Attribute)
	targetType := strings.TrimSpace(rel.Type)
	best := 0.0
	for _, target := range targets {
		targetState, ok := r.latest[target.Path()]
		if !ok {
			continue
		}
		if targetType != "" && r.nodeType(targetState) != targetType {
			continue
		}
		if targetState.meta == nil {
			continue
		}
		rawValue, ok := targetState.meta.Get(attribute)
		if !ok {
			continue
		}
		score, ok := omegaAttributeScore(maturity.Enum, rawValue)
		if ok && score > best {
			best = score
		}
	}
	return best
}

func (r *snapshotOmegaReplayer) relationTargets(id NodeId, maturity MaturityWeightSchema) []NodeId {
	direction, ok := normalizeRelationDirection(maturity.Direction)
	if !ok {
		return nil
	}
	switch direction {
	case RelationDirectionBacklinks:
		return backlinksAtSnapshot(r.latestLinks, id)
	default:
		return normalizeNodeIDList(r.latestLinks[id.Path()])
	}
}

func (r *snapshotOmegaReplayer) nodeType(state snapshotNodeState) string {
	if state.schema != "" {
		return state.schema
	}
	if state.meta == nil {
		return ""
	}
	typeName, ok := state.meta.Get("type")
	if !ok {
		return ""
	}
	return strings.TrimSpace(typeName)
}

func omegaAttributeScore(enum map[string]float64, rawValue string) (float64, bool) {
	value := strings.TrimSpace(rawValue)
	if len(enum) > 0 {
		score, ok := enum[value]
		if !ok {
			return 0, false
		}
		return clampOmegaScore(score), true
	}
	score, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return clampOmegaScore(score), true
}

func (k *LocalKeg) computePendingSnapshotOmega(ctx context.Context, pending timelineSnapshotState) (*float64, error) {
	records, err := k.loadTimelineSnapshotStates(ctx)
	if err != nil {
		return nil, err
	}
	records = append(records, pending)
	sortTimelineSnapshotStates(records)

	replay, err := k.replaySnapshotTimeline(ctx, records)
	if err != nil {
		return nil, err
	}
	for _, step := range replay.Steps {
		if !step.Record.snapshot.Node.Equals(pending.snapshot.Node) || step.Record.snapshot.ID != pending.snapshot.ID {
			continue
		}
		return omegaUpdateForNode(step.Updates, pending.snapshot.Node), nil
	}
	return nil, nil
}

func (k *LocalKeg) persistSnapshotOmegaStats(ctx context.Context, replay snapshotTimelineReplay) error {
	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return err
	}
	for _, id := range normalizeNodeIDList(ids) {
		desired := replay.Final[id.Path()]
		stats, err := k.getStats(ctx, id)
		if err != nil {
			return err
		}
		if stats == nil {
			stats = &NodeStats{}
		}
		if !setStatsOmega(stats, desired) {
			continue
		}
		if err := k.Repo.WriteStats(ctx, id, stats); err != nil {
			return fmt.Errorf("write replayed omega for node %s: %w", id.Path(), err)
		}
	}
	return nil
}

func timelineOmegaUpdates(updates []snapshotOmegaUpdate) []timelineOmegaUpdate {
	out := make([]timelineOmegaUpdate, 0, len(updates))
	for _, update := range updates {
		out = append(out, timelineOmegaUpdate{
			Node:  update.ID.Path(),
			Omega: cloneOmega(update.Omega),
		})
	}
	return out
}

func omegaUpdateForNode(updates []snapshotOmegaUpdate, id NodeId) *float64 {
	for _, update := range updates {
		if update.ID.Equals(id) {
			return cloneOmega(update.Omega)
		}
	}
	return nil
}

func setStatsOmega(stats *NodeStats, desired *float64) bool {
	if desired == nil {
		if _, ok := stats.Omega(); !ok {
			return false
		}
		stats.ClearOmega()
		return true
	}
	current, ok := stats.Omega()
	if ok && current == *desired {
		return false
	}
	stats.SetOmega(*desired)
	return true
}

func cloneOmega(value *float64) *float64 {
	if value == nil {
		return nil
	}
	return float64Ptr(*value)
}

func float64Ptr(value float64) *float64 {
	out := value
	return &out
}

func clampOmegaScore(score float64) float64 {
	switch {
	case score < 0:
		return 0
	case score > 1:
		return 1
	default:
		return score
	}
}
