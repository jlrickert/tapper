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
		update, err := batchEditUpdate(ctx, i, opts.Keg, item)
		if err != nil {
			return nil, err
		}
		updates[i] = update
	}
	return k.UpdateNodes(ctx, updates)
}

// batchEditUpdate checks one edit item and turns it into the keg update.
// kegAlias is the keg the caller targeted, carried into a missing-hash error
// so it can name the node_read that fixes it.
func batchEditUpdate(ctx context.Context, i int, kegAlias string, item BatchEditItem) (keg.NodeUpdateOptions, error) {
	id, err := keg.ParseNode(item.NodeID)
	if err != nil {
		return keg.NodeUpdateOptions{}, fmt.Errorf("edit %d node %q: %w: %w", i, item.NodeID, err, keg.ErrInvalid)
	}
	if !item.HasContent && !item.HasContentPatch && !item.HasMeta {
		return keg.NodeUpdateOptions{}, &EmptyEditError{Item: i, NodeID: item.NodeID}
	}
	if item.HasContent && item.HasContentPatch {
		return keg.NodeUpdateOptions{}, fmt.Errorf("edit %d node %q: content and patch are alternatives; send one: %w", i, item.NodeID, keg.ErrInvalid)
	}
	if err := requireEditPartHashes(i, kegAlias, item); err != nil {
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

// EmptyEditError reports an edit item that supplies none of content, patch,
// or meta, so it has nothing to change. It unwraps to keg.ErrInvalid; the
// typed form lets a surface say which field to add instead of a generic
// malformed-request hint.
type EmptyEditError struct {
	Item   int
	NodeID string
}

func (e *EmptyEditError) Error() string {
	return fmt.Sprintf("item %d (node %q) has nothing to change: supply content, patch, or meta: %s", e.Item, e.NodeID, keg.ErrInvalid)
}

func (e *EmptyEditError) Unwrap() error { return keg.ErrInvalid }

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
			return nil, fmt.Errorf("snapshot %d node %q: %w: %w", i, item.NodeID, err, keg.ErrInvalid)
		}
		nodes[i] = keg.NodeSnapshotRequest{ID: *id, Message: item.Message}
	}
	return k.AppendSnapshots(ctx, nodes)
}

// requireEditPartHashes rejects an edit that supplies a part without that
// part's token. It applies whenever the caller holds no combined ExpectedHash
// (node_edit never sends one), so an unguarded part can never fall through to
// the keg layer's combined-hash path. It runs after keg resolution so
// authorization failures still surface first. Every missing token is reported
// at once, so one node_read is enough to fix the call.
func requireEditPartHashes(i int, kegAlias string, item BatchEditItem) error {
	if item.ExpectedHash != "" && item.ExpectedContentHash == "" && item.ExpectedMetaHash == "" {
		return nil
	}
	var missing []keg.MissingPartHash
	if item.ExpectedContentHash == "" {
		switch {
		case item.HasContentPatch:
			missing = append(missing, keg.MissingPartHash{Part: "patch", Field: "expected_content_hash", TokenField: "content_hash"})
		case item.HasContent:
			missing = append(missing, keg.MissingPartHash{Part: "content", Field: "expected_content_hash", TokenField: "content_hash"})
		}
	}
	if item.HasMeta && item.ExpectedMetaHash == "" {
		missing = append(missing, keg.MissingPartHash{Part: "meta", Field: "expected_meta_hash", TokenField: "meta_hash"})
	}
	if len(missing) == 0 {
		return nil
	}
	return &keg.PartPreconditionRequiredError{Item: i, Keg: kegAlias, NodeID: item.NodeID, Missing: missing}
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
		return keg.MergeNodesResult{}, fmt.Errorf("merge source %q: %w: %w", opts.SourceID, err, keg.ErrInvalid)
	}
	if !opts.Target.HasContent && !opts.Target.HasContentPatch {
		return keg.MergeNodesResult{}, fmt.Errorf("merge into %q: content or patch is required, since a merge rewrites the target: %w", opts.Target.NodeID, keg.ErrInvalid)
	}
	update, err := batchEditUpdate(ctx, 0, opts.Keg, opts.Target)
	if err != nil {
		return keg.MergeNodesResult{}, err
	}
	return k.MergeNodes(ctx, keg.MergeNodesOptions{Source: *source, ExpectedSourceHash: opts.ExpectedSourceHash, Target: update})
}
