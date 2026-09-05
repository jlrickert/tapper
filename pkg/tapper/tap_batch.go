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
	NodeID       string
	Schema       string
	Content      string
	HasContent   bool
	Meta         string
	HasMeta      bool
	ExpectedHash string
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
		id, err := keg.ParseNode(item.NodeID)
		if err != nil {
			return nil, fmt.Errorf("edit %d node %q: %w", i, item.NodeID, err)
		}
		if !item.HasContent && !item.HasMeta {
			return nil, fmt.Errorf("edit %d node %q: content or meta is required: %w", i, item.NodeID, keg.ErrInvalid)
		}
		update := keg.NodeUpdateOptions{ID: *id, Schema: item.Schema, ExpectedHash: item.ExpectedHash}
		if item.HasContent {
			if err := keg.RejectFrontmatter([]byte(item.Content)); err != nil {
				return nil, fmt.Errorf("edit %d node %q: %w", i, item.NodeID, err)
			}
			update.Content, update.HasContent = []byte(item.Content), true
		}
		if item.HasMeta {
			meta, err := keg.ParseMeta(ctx, []byte(item.Meta))
			if err != nil {
				return nil, fmt.Errorf("edit %d node %q metadata: %w", i, item.NodeID, err)
			}
			update.Meta, update.HasMeta = []byte(meta.ToYAML()), true
		}
		updates[i] = update
	}
	return k.UpdateNodes(ctx, updates)
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
