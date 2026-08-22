package tapper

import (
	"context"
	"fmt"

	"github.com/jlrickert/tapper/pkg/keg"
	"gopkg.in/yaml.v3"
)

type BatchCreateNode struct {
	Key   string
	Title string
	Lead  string
	Body  string
	Tags  []string
	Attrs map[string]string
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
		nodes[i] = keg.NodeCreate{Key: item.Key, Title: item.Title, Lead: item.Lead, Body: []byte(item.Body), Tags: item.Tags, Attrs: createAttrsFromStrings(item.Attrs)}
	}
	return k.CreateNodes(ctx, nodes)
}

type BatchEditItem struct {
	NodeID         string
	Content        string
	ExpectedHash   string
	SnapshotBefore bool
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
		hasMeta, rawMeta, body, err := splitEditNodeFile([]byte(item.Content))
		if err != nil {
			return nil, fmt.Errorf("edit %d node %q: %w", i, item.NodeID, err)
		}
		updates[i] = keg.NodeUpdateOptions{ID: *id, Content: body, HasContent: true, ExpectedHash: item.ExpectedHash, SnapshotBefore: item.SnapshotBefore}
		if hasMeta {
			meta, err := keg.ParseMeta(ctx, rawMeta)
			if err != nil {
				return nil, fmt.Errorf("edit %d node %q metadata: %w", i, item.NodeID, err)
			}
			updates[i].Meta, updates[i].HasMeta = []byte(meta.ToYAML()), true
		}
	}
	return k.UpdateNodes(ctx, updates)
}

type BatchMetaUpdate struct {
	NodeID         string
	Content        string
	ExpectedHash   string
	SnapshotBefore bool
}
type BatchMetaOptions struct {
	KegTargetOptions
	NodeIDs []string
	Updates []BatchMetaUpdate
}
type BatchMetaResult struct {
	NodeID  string `json:"node_id"`
	Content string `json:"content"`
}

func (t *Tap) MetaBatch(ctx context.Context, opts BatchMetaOptions) ([]BatchMetaResult, []keg.NodeUpdateResult, error) {
	role := FlightRoleViewer
	if len(opts.Updates) > 0 {
		role = FlightRoleEditor
	}
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, role)
	if err != nil {
		return nil, nil, err
	}
	if len(opts.NodeIDs) > 0 && len(opts.Updates) > 0 {
		return nil, nil, fmt.Errorf("node_ids and updates are mutually exclusive: %w", keg.ErrInvalid)
	}
	if len(opts.Updates) > 0 {
		ctx = keg.WithDefaultValidationActor(ctx, keg.ValidationActorHuman)
		updates := make([]keg.NodeUpdateOptions, len(opts.Updates))
		for i, item := range opts.Updates {
			id, err := keg.ParseNode(item.NodeID)
			if err != nil {
				return nil, nil, fmt.Errorf("metadata update %d node %q: %w", i, item.NodeID, err)
			}
			var raw map[string]any
			if err := yaml.Unmarshal([]byte(item.Content), &raw); err != nil {
				return nil, nil, fmt.Errorf("metadata update %d node %q: %w", i, item.NodeID, err)
			}
			meta, err := keg.ParseMeta(ctx, []byte(item.Content))
			if err != nil {
				return nil, nil, fmt.Errorf("metadata update %d node %q: %w", i, item.NodeID, err)
			}
			updates[i] = keg.NodeUpdateOptions{ID: *id, Meta: []byte(meta.ToYAML()), HasMeta: true, ExpectedHash: item.ExpectedHash, SnapshotBefore: item.SnapshotBefore}
		}
		results, err := k.UpdateNodes(ctx, updates)
		return nil, results, err
	}
	if len(opts.NodeIDs) == 0 {
		return nil, nil, fmt.Errorf("node_ids must contain at least one item: %w", keg.ErrInvalid)
	}
	if len(opts.NodeIDs) > keg.MaxMutationBatchSize {
		return nil, nil, fmt.Errorf("node_ids exceeds maximum %d: %w", keg.MaxMutationBatchSize, keg.ErrInvalid)
	}
	ids := make([]keg.NodeId, len(opts.NodeIDs))
	seen := map[keg.NodeId]int{}
	for i, raw := range opts.NodeIDs {
		id, err := keg.ParseNode(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("metadata read %d node %q: %w", i, raw, err)
		}
		ids[i] = *id
		if first, ok := seen[*id]; ok {
			return nil, nil, fmt.Errorf("metadata read %d node %q duplicates index %d: %w", i, raw, first, keg.ErrInvalid)
		}
		seen[*id] = i
	}
	views, err := k.ReadNodes(ctx, keg.ReadNodesOptions{NodeIDs: ids})
	if err != nil {
		return nil, nil, err
	}
	out := make([]BatchMetaResult, len(views))
	for i, view := range views {
		out[i] = BatchMetaResult{NodeID: view.ID.Path(), Content: string(view.Meta)}
	}
	return out, nil, nil
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
