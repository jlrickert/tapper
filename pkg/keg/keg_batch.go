package keg

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var nodePlaceholderPattern = regexp.MustCompile(`\{\{node:([^{}]+)\}\}`)

// A placeholder expands to a bare id, which is not a link: a node link is
// ../ID. These match a placeholder that is the whole target of an inline link
// or a reference definition, the one position where a bare id can only be a
// mistake, so expansion can supply the ../ the caller left out. An inline
// link may carry a title after the target. A footnote definition ([^label]:)
// is not a reference definition: its text is prose, so it is left alone.
var (
	inlineLinkPlaceholderPattern = regexp.MustCompile(`\]\([ \t]*(\{\{node:[^{}]+\}\})((?:[ \t]+(?:"[^"\n]*"|'[^'\n]*'|\([^)\n]*\)))?)[ \t]*\)`)
	refDefPlaceholderPattern     = regexp.MustCompile(`(?m)^( {0,3}\[[^\]^\n][^\]\n]*\]:[ \t]*)(\{\{node:[^{}]+\}\})([ \t]|$)`)
)

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
		data, err := k.buildNodeData(ctx, &CreateOptions{Schema: item.Schema, Body: body, Meta: item.Meta}, now, fmt.Sprintf("NodeId %s", ids[i].Path()))
		if err != nil {
			return nil, &BatchMutationError{Index: i, Key: item.Key, NodeID: ids[i], Err: err}
		}
		data.ID = ids[i]
		// Only meta can declare a type: buildNodeData rejects content that
		// opens with a frontmatter block, so there is no second source to
		// reconcile against the selected schema.
		validation, err := k.validateNodeWrite(ctx, schemaWriteCreate, ids[i], data, item.Schema,
			schemaTypeCandidateFromMeta("metadata", data.Meta))
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
	text := inlineLinkPlaceholderPattern.ReplaceAllString(string(body), "](../${1}${2})")
	text = refDefPlaceholderPattern.ReplaceAllString(text, "${1}../${2}${3}")
	var unknown string
	out := nodePlaceholderPattern.ReplaceAllStringFunc(text, func(match string) string {
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
	opts NodeUpdateOptions
	// existing is the node as it was read before the update.
	existing *NodeView
	data     *NodeData
	content  []byte
	// writeContent says the update carries a body (full content or a
	// patch), so content is written.
	writeContent bool
	validation   *SchemaValidationResult
	backup       *aggregateBackup
}

// PlanContentUpdate works out the body an update would leave a node with,
// given the node's current body. It reports whether the update writes a body
// at all; without content or a patch it returns existing unchanged.
//
// Content and a content patch are alternatives. A patch must apply to
// existing exactly (see ApplyUnifiedPatch). Either way the result may open
// with a frontmatter block only when existing already does: a stored body
// written before the no-frontmatter rule stays editable, but no update can
// introduce one.
func PlanContentUpdate(existing []byte, opts NodeUpdateOptions) ([]byte, bool, error) {
	var result []byte
	switch {
	case opts.HasContent && opts.HasContentPatch:
		return nil, false, fmt.Errorf("content and a content patch are alternatives; send one: %w", ErrInvalid)
	case opts.HasContentPatch:
		patched, err := ApplyUnifiedPatch(existing, opts.ContentPatch)
		if err != nil {
			return nil, false, err
		}
		result = patched
	case opts.HasContent:
		result = opts.Content
	default:
		return existing, false, nil
	}
	if RejectFrontmatter(existing) == nil {
		if err := RejectFrontmatter(result); err != nil {
			return nil, false, err
		}
	}
	return result, true, nil
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
		if !opts.HasContent && !opts.HasContentPatch && !opts.HasMeta {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: fmt.Errorf("content, a content patch, or metadata replacement is required: %w", ErrInvalid)}
		}
		if opts.HasContent && opts.HasContentPatch {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: fmt.Errorf("content and a content patch are alternatives; send one: %w", ErrInvalid)}
		}
		if first, ok := seen[opts.ID]; ok {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: fmt.Errorf("duplicate node id (first at index %d): %w", first, ErrInvalid)}
		}
		seen[opts.ID] = i
		item, err := k.prepareNodeUpdate(ctx, opts)
		if err != nil {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: err}
		}
		backup, err := k.captureAggregateBackup(ctx, item.existing)
		if err != nil {
			return nil, &BatchMutationError{Index: i, NodeID: opts.ID, Err: err}
		}
		item.backup = backup
		prepared[i] = item
	}
	results := make([]NodeUpdateResult, len(prepared))
	createdSnapshots := false
	history := &snapshotTimeline{}
	for i, item := range prepared {
		if item.opts.SnapshotBefore {
			if _, err := k.appendSnapshotNoRefresh(ctx, history, item.opts.ID, "before batch update"); err != nil {
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
			if item.writeContent {
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
		contentHash, metaHash := NodePartHashes(k.Runtime, item.content, item.data.Meta)
		results[i] = NodeUpdateResult{ID: item.opts.ID, Validation: item.validation, Hash: item.data.Stats.Hash(), ContentHash: contentHash, MetaHash: metaHash}
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
		if err := k.refreshSnapshotGeneratedIndexesFrom(ctx, history); err != nil {
			return nil, err
		}
	}
	return results, nil
}

// prepareNodeUpdate checks one update against the node as it stands and
// builds what it would write, without writing anything: the lock, the
// precondition tokens, the planned body (PlanContentUpdate), metadata that
// parses, and the keg's schema rules. Any error means the update would be
// refused.
func (k *LocalKeg) prepareNodeUpdate(ctx context.Context, opts NodeUpdateOptions) (preparedNodeUpdate, error) {
	existing, err := k.ReadNode(ctx, opts.ID)
	if err != nil {
		return preparedNodeUpdate{}, err
	}
	if err := k.validateAggregateLock(ctx, opts.ID, opts.LockToken); err != nil {
		return preparedNodeUpdate{}, err
	}
	// A patch is a content write: it is held to the content token, and
	// checked before it is applied so a stale token reports a conflict
	// rather than a patch that no longer fits.
	check := opts
	if opts.HasContentPatch {
		check.HasContent = true
	}
	if err := checkNodeUpdatePrecondition(ctx, k.Runtime, check, existing); err != nil {
		return preparedNodeUpdate{}, err
	}
	contentBytes, writeContent, err := PlanContentUpdate(existing.Content, opts)
	if err != nil {
		return preparedNodeUpdate{}, err
	}
	metaBytes := existing.Meta
	if opts.HasMeta {
		metaBytes = opts.Meta
	}
	content, err := ParseContent(k.Runtime, contentBytes, MarkdownContentFilename)
	if err != nil {
		return preparedNodeUpdate{}, fmt.Errorf("invalid content: %w", err)
	}
	meta, err := ParseMeta(ctx, metaBytes)
	if err != nil {
		return preparedNodeUpdate{}, fmt.Errorf("invalid metadata: %s: %w", err, ErrInvalid)
	}
	stats, err := cloneNodeStats(ctx, existing.Stats)
	if err != nil {
		return preparedNodeUpdate{}, err
	}
	if stats == nil {
		stats = &NodeStats{}
	}
	// Without submitted meta, meta is the stored metadata: a content-only
	// edit keeps its type. Read it before updateMeta touches meta.
	kept := schemaTypeCandidateInherited(meta)
	now := k.Runtime.Clock().Now()
	data := &NodeData{ID: opts.ID, Content: content, Meta: meta, Stats: stats}
	if err := data.updateMeta(ctx, k.Runtime, &now); err != nil {
		return preparedNodeUpdate{}, err
	}
	// Only meta can declare a type: content may not introduce a frontmatter
	// block, and a legacy one is not read as a type.
	candidate := kept
	if opts.HasMeta {
		candidate = schemaTypeCandidateFromMeta("metadata", meta)
	}
	validation, err := k.validateNodeWrite(ctx, schemaWriteUpdate, opts.ID, data, opts.Schema, candidate)
	if err != nil && !errors.Is(err, ErrNotSupported) {
		return preparedNodeUpdate{}, err
	}
	if validation != nil && validation.Valid {
		validation = nil
	}
	return preparedNodeUpdate{opts: opts, existing: existing, data: data, content: contentBytes, writeContent: writeContent, validation: validation}, nil
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
		history := &snapshotTimeline{}
		for i, item := range nodes {
			snap, err := k.appendSnapshotNoRefresh(ctx, history, item.ID, item.Message)
			if err != nil {
				return nil, &BatchMutationError{Index: i, NodeID: item.ID, Err: err}
			}
			out[i] = snap
		}
		if err := k.refreshSnapshotGeneratedIndexesFrom(ctx, history); err != nil {
			return nil, err
		}
		return out, nil
	})
}
