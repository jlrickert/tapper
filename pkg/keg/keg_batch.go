package keg

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var nodePlaceholderPattern = regexp.MustCompile(`\{\{node:([^{}]+)\}\}`)

type BatchMutationError struct {
	Index  int
	Key    string
	NodeID NodeId
	Err    error
}

func (e *BatchMutationError) Error() string {
	if e == nil {
		return "batch mutation failed"
	}
	where := fmt.Sprintf("batch item %d", e.Index)
	if e.Key != "" {
		where += fmt.Sprintf(" (key %q)", e.Key)
	}
	if e.NodeID.Valid() {
		where += fmt.Sprintf(" (node %s)", e.NodeID.Path())
	}
	return where + ": " + e.Err.Error()
}
func (e *BatchMutationError) Unwrap() error { return e.Err }

func validateMutationBatchSize(n int) error {
	if n == 0 {
		return fmt.Errorf("batch must contain at least one item: %w", ErrInvalid)
	}
	if n > MaxMutationBatchSize {
		return fmt.Errorf("batch contains %d items; maximum is %d: %w", n, MaxMutationBatchSize, ErrInvalid)
	}
	return nil
}

func (k *LocalKeg) CreateNodes(ctx context.Context, nodes []NodeCreate) ([]CreateNodeResult, error) {
	return withKegAtomicWriteValue(ctx, k, func(ctx context.Context) ([]CreateNodeResult, error) { return k.createNodes(ctx, nodes) })
}

func (k *LocalKeg) createNodes(ctx context.Context, nodes []NodeCreate) ([]CreateNodeResult, error) {
	if err := validateMutationBatchSize(len(nodes)); err != nil {
		return nil, err
	}
	if err := k.checkKegExists(ctx); err != nil {
		return nil, err
	}
	seen := make(map[string]int, len(nodes))
	for i, item := range nodes {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			return nil, &BatchMutationError{Index: i, Err: fmt.Errorf("creation key is required: %w", ErrInvalid)}
		}
		if first, ok := seen[key]; ok {
			return nil, &BatchMutationError{Index: i, Key: key, Err: fmt.Errorf("duplicate creation key (first at index %d): %w", first, ErrInvalid)}
		}
		seen[key] = i
	}
	ids := make([]NodeId, len(nodes))
	byKey := make(map[string]NodeId, len(nodes))
	for i, item := range nodes {
		id, err := k.Repo.Next(ctx)
		if err != nil {
			return nil, &BatchMutationError{Index: i, Key: item.Key, Err: err}
		}
		ids[i], byKey[strings.TrimSpace(item.Key)] = id, id
	}
	now := k.Runtime.Clock().Now()
	proposed := make([]*NodeData, len(nodes))
	results := make([]CreateNodeResult, len(nodes))
	for i, item := range nodes {
		body, err := expandNodePlaceholders(item.Body, byKey)
		if err != nil {
			return nil, &BatchMutationError{Index: i, Key: item.Key, NodeID: ids[i], Err: err}
		}
		data, err := k.buildNodeData(ctx, &CreateOptions{Schema: item.Schema, Title: item.Title, Lead: item.Lead, Body: body, Tags: item.Tags, Attrs: item.Attrs}, now, fmt.Sprintf("NodeId %s", ids[i].Path()))
		if err != nil {
			return nil, &BatchMutationError{Index: i, Key: item.Key, NodeID: ids[i], Err: err}
		}
		data.ID = ids[i]
		validation, err := k.validateNodeWrite(ctx, schemaWriteCreate, ids[i], data, item.Schema,
			schemaTypeCandidateFromAttrs("attributes", item.Attrs),
			schemaTypeCandidateFromFrontmatter("frontmatter", data.Content))
		if err != nil {
			return nil, &BatchMutationError{Index: i, Key: item.Key, NodeID: ids[i], Err: err}
		}
		if validation != nil && validation.Valid {
			validation = nil
		}
		proposed[i] = data
		results[i] = CreateNodeResult{Key: item.Key, ID: ids[i], Hash: data.Stats.Hash(), Validation: validation}
	}
	for i, data := range proposed {
		err := k.withNodeLock(ctx, data.ID, func(lockCtx context.Context) error {
			if err := k.Repo.WriteContent(lockCtx, data.ID, []byte(data.Content.Body)); err != nil {
				return err
			}
			if err := k.Repo.WriteMeta(lockCtx, data.ID, []byte(data.Meta.ToYAML())); err != nil {
				return err
			}
			return k.Repo.WriteStats(lockCtx, data.ID, data.Stats)
		})
		if err != nil {
			for _, id := range ids {
				_ = k.Repo.DeleteNode(ctx, id)
			}
			k.InvalidateDex()
			_ = k.rebuildDexFromRepo(ctx)
			return nil, &BatchMutationError{Index: i, Key: nodes[i].Key, NodeID: data.ID, Err: err}
		}
	}
	if err := k.writeNodesToDex(ctx, proposed, now); err != nil {
		return nil, err
	}
	if err := k.refreshDirtyIndex(ctx); err != nil {
		return nil, err
	}
	return results, nil
}

