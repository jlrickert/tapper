package tapper

import (
	"context"
	"fmt"

	"github.com/jlrickert/tapper/pkg/keg"
)

type BatchCreateNode struct {
	Key     string
	Schema  string
	Content string
	Meta    string
}
type BatchCreateOptions struct {
	KegTargetOptions
	Nodes []BatchCreateNode
}

func (t *Tap) CreateBatch(ctx context.Context, opts BatchCreateOptions) ([]keg.CreateNodeResult, error) {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return nil, err
	}
	ctx = keg.WithDefaultValidationActor(ctx, keg.ValidationActorHuman)
	nodes := make([]keg.NodeCreate, len(opts.Nodes))
	for i, item := range opts.Nodes {
		// keg.RejectFrontmatter covers this too; naming the batch item here
		// tells the caller which one to fix.
		if err := keg.RejectFrontmatter([]byte(item.Content)); err != nil {
			return nil, fmt.Errorf("create %d (key %q): %w", i, item.Key, err)
		}
		nodes[i] = keg.NodeCreate{Key: item.Key, Schema: item.Schema, Body: []byte(item.Content), Meta: []byte(item.Meta)}
	}
	return k.CreateNodes(ctx, nodes)
}

type BatchEditItem struct {
	NodeID     string
	Schema     string
	Content    string
	HasContent bool
	// ContentPatch is a unified diff against the node's current content,
	// an alternative to Content.
	ContentPatch    string
	HasContentPatch bool
	Meta            string
	HasMeta         bool
	// ExpectedHash is the combined content+metadata token (node_read's
	// hash). It is used only when neither part token is set; the CLI and
	// other single-token callers rely on it.
	ExpectedHash string
	// ExpectedContentHash and ExpectedMetaHash are the per-part tokens
	// (node_read's content_hash and meta_hash). When either is set, each
	// supplied part must carry its own token and ExpectedHash is ignored.
	ExpectedContentHash string
	ExpectedMetaHash    string
}
type BatchEditOptions struct {
	KegTargetOptions
	Edits []BatchEditItem
}

func (t *Tap) EditBatch(ctx context.Context, opts BatchEditOptions) ([]keg.NodeUpdateResult, error) {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return nil, err
	}
	ctx = keg.WithDefaultValidationActor(ctx, keg.ValidationActorHuman)
	updates := make([]keg.NodeUpdateOptions, len(opts.Edits))
	for i, item := range opts.Edits {
		update, err := batchEditUpdate(ctx, i, item)
		if err != nil {
			return nil, err
		}
		updates[i] = update
	}
	return k.UpdateNodes(ctx, updates)
}

// batchEditUpdate checks one edit item and turns it into the keg update.
func batchEditUpdate(ctx context.Context, i int, item BatchEditItem) (keg.NodeUpdateOptions, error) {
	id, err := keg.ParseNode(item.NodeID)
	if err != nil {
		return keg.NodeUpdateOptions{}, fmt.Errorf("edit %d node %q: %w", i, item.NodeID, err)
	}
	if !item.HasContent && !item.HasContentPatch && !item.HasMeta {
		return keg.NodeUpdateOptions{}, fmt.Errorf("edit %d node %q: content, patch, or meta is required: %w", i, item.NodeID, keg.ErrInvalid)
	}
	if item.HasContent && item.HasContentPatch {
		return keg.NodeUpdateOptions{}, fmt.Errorf("edit %d node %q: content and patch are alternatives; send one: %w", i, item.NodeID, keg.ErrInvalid)
	}
	if err := requireEditPartHashes(i, item); err != nil {
		return keg.NodeUpdateOptions{}, err
	}
	update := keg.NodeUpdateOptions{
		ID:                  *id,
		Schema:              item.Schema,
		ExpectedHash:        item.ExpectedHash,
		ExpectedContentHash: item.ExpectedContentHash,
		ExpectedMetaHash:    item.ExpectedMetaHash,
	}
	if item.HasContent {
		if err := keg.RejectFrontmatter([]byte(item.Content)); err != nil {
			return keg.NodeUpdateOptions{}, fmt.Errorf("edit %d node %q: %w", i, item.NodeID, err)
		}
		update.Content, update.HasContent = []byte(item.Content), true
	}
	if item.HasContentPatch {
		update.ContentPatch, update.HasContentPatch = item.ContentPatch, true
	}
	if item.HasMeta {
		meta, err := keg.ParseMeta(ctx, []byte(item.Meta))
		if err != nil {
			return keg.NodeUpdateOptions{}, fmt.Errorf("edit %d node %q metadata: %w", i, item.NodeID, err)
		}
		update.Meta, update.HasMeta = []byte(meta.ToYAML()), true
	}
	return update, nil
}

