package keg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func (k *RemoteKeg) ListEntries(ctx context.Context, opts ListEntriesOptions) (*ListEntriesResult, error) {
	var out ListEntriesResult
	if err := k.postJSON(ctx, "/list", "ListEntries", opts, &out, http.StatusOK); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListView resolves a whole listing page in one request.
func (k *RemoteKeg) ListView(ctx context.Context, opts ListViewOptions) (*ListViewResult, error) {
	var out ListViewResult
	if err := k.postJSON(ctx, "/list/view", "ListView", opts, &out, http.StatusOK); err != nil {
		return nil, err
	}
	return &out, nil
}

type remoteNodeView struct {
	ID      string          `json:"id"`
	Content string          `json:"content"`
	Meta    string          `json:"meta"`
	Stats   json.RawMessage `json:"stats"`
	Assets  []string        `json:"assets"`
	Images  []string        `json:"images"`
}

func decodeRemoteNodeView(resp remoteNodeView, withContent bool) (NodeView, error) {
	id, err := ParseNode(resp.ID)
	if err != nil {
		return NodeView{}, err
	}
	stats := &NodeStats{}
	if len(bytes.TrimSpace(resp.Stats)) > 0 && !bytes.Equal(bytes.TrimSpace(resp.Stats), []byte("null")) {
		stats, err = ParseStats(context.Background(), resp.Stats)
		if err != nil {
			return NodeView{}, err
		}
	}
	view := NodeView{ID: *id, Content: []byte(resp.Content), Meta: []byte(resp.Meta), Stats: stats, Files: resp.Assets, Images: resp.Images}
	view.setPartHashes(context.Background(), nil, withContent)
	return view, nil
}

func (k *RemoteKeg) ReadNodes(ctx context.Context, opts ReadNodesOptions) ([]NodeView, error) {
	ids := make([]int, len(opts.NodeIDs))
	for i, id := range opts.NodeIDs {
		ids[i] = id.ID
	}
	req := struct {
		NodeIDs  []int  `json:"node_ids,omitempty"`
		Query    string `json:"query,omitempty"`
		Touch    bool   `json:"touch,omitempty"`
		MetaOnly bool   `json:"meta_only,omitempty"`
	}{ids, opts.Query, opts.Touch, opts.MetaOnly}
	var wire []remoteNodeView
	if err := k.postJSON(ctx, "/nodes/read", "ReadNodes", req, &wire, http.StatusOK); err != nil {
		return nil, err
	}
	out := make([]NodeView, 0, len(wire))
	for _, item := range wire {
		view, err := decodeRemoteNodeView(item, !opts.MetaOnly)
		if err != nil {
			return nil, NewBackendError("remote", "ReadNodes", 0, err, false)
		}
		out = append(out, view)
	}
	return out, nil
}

func (k *RemoteKeg) RelatedNodes(ctx context.Context, opts RelatedNodesOptions) (*RelatedNodesResult, error) {
	ids := make([]int, len(opts.NodeIDs))
	for i, id := range opts.NodeIDs {
		ids[i] = id.ID
	}
	req := struct {
		NodeIDs   []int            `json:"node_ids"`
		Direction RelatedDirection `json:"direction"`
	}{ids, opts.Direction}
	var out RelatedNodesResult
	if err := k.postJSON(ctx, "/related", "RelatedNodes", req, &out, http.StatusOK); err != nil {
		return nil, err
	}
	if out.Entries == nil {
		out.Entries = []NodeIndexEntry{}
	}
	if out.Pairs == nil {
		out.Pairs = []RelatedPair{}
	}
	return &out, nil
}

func (k *RemoteKeg) Info(ctx context.Context) (*KegInfo, error) {
	var out KegInfo
	if err := k.getJSON(ctx, "/info", "Info", &out); err != nil {
		return nil, err
	}
	return &out, nil
}
func (k *RemoteKeg) Doctor(ctx context.Context) ([]DoctorIssue, error) {
	var out []DoctorIssue
	if err := k.getJSON(ctx, "/doctor", "Doctor", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (k *RemoteKeg) RemoveNodes(ctx context.Context, opts RemoveNodesOptions) (RemoveNodesResult, error) {
	nodes := make([]struct {
		ID           int    `json:"id"`
		ExpectedHash string `json:"expected_hash,omitempty"`
	}, len(opts.Nodes))
	for i, item := range opts.Nodes {
		nodes[i].ID = item.ID.ID
		nodes[i].ExpectedHash = item.ExpectedHash
	}
	req := struct {
		Nodes []struct {
			ID           int    `json:"id"`
			ExpectedHash string `json:"expected_hash,omitempty"`
		} `json:"nodes,omitempty"`
		Query string `json:"query,omitempty"`
	}{nodes, opts.Query}
	var wire struct {
		Removed []struct {
			ID        int   `json:"id"`
			Rewritten []int `json:"rewritten"`
		} `json:"removed"`
		Failure *struct {
			NodeID         int    `json:"node_id"`
			Code           string `json:"code"`
			Status         int    `json:"status"`
			Message        string `json:"message"`
			CurrentHash    string `json:"current_hash,omitempty"`
			CurrentContent string `json:"current_content,omitempty"`
		} `json:"failure,omitempty"`
	}
	err := k.postJSON(ctx, "/nodes/remove", "RemoveNodes", req, &wire, http.StatusOK)
	out := RemoveNodesResult{Removed: make([]RemovedNode, 0, len(wire.Removed))}
	for _, item := range wire.Removed {
		rewritten := make([]NodeId, len(item.Rewritten))
		for i, id := range item.Rewritten {
			rewritten[i] = NodeId{ID: id}
		}
		out.Removed = append(out.Removed, RemovedNode{ID: NodeId{ID: item.ID}, Rewritten: rewritten})
	}
	if wire.Failure != nil {
		out.Failure = &BatchFailure{NodeID: NodeId{ID: wire.Failure.NodeID}, Code: wire.Failure.Code, Status: wire.Failure.Status, Message: wire.Failure.Message, CurrentHash: wire.Failure.CurrentHash, CurrentContent: []byte(wire.Failure.CurrentContent)}
	}
	return out, err
}

func (k *RemoteKeg) ValidateNodes(ctx context.Context, opts ValidateNodesOptions) ([]SchemaValidationResult, error) {
	ids := make([]int, len(opts.NodeIDs))
	for i, id := range opts.NodeIDs {
		ids[i] = id.ID
	}
	var out []SchemaValidationResult
	if err := k.postJSON(ctx, "/nodes/validate", "ValidateNodes", struct {
		NodeIDs []int `json:"node_ids,omitempty"`
	}{ids}, &out, http.StatusOK); err != nil {
		return nil, err
	}
	return out, nil
}

func (k *RemoteKeg) CreateSchema(ctx context.Context, typeName string, data []byte) error {
	return k.postJSON(ctx, "/schemas", "CreateSchema", struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}{typeName, string(data)}, nil, http.StatusCreated, http.StatusNoContent)
}

func (k *RemoteKeg) OpenNode(ctx context.Context, opts NodeOpenOptions) (*NodeView, error) {
	var wire remoteNodeView
	if err := k.postJSON(ctx, fmt.Sprintf("/nodes/%d/open", opts.ID.ID), "OpenNode", struct {
		Touch     bool   `json:"touch"`
		LockToken string `json:"lock_token,omitempty"`
	}{opts.Touch, string(opts.LockToken)}, &wire, http.StatusOK); err != nil {
		return nil, err
	}
	view, err := decodeRemoteNodeView(wire, true)
	if err != nil {
		return nil, err
	}
	return &view, nil
}

func (k *RemoteKeg) UpdateNode(ctx context.Context, opts NodeUpdateOptions) (*NodeUpdateResult, error) {
	// A single-node update carries content unless it carries a patch, as
	// LocalKeg.UpdateNode treats it.
	opts.HasContent = !opts.HasContentPatch
	results, err := k.UpdateNodes(ctx, []NodeUpdateOptions{opts})
	if len(results) == 0 {
		return nil, err
	}
	return &results[0], err
}

func (k *RemoteKeg) CreateNodes(ctx context.Context, nodes []NodeCreate) ([]CreateNodeResult, error) {
	type wireNode struct {
		Key     string `json:"key"`
		Schema  string `json:"schema,omitempty"`
		Content string `json:"content,omitempty"`
		Meta    string `json:"meta,omitempty"`
	}
	wire := make([]wireNode, len(nodes))
	for i, n := range nodes {
		// Keyed, not positional: a positional literal is what let Meta be added
		// to NodeCreate and go unsent here without anything failing to compile.
		wire[i] = wireNode{
			Key:     n.Key,
			Schema:  n.Schema,
			Content: string(n.Body),
			Meta:    string(n.Meta),
		}
	}
	var response []struct {
		Key        string                  `json:"key"`
		ID         int                     `json:"id"`
		Hash       string                  `json:"hash"`
		Validation *SchemaValidationResult `json:"validation,omitempty"`
	}
	if err := k.postJSON(ctx, "/nodes", "CreateNodes", struct {
		Nodes []wireNode `json:"nodes"`
	}{wire}, &response, http.StatusCreated, http.StatusOK); err != nil {
		return nil, err
	}
	out := make([]CreateNodeResult, len(response))
	for i, item := range response {
		out[i] = CreateNodeResult{Key: item.Key, ID: NodeId{ID: item.ID}, Hash: item.Hash, Validation: item.Validation}
	}
	return out, nil
}

func (k *RemoteKeg) UpdateNodes(ctx context.Context, updates []NodeUpdateOptions) ([]NodeUpdateResult, error) {
	type wireUpdate struct {
		NodeID       int     `json:"node_id"`
		Schema       string  `json:"schema,omitempty"`
		Content      *string `json:"content,omitempty"`
		ContentPatch *string `json:"content_patch,omitempty"`
		Meta         *string `json:"meta,omitempty"`
		LockToken    string  `json:"lock_token,omitempty"`
		ExpectedHash string  `json:"expected_hash,omitempty"`
		// The part tokens are keyed, not positional, for the same reason
		// as CreateNodes: a field added to NodeUpdateOptions must not go
		// unsent here without anything failing.
		ExpectedContentHash string `json:"expected_content_hash,omitempty"`
		ExpectedMetaHash    string `json:"expected_meta_hash,omitempty"`
		SnapshotBefore      bool   `json:"snapshot_before,omitempty"`
	}
	wire := make([]wireUpdate, len(updates))
	for i, item := range updates {
		wire[i] = wireUpdate{
			NodeID:              item.ID.ID,
			Schema:              item.Schema,
			LockToken:           string(item.LockToken),
			ExpectedHash:        item.ExpectedHash,
			ExpectedContentHash: item.ExpectedContentHash,
			ExpectedMetaHash:    item.ExpectedMetaHash,
			SnapshotBefore:      item.SnapshotBefore,
		}
		if item.HasContent {
			v := string(item.Content)
			wire[i].Content = &v
		}
		if item.HasContentPatch {
			v := item.ContentPatch
			wire[i].ContentPatch = &v
		}
		if item.HasMeta {
			v := string(item.Meta)
			wire[i].Meta = &v
		}
	}
	var response []struct {
		ID          int                     `json:"id"`
		Hash        string                  `json:"hash"`
		ContentHash string                  `json:"content_hash,omitempty"`
		MetaHash    string                  `json:"meta_hash,omitempty"`
		Validation  *SchemaValidationResult `json:"validation,omitempty"`
	}
	if err := k.putJSON(ctx, "/nodes", "UpdateNodes", struct {
		Updates []wireUpdate `json:"updates"`
	}{wire}, &response, http.StatusOK); err != nil {
		return nil, err
	}
	out := make([]NodeUpdateResult, len(response))
	for i, item := range response {
		out[i] = NodeUpdateResult{ID: NodeId{ID: item.ID}, Hash: item.Hash, ContentHash: item.ContentHash, MetaHash: item.MetaHash, Validation: item.Validation}
	}
	return out, nil
}

func (k *RemoteKeg) AppendSnapshots(ctx context.Context, nodes []NodeSnapshotRequest) ([]Snapshot, error) {
	type wireNode struct {
		NodeID  int    `json:"node_id"`
		Message string `json:"message,omitempty"`
	}
	wire := make([]wireNode, len(nodes))
	for i, item := range nodes {
		wire[i] = wireNode{item.ID.ID, item.Message}
	}
	var response []remoteSnapshotEntry
	if err := k.postJSON(ctx, "/nodes/snapshots", "AppendSnapshots", struct {
		Nodes []wireNode `json:"nodes"`
	}{wire}, &response, http.StatusCreated, http.StatusOK); err != nil {
		return nil, err
	}
	out := make([]Snapshot, len(response))
	for i, item := range response {
		snap, err := snapshotFromRemoteEntry(item)
		if err != nil {
			return nil, NewBackendError("remote", "AppendSnapshots", 0, err, false)
		}
		out[i] = snap
	}
	return out, nil
}

func (k *RemoteKeg) DexArtifacts(ctx context.Context) (*DexArtifacts, error) {
	var wire struct {
		Indexes map[string]string `json:"indexes"`
	}
	if err := k.getJSON(ctx, "/dex", "DexArtifacts", &wire); err != nil {
		return nil, err
	}
	out := &DexArtifacts{Indexes: map[string][]byte{}}
	for name, data := range wire.Indexes {
		out.Indexes[name] = []byte(data)
	}
	return out, nil
}

// trashWireEntry is one GET /trash item.
type trashWireEntry struct {
	ID         int        `json:"id"`
	Title      string     `json:"title"`
	Reason     string     `json:"reason"`
	MergedInto *int       `json:"merged_into,omitempty"`
	TrashedAt  time.Time  `json:"trashed_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Size       int64      `json:"size"`
	// DisplacedFrom is the id the entry was trashed at before an archive
	// restore moved it.
	DisplacedFrom *int `json:"displaced_from,omitempty"`
}

func (k *RemoteKeg) ListTrash(ctx context.Context) ([]TrashEntry, error) {
	var wire struct {
		Trash []trashWireEntry `json:"trash"`
	}
	if err := k.getJSON(ctx, "/trash", "ListTrash", &wire); err != nil {
		return nil, err
	}
	out := make([]TrashEntry, len(wire.Trash))
	for i, item := range wire.Trash {
		out[i] = TrashEntry{ID: NodeId{ID: item.ID}, Title: item.Title, Reason: item.Reason, TrashedAt: item.TrashedAt, ExpiresAt: item.ExpiresAt, Size: item.Size}
		if item.MergedInto != nil {
			out[i].MergedInto = &NodeId{ID: *item.MergedInto}
		}
		if item.DisplacedFrom != nil {
			out[i].DisplacedFrom = &NodeId{ID: *item.DisplacedFrom}
		}
	}
	return out, nil
}

// TrashInfo finds id in the hub's trash listing; the REST API has no route
// for a single entry. ErrNotExist when id is not trashed.
func (k *RemoteKeg) TrashInfo(ctx context.Context, id NodeId) (TrashEntry, error) {
	entries, err := k.ListTrash(ctx)
	if err != nil {
		return TrashEntry{}, err
	}
	for _, entry := range entries {
		if entry.ID.ID == id.ID {
			return entry, nil
		}
	}
	return TrashEntry{}, fmt.Errorf("trashed node %s: %w", id.Path(), ErrNotExist)
}

func (k *RemoteKeg) RestoreTrashed(ctx context.Context, ids []NodeId) error {
	return k.postJSON(ctx, "/trash/restore", "RestoreTrashed", struct {
		Nodes []int `json:"nodes"`
	}{remoteNodeNumbers(ids)}, nil, http.StatusOK)
}

func (k *RemoteKeg) PurgeTrashed(ctx context.Context, opts PurgeTrashOptions) (PurgeTrashResult, error) {
	if opts.ExpiredAt != nil {
		// The hub runs its own retention sweep; an expiry purge is local.
		return PurgeTrashResult{}, ErrNotSupported
	}
	var wire struct {
		Purged    []int `json:"purged"`
		Rewritten []int `json:"rewritten"`
	}
	if err := k.postJSON(ctx, "/trash/purge", "PurgeTrashed", struct {
		Nodes []int `json:"nodes"`
	}{remoteNodeNumbers(opts.IDs)}, &wire, http.StatusOK); err != nil {
		return PurgeTrashResult{}, err
	}
	out := PurgeTrashResult{Purged: make([]NodeId, len(wire.Purged)), Rewritten: make([]NodeId, len(wire.Rewritten))}
	for i, id := range wire.Purged {
		out.Purged[i] = NodeId{ID: id}
	}
	for i, id := range wire.Rewritten {
		out.Rewritten[i] = NodeId{ID: id}
	}
	return out, nil
}

func remoteNodeNumbers(ids []NodeId) []int {
	out := make([]int, len(ids))
	for i, id := range ids {
		out[i] = id.ID
	}
	return out
}

func (k *RemoteKeg) MergeNodes(ctx context.Context, opts MergeNodesOptions) (MergeNodesResult, error) {
	type wireTarget struct {
		NodeID              int     `json:"node_id"`
		Schema              string  `json:"schema,omitempty"`
		Content             *string `json:"content,omitempty"`
		ContentPatch        *string `json:"content_patch,omitempty"`
		Meta                *string `json:"meta,omitempty"`
		ExpectedHash        string  `json:"expected_hash,omitempty"`
		ExpectedContentHash string  `json:"expected_content_hash,omitempty"`
		ExpectedMetaHash    string  `json:"expected_meta_hash,omitempty"`
	}
	target := wireTarget{
		NodeID:              opts.Target.ID.ID,
		Schema:              opts.Target.Schema,
		ExpectedHash:        opts.Target.ExpectedHash,
		ExpectedContentHash: opts.Target.ExpectedContentHash,
		ExpectedMetaHash:    opts.Target.ExpectedMetaHash,
	}
	if opts.Target.HasContent {
		v := string(opts.Target.Content)
		target.Content = &v
	}
	if opts.Target.HasContentPatch {
		v := opts.Target.ContentPatch
		target.ContentPatch = &v
	}
	if opts.Target.HasMeta {
		v := string(opts.Target.Meta)
		target.Meta = &v
	}
	var wire struct {
		Target struct {
			ID          int                     `json:"id"`
			Hash        string                  `json:"hash"`
			ContentHash string                  `json:"content_hash,omitempty"`
			MetaHash    string                  `json:"meta_hash,omitempty"`
			Validation  *SchemaValidationResult `json:"validation,omitempty"`
		} `json:"target"`
		Rewritten []int `json:"rewritten"`
	}
	if err := k.postJSON(ctx, "/nodes/merge", "MergeNodes", struct {
		Source             int        `json:"source"`
		ExpectedSourceHash string     `json:"expected_source_hash"`
		Target             wireTarget `json:"target"`
	}{opts.Source.ID, opts.ExpectedSourceHash, target}, &wire, http.StatusOK); err != nil {
		return MergeNodesResult{}, err
	}
	out := MergeNodesResult{Target: NodeUpdateResult{ID: NodeId{ID: wire.Target.ID}, Hash: wire.Target.Hash, ContentHash: wire.Target.ContentHash, MetaHash: wire.Target.MetaHash, Validation: wire.Target.Validation}}
	for _, id := range wire.Rewritten {
		out.Rewritten = append(out.Rewritten, NodeId{ID: id})
	}
	return out, nil
}

func (k *RemoteKeg) ReplaceText(ctx context.Context, opts ReplaceTextOptions) (ReplaceTextResult, error) {
	var wire struct {
		Nodes []struct {
			ReplacedNode
			ID int `json:"id"`
		} `json:"nodes"`
	}
	if err := k.postJSON(ctx, "/nodes/replace", "ReplaceText", opts, &wire, http.StatusOK); err != nil {
		return ReplaceTextResult{}, err
	}
	out := ReplaceTextResult{Nodes: make([]ReplacedNode, len(wire.Nodes))}
	for i, item := range wire.Nodes {
		out.Nodes[i] = item.ReplacedNode
		out.Nodes[i].ID = NodeId{ID: item.ID}
	}
	return out, nil
}
