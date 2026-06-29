package keg

import (
	"context"
	"errors"
	"strconv"
	"strings"
)

func (k *LocalKeg) applyComputedOmega(ctx context.Context, id NodeId, stats *NodeStats) error {
	if stats == nil {
		return nil
	}
	stats.ClearOmega()
	omega, ok, err := k.computeOmega(ctx, id)
	if err != nil {
		return err
	}
	if ok {
		stats.SetOmega(omega)
	}
	return nil
}

func (k *LocalKeg) computeOmega(ctx context.Context, id NodeId) (float64, bool, error) {
	schema, ok, err := k.schemaForOmega(ctx, id)
	if err != nil || !ok {
		return 0, false, err
	}

	var weightedSum float64
	var totalWeight float64
	for _, rel := range schema.Relations {
		attribute := strings.TrimSpace(rel.Attribute)
		if attribute == "" || rel.Weight <= 0 {
			continue
		}
		score, err := k.omegaRelationScore(ctx, id, rel)
		if err != nil {
			return 0, false, err
		}
		weightedSum += score * rel.Weight
		totalWeight += rel.Weight
	}
	if totalWeight <= 0 {
		return 0, false, nil
	}
	return clampOmegaScore(weightedSum / totalWeight), true, nil
}

func (k *LocalKeg) schemaForOmega(ctx context.Context, id NodeId) (*SchemaDefinition, bool, error) {
	store, ok := repoSchemas(k.Repo)
	if !ok {
		return nil, false, nil
	}
	meta, err := k.getMeta(ctx, id)
	if err != nil {
		return nil, false, err
	}
	typeName, ok := meta.Get("type")
	typeName = strings.TrimSpace(typeName)
	if !ok || typeName == "" {
		return nil, false, nil
	}
	raw, err := store.ReadSchema(ctx, typeName)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	schema, err := ParseSchemaDefinition(raw)
	if err != nil {
		return nil, false, err
	}
	return schema, true, nil
}

func (k *LocalKeg) omegaRelationScore(ctx context.Context, id NodeId, rel RelationSchema) (float64, error) {
	targets, err := k.omegaRelationTargets(ctx, id, rel)
	if err != nil {
		return 0, err
	}
	if len(targets) == 0 {
		return 0, nil
	}

	attribute := strings.TrimSpace(rel.Attribute)
	targetType := strings.TrimSpace(rel.Type)
	best := 0.0
	for _, target := range targets {
		meta, err := k.getMeta(ctx, target)
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				continue
			}
			return 0, err
		}
		if targetType != "" {
			gotType, ok := meta.Get("type")
			if !ok || strings.TrimSpace(gotType) != targetType {
				continue
			}
		}
		rawValue, ok := meta.Get(attribute)
		if !ok {
			continue
		}
		score, ok := omegaAttributeScore(rel, rawValue)
		if ok && score > best {
			best = score
		}
	}
	return best, nil
}

func (k *LocalKeg) omegaRelationTargets(ctx context.Context, id NodeId, rel RelationSchema) ([]NodeId, error) {
	dex, err := k.Dex(ctx)
	if err != nil {
		return nil, err
	}
	direction, ok := normalizeRelationDirection(rel.Direction)
	if !ok {
		return nil, nil
	}
	var targets []NodeId
	switch direction {
	case RelationDirectionBacklinks:
		targets, _ = dex.Backlinks(ctx, id)
	default:
		targets, _ = dex.Links(ctx, id)
	}
	return normalizeNodeIDList(targets), nil
}

func omegaAttributeScore(rel RelationSchema, rawValue string) (float64, bool) {
	value := strings.TrimSpace(rawValue)
	if len(rel.Enum) > 0 {
		score, ok := rel.Enum[value]
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