type BatchSnapshotItem struct {
	NodeID  string
	Message string
}
type BatchSnapshotOptions struct {
	KegTargetOptions
	Nodes []BatchSnapshotItem
}

func (t *Tap) NodeSnapshotBatch(ctx context.Context, opts BatchSnapshotOptions) ([]keg.Snapshot, error) {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return nil, err
	}
	nodes := make([]keg.NodeSnapshotRequest, len(opts.Nodes))
	for i, item := range opts.Nodes {
		id, err := keg.ParseNode(item.NodeID)
		if err != nil {
			return nil, fmt.Errorf("snapshot %d node %q: %w", i, item.NodeID, err)
		}
		nodes[i] = keg.NodeSnapshotRequest{ID: *id, Message: item.Message}
	}
	return k.AppendSnapshots(ctx, nodes)
}

// requireEditPartHashes rejects an edit that supplies a part without that
// part's token. It applies whenever the caller holds no combined ExpectedHash
// (node_edit never sends one), so an unguarded part can never fall through to
// the keg layer's combined-hash path. It runs after keg resolution so
// authorization failures still surface first.
func requireEditPartHashes(i int, item BatchEditItem) error {
	if item.ExpectedHash != "" && item.ExpectedContentHash == "" && item.ExpectedMetaHash == "" {
		return nil
	}
	if item.HasContentPatch && item.ExpectedContentHash == "" {
		return fmt.Errorf("edit %d node %q: patch requires expected_content_hash (the content_hash from node_read): %w", i, item.NodeID, keg.ErrPreconditionRequired)
	}
	if item.HasContent && item.ExpectedContentHash == "" {
		return fmt.Errorf("edit %d node %q: content requires expected_content_hash (the content_hash from node_read): %w", i, item.NodeID, keg.ErrPreconditionRequired)
	}
	if item.HasMeta && item.ExpectedMetaHash == "" {
		return fmt.Errorf("edit %d node %q: meta requires expected_meta_hash (the meta_hash from node_read): %w", i, item.NodeID, keg.ErrPreconditionRequired)
	}
	return nil
}

type MergeOptions struct {
	KegTargetOptions
	SourceID           string
	ExpectedSourceHash string
	// Target is the surviving node's rewrite, as for one EditBatch item.
	Target BatchEditItem
}

// Merge folds a source node into a target: the target takes the merged
// body, links to the source follow it, and the source goes to the trash.
func (t *Tap) Merge(ctx context.Context, opts MergeOptions) (keg.MergeNodesResult, error) {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return keg.MergeNodesResult{}, err
	}
	ctx = keg.WithDefaultValidationActor(ctx, keg.ValidationActorHuman)
	source, err := keg.ParseNode(opts.SourceID)
	if err != nil {
		return keg.MergeNodesResult{}, fmt.Errorf("merge source %q: %w", opts.SourceID, err)
	}
	if !opts.Target.HasContent && !opts.Target.HasContentPatch {
		return keg.MergeNodesResult{}, fmt.Errorf("merge into %q: content or patch is required, since a merge rewrites the target: %w", opts.Target.NodeID, keg.ErrInvalid)
	}
	update, err := batchEditUpdate(ctx, 0, opts.Target)
	if err != nil {
		return keg.MergeNodesResult{}, err
	}
	return k.MergeNodes(ctx, keg.MergeNodesOptions{Source: *source, ExpectedSourceHash: opts.ExpectedSourceHash, Target: update})
}
