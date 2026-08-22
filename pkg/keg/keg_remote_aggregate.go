package keg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

func (k *RemoteKeg) ListEntries(ctx context.Context, opts ListEntriesOptions) (*ListEntriesResult, error) {
	var out ListEntriesResult
	if err := k.postJSON(ctx, "/list", "ListEntries", opts, &out, http.StatusOK); err != nil {
		return nil, err
	}
	return &out, nil
}

// ErrListViewUnsupported reports that the hub predates the server-resolved
// listing endpoint. Callers degrade to assembling the listing client-side.
var ErrListViewUnsupported = errors.New("hub list view API is unavailable")

// ListView resolves a whole listing page in one request. A hub that does not
// implement the route answers 404, which is reported as
// ErrListViewUnsupported so the caller can fall back rather than fail.
func (k *RemoteKeg) ListView(ctx context.Context, opts ListViewOptions) (*ListViewResult, error) {
	var out ListViewResult
	if err := k.postJSON(ctx, "/list/view", "ListView", opts, &out, http.StatusOK); err != nil {
		if _, status := RemoteErrorCode(err); status == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %w", ErrListViewUnsupported, err)
		}
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

func decodeRemoteNodeView(resp remoteNodeView) (NodeView, error) {
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
	return NodeView{ID: *id, Content: []byte(resp.Content), Meta: []byte(resp.Meta), Stats: stats, Files: resp.Assets, Images: resp.Images}, nil
}

func (k *RemoteKeg) ReadNodes(ctx context.Context, opts ReadNodesOptions) ([]NodeView, error) {
	ids := make([]int, len(opts.NodeIDs))
	for i, id := range opts.NodeIDs {
		ids[i] = id.ID
	}
	req := struct {
		NodeIDs []int  `json:"node_ids,omitempty"`
		Query   string `json:"query,omitempty"`
		Touch   bool   `json:"touch,omitempty"`
	}{ids, opts.Query, opts.Touch}
	var wire []remoteNodeView
	if err := k.postJSON(ctx, "/nodes/read", "ReadNodes", req, &wire, http.StatusOK); err != nil {
		return nil, err
	}
	out := make([]NodeView, 0, len(wire))
	for _, item := range wire {
		view, err := decodeRemoteNodeView(item)
		if err != nil {
			return nil, NewBackendError("remote", "ReadNodes", 0, err, false)
		}
		out = append(out, view)
	}
	return out, nil
}

func (k *RemoteKeg) RelatedNodes(ctx context.Context, opts RelatedNodesOptions) ([]NodeIndexEntry, error) {
	ids := make([]int, len(opts.NodeIDs))
	for i, id := range opts.NodeIDs {
		ids[i] = id.ID
	}
	req := struct {
		NodeIDs   []int            `json:"node_ids"`
		Direction RelatedDirection `json:"direction"`
	}{ids, opts.Direction}
	var out struct {
		Entries []NodeIndexEntry `json:"entries"`
	}
	if err := k.postJSON(ctx, "/related", "RelatedNodes", req, &out, http.StatusOK); err != nil {
		return nil, err
	}
	return out.Entries, nil
}

func (k *RemoteKeg) Graph(ctx context.Context) (*GraphView, error) {
	var out GraphView
	if err := k.getJSON(ctx, "/graph", "Graph", &out); err != nil {
		return nil, err
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
	ids := make([]int, len(opts.NodeIDs))
	for i, id := range opts.NodeIDs {
		ids[i] = id.ID
	}
	req := struct {
		NodeIDs []int  `json:"node_ids,omitempty"`
		Query   string `json:"query,omitempty"`
	}{ids, opts.Query}
	var wire struct {
		Removed []struct {
			ID        int   `json:"id"`
			Rewritten []int `json:"rewritten"`
		} `json:"removed"`
		Failure *struct {
			NodeID  int    `json:"node_id"`
			Code    string `json:"code"`
			Status  int    `json:"status"`
			Message string `json:"message"`
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
		out.Failure = &BatchFailure{NodeID: NodeId{ID: wire.Failure.NodeID}, Code: wire.Failure.Code, Status: wire.Failure.Status, Message: wire.Failure.Message}
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
	view, err := decodeRemoteNodeView(wire)
	if err != nil {
		return nil, err
	}
	return &view, nil
}

func (k *RemoteKeg) UpdateNode(ctx context.Context, opts NodeUpdateOptions) (*NodeUpdateResult, error) {
	opts.HasContent = true
	results, err := k.UpdateNodes(ctx, []NodeUpdateOptions{opts})
	if len(results) == 0 {
		return nil, err
	}
	return &results[0], err
}

func (k *RemoteKeg) CreateNodes(ctx context.Context, nodes []NodeCreate) ([]CreateNodeResult, error) {
	type wireNode struct {
		Key    string         `json:"key"`
		Schema string         `json:"schema,omitempty"`
		Title  string         `json:"title,omitempty"`
		Lead   string         `json:"lead,omitempty"`
		Body   string         `json:"body,omitempty"`
		Tags   []string       `json:"tags,omitempty"`
		Attrs  map[string]any `json:"attrs,omitempty"`
	}
	wire := make([]wireNode, len(nodes))
	for i, n := range nodes {
		wire[i] = wireNode{n.Key, n.Schema, n.Title, n.Lead, string(n.Body), n.Tags, n.Attrs}
	}
	var response []struct {
		Key        string                  `json:"key"`
		ID         int                     `json:"id"`
		Hash       string                  `json:"hash"`
		Validation *SchemaValidationResult `json:"validation,omitempty"`
	}
	if err := k.postJSON(ctx, "/nodes/batch", "CreateNodes", struct {
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
		NodeID         int     `json:"node_id"`
		Schema         string  `json:"schema,omitempty"`
		Content        *string `json:"content,omitempty"`
		Meta           *string `json:"meta,omitempty"`
		LockToken      string  `json:"lock_token,omitempty"`
		ExpectedHash   string  `json:"expected_hash,omitempty"`
		SnapshotBefore bool    `json:"snapshot_before,omitempty"`
	}
	wire := make([]wireUpdate, len(updates))
	for i, item := range updates {
		wire[i] = wireUpdate{NodeID: item.ID.ID, Schema: item.Schema, LockToken: string(item.LockToken), ExpectedHash: item.ExpectedHash, SnapshotBefore: item.SnapshotBefore}
		if item.HasContent {
			v := string(item.Content)
			wire[i].Content = &v
		}
		if item.HasMeta {
			v := string(item.Meta)
			wire[i].Meta = &v
		}
	}
	var response []struct {
		ID         int                     `json:"id"`
		Hash       string                  `json:"hash"`
		Validation *SchemaValidationResult `json:"validation,omitempty"`
	}
	if err := k.putJSON(ctx, "/nodes/batch", "UpdateNodes", struct {
		Updates []wireUpdate `json:"updates"`
	}{wire}, &response, http.StatusOK); err != nil {
		return nil, err
	}
	out := make([]NodeUpdateResult, len(response))
	for i, item := range response {
		out[i] = NodeUpdateResult{ID: NodeId{ID: item.ID}, Hash: item.Hash, Validation: item.Validation}
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
	if err := k.postJSON(ctx, "/nodes/snapshots/batch", "AppendSnapshots", struct {
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

func (k *RemoteKeg) ReplaceNodesWithRedirects(ctx context.Context, redirects []NodeRedirect) (ReplaceNodesWithRedirectsResult, error) {
	type item struct {
		ID           int    `json:"id"`
		Target       string `json:"target"`
		Title        string `json:"title,omitempty"`
		TargetID     int    `json:"target_id"`
		ExpectedHash string `json:"expected_hash,omitempty"`
	}
	wire := make([]item, len(redirects))
	for i, r := range redirects {
		wire[i] = item{r.ID.ID, r.Target, r.Title, r.TargetID.ID, r.ExpectedHash}
	}
	var response struct {
		Replaced []int `json:"replaced"`
		Failure  *struct {
			NodeID  int    `json:"node_id"`
			Code    string `json:"code"`
			Status  int    `json:"status"`
			Message string `json:"message"`
		} `json:"failure,omitempty"`
	}
	err := k.postJSON(ctx, "/nodes/redirects", "ReplaceNodesWithRedirects", struct {
		Redirects []item `json:"redirects"`
	}{wire}, &response, http.StatusOK)
	result := ReplaceNodesWithRedirectsResult{Replaced: make([]NodeId, len(response.Replaced))}
	for i, id := range response.Replaced {
		result.Replaced[i] = NodeId{ID: id}
	}
	if response.Failure != nil {
		result.Failure = &BatchFailure{NodeID: NodeId{ID: response.Failure.NodeID}, Code: response.Failure.Code, Status: response.Failure.Status, Message: response.Failure.Message}
	}
	return result, err
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