func expandNodePlaceholders(body []byte, ids map[string]NodeId) ([]byte, error) {
	var unknown string
	out := nodePlaceholderPattern.ReplaceAllStringFunc(string(body), func(match string) string {
		parts := nodePlaceholderPattern.FindStringSubmatch(match)
		key := strings.TrimSpace(parts[1])
		id, ok := ids[key]
		if !ok {
			unknown = key
			return match
		}
		return id.Path()
	})
	if unknown != "" {
		return nil, fmt.Errorf("unknown node placeholder %q: %w", unknown, ErrInvalid)
	}
	return []byte(out), nil
}

type preparedNodeUpdate struct {
	opts       NodeUpdateOptions
	data       *NodeData
	content    []byte
	validation *SchemaValidationResult
	backup     *aggregateBackup
}

func (k *LocalKeg) UpdateNodes(ctx context.Context, updates []NodeUpdateOptions) ([]NodeUpdateResult, error) {
	return withKegAtomicWriteValue(ctx, k, func(ctx context.Context) ([]NodeUpdateResult, error) { return k.updateNodes(ctx, updates) })
}

func (k *LocalKeg) updateNodes(ctx context.Context, updates []NodeUpdateOptions) ([]NodeUpdateResult, error) {
	if err := validateMutationBatchSize(len(updates)); err != nil {
		return nil, err
	}
	seen := map[NodeId]int{}
	prepared := make([]preparedNodeUpdate, len(updates))
	for i, opts := range updates {
		if !opts.ID.Valid() {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: fmt.Errorf("node id is required: %w", ErrInvalid)}
		}
		if !opts.HasContent && !opts.HasMeta {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: fmt.Errorf("content or metadata replacement is required: %w", ErrInvalid)}
		}
		if first, ok := seen[opts.ID]; ok {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: fmt.Errorf("duplicate node id (first at index %d): %w", first, ErrInvalid)}
		}
		seen[opts.ID] = i
		existing, err := k.ReadNode(ctx, opts.ID)
		if err != nil {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: err}
		}
		backup, err := k.captureAggregateBackup(ctx, existing)
		if err != nil {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: err}
		}
		if err := k.validateAggregateLock(ctx, opts.ID, opts.LockToken); err != nil {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: err}
		}
		currentHash := existing.Hash()
		if err := checkExpectedHash("node "+opts.ID.Path(), opts.ExpectedHash, currentHash, nodeRecoveryContent(existing)); err != nil {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: err}
		}
		contentBytes := existing.Content
		if opts.HasContent {
			contentBytes = opts.Content
		}
		metaBytes := existing.Meta
		if opts.HasMeta {
			metaBytes = opts.Meta
		}
		content, err := ParseContent(k.Runtime, contentBytes, MarkdownContentFilename)
		if err != nil {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: fmt.Errorf("invalid content: %w", err)}
		}
		meta, err := ParseMeta(ctx, metaBytes)
		if err != nil {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: fmt.Errorf("invalid metadata: %w", err)}
		}
		stats, err := cloneNodeStats(ctx, existing.Stats)
		if err != nil {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: err}
		}
		if stats == nil {
			stats = &NodeStats{}
		}
		now := k.Runtime.Clock().Now()
		data := &NodeData{ID: opts.ID, Content: content, Meta: meta, Stats: stats}
		if err := data.updateMeta(ctx, k.Runtime, &now); err != nil {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: err}
		}
		candidates := make([]schemaTypeCandidate, 0, 2)
		if opts.HasContent {
			candidates = append(candidates, schemaTypeCandidateFromFrontmatter("frontmatter", content))
		}
		if opts.HasMeta {
			candidates = append(candidates, schemaTypeCandidateFromMeta("metadata", meta))
		}
		validation, err := k.validateNodeWrite(ctx, schemaWriteUpdate, opts.ID, data, opts.Schema, candidates...)
		if err != nil && !errors.Is(err, ErrNotSupported) {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: err}
		}
		if validation != nil && validation.Valid {
			validation = nil
		}
		prepared[i] = preparedNodeUpdate{opts: opts, data: data, content: contentBytes, validation: validation, backup: backup}
	}
	results := make([]NodeUpdateResult, len(prepared))
	createdSnapshots := false
	for i, item := range prepared {
		if item.opts.SnapshotBefore {
			if _, err := k.appendSnapshotNoRefresh(ctx, item.opts.ID, "before batch update"); err != nil {
				return nil, &BatchMutationError{Index: i, NodeID: item.opts.ID, Err: err}
			}
			createdSnapshots = true
		}
		err := k.withNodeLock(ctx, item.opts.ID, func(lockCtx context.Context) error {
			if item.opts.HasMeta || strings.TrimSpace(item.opts.Schema) != "" {
				if err := k.Repo.WriteMeta(lockCtx, item.opts.ID, []byte(item.data.Meta.ToYAML())); err != nil {
					return err
				}
			}
			if item.opts.HasContent {
				if err := k.Repo.WriteContent(lockCtx, item.opts.ID, item.content); err != nil {
					return err
				}
			}
			return k.Repo.WriteStats(lockCtx, item.opts.ID, item.data.Stats)
		})
		if err != nil {
			var rollback []error
			for _, preparedItem := range prepared {
				rollback = append(rollback, k.restoreAggregateBackup(ctx, preparedItem.backup))
			}
			return nil, &BatchMutationError{Index: i, NodeID: item.opts.ID, Err: errors.Join(err, errors.Join(rollback...))}
		}
		results[i] = NodeUpdateResult{ID: item.opts.ID, Validation: item.validation, Hash: item.data.Stats.Hash()}
	}
	updatedNodes := make([]*NodeData, len(prepared))
	for i := range prepared {
		updatedNodes[i] = prepared[i].data
	}
	if err := k.writeNodesToDex(ctx, updatedNodes, k.Runtime.Clock().Now()); err != nil {
		return nil, err
	}
	if err := k.refreshDirtyIndex(ctx); err != nil {
		return nil, err
	}
	if createdSnapshots {
		if err := k.refreshSnapshotGeneratedIndexes(ctx); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func (k *LocalKeg) AppendSnapshots(ctx context.Context, nodes []NodeSnapshotRequest) ([]Snapshot, error) {
	return withKegAtomicWriteValue(ctx, k, func(ctx context.Context) ([]Snapshot, error) {
		if err := validateMutationBatchSize(len(nodes)); err != nil {
			return nil, err
		}
		seen := map[NodeId]int{}
		for i, item := range nodes {
			if first, ok := seen[item.ID]; ok {
				return nil, &BatchMutationError{Index: i, NodeID: item.ID, Err: fmt.Errorf("duplicate node id (first at index %d): %w", first, ErrInvalid)}
			}
			seen[item.ID] = i
			if exists, err := k.nodeExistsWithContent(ctx, item.ID); err != nil || !exists {
				if err == nil {
					err = ErrNotExist
				}
				return nil, &BatchMutationError{Index: i, NodeID: item.ID, Err: err}
			}
		}
		out := make([]Snapshot, len(nodes))
		for i, item := range nodes {
			snap, err := k.appendSnapshotNoRefresh(ctx, item.ID, item.Message)
			if err != nil {
				return nil, &BatchMutationError{Index: i, NodeID: item.ID, Err: err}
			}
			out[i] = snap
		}
		if err := k.refreshSnapshotGeneratedIndexes(ctx); err != nil {
			return nil, err
		}
		return out, nil
	})
}
