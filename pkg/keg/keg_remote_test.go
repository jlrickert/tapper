package keg_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// mockOpsHub is an httptest server implementing the hub's operation-level
// API over a real backing *LocalKeg. Each route is a thin mirror of the hub
// handlers — no business logic is reimplemented; every request delegates to
// the backing keg exactly like tapper-hub's handlers delegate to theirs.
//
// It counts requests so tests can assert the headline guarantee of the
// RemoteKeg refactor: one HTTP round trip per Keg operation.
type mockOpsHub struct {
	backing  *kegpkg.LocalKeg
	token    string
	requests atomic.Int64
	srv      *httptest.Server
}

func (h *mockOpsHub) writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *mockOpsHub) writeError(w http.ResponseWriter, status int, msg, code string) {
	h.writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// kegError mirrors the hub's kegError: map the sentinel through the shared
// wire table.
func (h *mockOpsHub) kegError(w http.ResponseWriter, err error) {
	code, status := kegpkg.RemoteErrorCode(err)
	h.writeError(w, status, err.Error(), code)
}

func (h *mockOpsHub) parseID(w http.ResponseWriter, r *http.Request) (kegpkg.NodeId, bool) {
	n, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid node id", "BAD_REQUEST")
		return kegpkg.NodeId{}, false
	}
	return kegpkg.NodeId{ID: n}, true
}

func rewrittenWire(ids []kegpkg.NodeId) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.Path()
	}
	return out
}

func snapshotWire(item kegpkg.Snapshot) map[string]any {
	return map[string]any{
		"id": item.ID, "node": item.Node.ID, "parent": item.Parent,
		"created_at": item.CreatedAt.Format(time.RFC3339), "message": item.Message,
		"content_hash": item.ContentHash, "meta_hash": item.MetaHash,
		"stats_hash": item.StatsHash, "is_checkpoint": item.IsCheckpoint,
	}
}

// newMockOpsHub builds an initialized memory-backed keg with two linked,
// tagged nodes and serves the operation API over it.
func newMockOpsHub(t *testing.T, f *sandbox.Sandbox, token string) *mockOpsHub {
	t.Helper()

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	backing := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, backing, f.Context())
	_, err := backing.Create(f.Context(), &kegpkg.CreateOptions{
		Title: "Alpha node",
		Body:  []byte("# Alpha node\n\nAlpha body links to [beta](../2)"),
		Tags:  []string{"alpha", "shared"},
	})
	require.NoError(t, err)
	_, err = backing.Create(f.Context(), &kegpkg.CreateOptions{
		Title: "Beta node",
		Body:  []byte("# Beta node\n\nBeta body mentions gamma rays"),
		Tags:  []string{"beta", "shared"},
	})
	require.NoError(t, err)

	h := &mockOpsHub{backing: backing, token: token}
	mux := http.NewServeMux()

	// Node lifecycle
	mux.HandleFunc("GET /nodes", func(w http.ResponseWriter, r *http.Request) {
		ids, err := backing.ListNodes(r.Context())
		if err != nil {
			h.kegError(w, err)
			return
		}
		out := make([]int, len(ids))
		for i, id := range ids {
			out[i] = id.ID
		}
		h.writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /nodes/next", func(w http.ResponseWriter, r *http.Request) {
		id, err := backing.Next(r.Context())
		if err != nil {
			h.kegError(w, err)
			return
		}
		h.writeJSON(w, http.StatusOK, map[string]int{"id": id.ID})
	})
	mux.HandleFunc("POST /nodes", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Content *string `json:"content"`
			Meta    *string `json:"meta"`
			Schema  string  `json:"schema"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Content == nil {
			h.writeError(w, http.StatusBadRequest, "invalid JSON body", "BAD_REQUEST")
			return
		}
		var meta *kegpkg.NodeMeta
		if req.Meta != nil && strings.TrimSpace(*req.Meta) != "" {
			parsed, err := kegpkg.ParseMeta(r.Context(), []byte(*req.Meta))
			if err != nil {
				h.writeError(w, http.StatusBadRequest, "invalid meta: "+err.Error(), "BAD_REQUEST")
				return
			}
			meta = parsed
		}
		id, err := backing.Create(r.Context(), &kegpkg.CreateOptions{Schema: req.Schema, Body: []byte(*req.Content)})
		if err != nil {
			h.kegError(w, err)
			return
		}
		if meta != nil {
			if err := backing.SetMeta(r.Context(), id.ID, meta); err != nil {
				h.kegError(w, err)
				return
			}
		}
		h.writeJSON(w, http.StatusCreated, map[string]int{"id": id.ID.ID})
	})
	mux.HandleFunc("POST /nodes/batch", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Nodes []struct {
				Key    string         `json:"key"`
				Schema string         `json:"schema"`
				Title  string         `json:"title"`
				Lead   string         `json:"lead"`
				Body   string         `json:"body"`
				Tags   []string       `json:"tags"`
				Attrs  map[string]any `json:"attrs"`
			} `json:"nodes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid JSON body", "BAD_REQUEST")
			return
		}
		nodes := make([]kegpkg.NodeCreate, len(req.Nodes))
		for i, item := range req.Nodes {
			nodes[i] = kegpkg.NodeCreate{Key: item.Key, Schema: item.Schema, Title: item.Title, Lead: item.Lead, Body: []byte(item.Body), Tags: item.Tags, Attrs: item.Attrs}
		}
		results, err := backing.CreateNodes(r.Context(), nodes)
		if err != nil {
			h.kegError(w, err)
			return
		}
		wire := make([]map[string]any, len(results))
		for i, item := range results {
			wire[i] = map[string]any{"key": item.Key, "id": item.ID.ID, "hash": item.Hash, "validation": item.Validation}
		}
		h.writeJSON(w, http.StatusCreated, wire)
	})
	mux.HandleFunc("PUT /nodes/batch", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Updates []struct {
				NodeID         int     `json:"node_id"`
				Schema         string  `json:"schema"`
				Content        *string `json:"content"`
				Meta           *string `json:"meta"`
				LockToken      string  `json:"lock_token"`
				ExpectedHash   string  `json:"expected_hash"`
				SnapshotBefore bool    `json:"snapshot_before"`
			} `json:"updates"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid JSON body", "BAD_REQUEST")
			return
		}
		updates := make([]kegpkg.NodeUpdateOptions, len(req.Updates))
		for i, item := range req.Updates {
			updates[i] = kegpkg.NodeUpdateOptions{ID: kegpkg.NodeId{ID: item.NodeID}, Schema: item.Schema, LockToken: kegpkg.LockToken(item.LockToken), ExpectedHash: item.ExpectedHash, SnapshotBefore: item.SnapshotBefore}
			if item.Content != nil {
				updates[i].Content, updates[i].HasContent = []byte(*item.Content), true
			}
			if item.Meta != nil {
				updates[i].Meta, updates[i].HasMeta = []byte(*item.Meta), true
			}
		}
		results, err := backing.UpdateNodes(r.Context(), updates)
		if err != nil {
			h.kegError(w, err)
			return
		}
		wire := make([]map[string]any, len(results))
		for i, item := range results {
			wire[i] = map[string]any{"id": item.ID.ID, "hash": item.Hash, "validation": item.Validation}
		}
		h.writeJSON(w, http.StatusOK, wire)
	})
	mux.HandleFunc("POST /nodes/snapshots/batch", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Nodes []struct {
				NodeID  int    `json:"node_id"`
				Message string `json:"message"`
			} `json:"nodes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid JSON body", "BAD_REQUEST")
			return
		}
		nodes := make([]kegpkg.NodeSnapshotRequest, len(req.Nodes))
		for i, item := range req.Nodes {
			nodes[i] = kegpkg.NodeSnapshotRequest{ID: kegpkg.NodeId{ID: item.NodeID}, Message: item.Message}
		}
		snapshots, err := backing.AppendSnapshots(r.Context(), nodes)
		if err != nil {
			h.kegError(w, err)
			return
		}
		wire := make([]map[string]any, len(snapshots))
		for i, item := range snapshots {
			wire[i] = snapshotWire(item)
		}
		h.writeJSON(w, http.StatusCreated, wire)
	})
	mux.HandleFunc("GET /nodes/{id}/snapshots", func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.parseID(w, r)
		if !ok {
			return
		}
		snapshots, err := backing.ListSnapshots(r.Context(), id)
		if err != nil {
			h.kegError(w, err)
			return
		}
		wire := make([]map[string]any, len(snapshots))
		for i, item := range snapshots {
			wire[i] = snapshotWire(item)
		}
		h.writeJSON(w, http.StatusOK, wire)
	})
	mux.HandleFunc("GET /nodes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.parseID(w, r)
		if !ok {
			return
		}
		// HEAD /nodes/{id} doubles as the existence probe (Go's ServeMux
		// routes HEAD through GET handlers), mirroring the hub's HasNode.
		if r.Method == http.MethodHead {
			exists, err := backing.NodeExists(r.Context(), id)
			if err != nil {
				h.kegError(w, err)
				return
			}
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		view, err := backing.ReadNode(r.Context(), id)
		if err != nil {
			h.kegError(w, err)
			return
		}
		resp := map[string]any{
			"id":      view.ID.Path(),
			"content": string(view.Content),
			"meta":    string(view.Meta),
			"assets":  view.Files,
			"images":  view.Images,
		}
		if view.Stats != nil {
			statsJSON, err := view.Stats.ToJSON()
			if err != nil {
				h.writeError(w, http.StatusInternalServerError, err.Error(), "INTERNAL")
				return
			}
			resp["stats"] = json.RawMessage(statsJSON)
		}
		h.writeJSON(w, http.StatusOK, resp)
	})
	mux.HandleFunc("DELETE /nodes/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.parseID(w, r)
		if !ok {
			return
		}
		rewritten, err := backing.Remove(r.Context(), id)
		if err != nil {
			h.kegError(w, err)
			return
		}
		h.writeJSON(w, http.StatusOK, map[string][]string{"rewritten": rewrittenWire(rewritten)})
	})
	mux.HandleFunc("POST /nodes/{id}/move", func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.parseID(w, r)
		if !ok {
			return
		}
		var req struct {
			Dst int `json:"dst"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid JSON body", "BAD_REQUEST")
			return
		}
		rewritten, err := backing.Move(r.Context(), id, kegpkg.NodeId{ID: req.Dst})
		if err != nil {
			h.kegError(w, err)
			return
		}
		h.writeJSON(w, http.StatusOK, map[string][]string{"rewritten": rewrittenWire(rewritten)})
	})
	mux.HandleFunc("POST /nodes/{id}/touch", func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.parseID(w, r)
		if !ok {
			return
		}
		if err := backing.Touch(r.Context(), id); err != nil {
			h.kegError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// Node primary data
	mux.HandleFunc("GET /nodes/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.parseID(w, r)
		if !ok {
			return
		}
		data, err := backing.GetContent(r.Context(), id)
		if err != nil {
			h.kegError(w, err)
			return
		}
		w.Write(data)
	})
	mux.HandleFunc("PUT /nodes/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("RemoteKeg used removed raw content mutation route")
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("GET /nodes/{id}/meta", func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.parseID(w, r)
		if !ok {
			return
		}
		data, err := backing.GetMetaRaw(r.Context(), id)
		if err != nil {
			h.kegError(w, err)
			return
		}
		w.Write(data)
	})
	mux.HandleFunc("PUT /nodes/{id}/meta", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("RemoteKeg used removed raw metadata mutation route")
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	mux.HandleFunc("GET /nodes/{id}/stats", func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.parseID(w, r)
		if !ok {
			return
		}
		stats, err := backing.GetStats(r.Context(), id)
		if err != nil {
			h.kegError(w, err)
			return
		}
		data, _ := stats.ToJSON()
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	})

	// Indexes / dex / query / grep / summary
	mux.HandleFunc("GET /indexes", func(w http.ResponseWriter, r *http.Request) {
		names, err := backing.ListIndexes(r.Context())
		if err != nil {
			h.kegError(w, err)
			return
		}
		h.writeJSON(w, http.StatusOK, names)
	})
	mux.HandleFunc("GET /indexes/{name}", func(w http.ResponseWriter, r *http.Request) {
		data, err := backing.ReadIndex(r.Context(), r.PathValue("name"))
		if err != nil {
			h.kegError(w, err)
			return
		}
		w.Write(data)
	})
	mux.HandleFunc("POST /index/rebuild", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			NoUpdate bool `json:"no_update"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := backing.Index(r.Context(), kegpkg.IndexOptions{NoUpdate: req.NoUpdate}); err != nil {
			h.kegError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /dex", func(w http.ResponseWriter, r *http.Request) {
		names, err := backing.ListIndexes(r.Context())
		if err != nil {
			h.kegError(w, err)
			return
		}
		indexes := make(map[string]string, len(names))
		for _, name := range names {
			data, err := backing.ReadIndex(r.Context(), name)
			if err != nil {
				h.kegError(w, err)
				return
			}
			indexes[name] = string(data)
		}
		h.writeJSON(w, http.StatusOK, map[string]any{"indexes": indexes})
	})
	mux.HandleFunc("POST /query", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Expr string `json:"expr"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid JSON body", "BAD_REQUEST")
			return
		}
		entries, err := backing.Query(r.Context(), kegpkg.QueryOptions{Expr: req.Expr})
		if err != nil {
			h.kegError(w, err)
			return
		}
		if entries == nil {
			entries = []kegpkg.NodeIndexEntry{}
		}
		h.writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
	})
	mux.HandleFunc("POST /grep", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Pattern    string `json:"pattern"`
			IgnoreCase bool   `json:"ignore_case"`
			MaxLines   int    `json:"max_lines"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid JSON body", "BAD_REQUEST")
			return
		}
		matches, err := backing.Grep(r.Context(), kegpkg.GrepOptions{
			Pattern:    req.Pattern,
			IgnoreCase: req.IgnoreCase,
			MaxLines:   req.MaxLines,
		})
		if err != nil {
			h.kegError(w, err)
			return
		}
		type matchWire struct {
			Entry kegpkg.NodeIndexEntry `json:"entry"`
			Lines []string              `json:"lines"`
		}
		out := make([]matchWire, len(matches))
		for i, m := range matches {
			out[i] = matchWire{Entry: m.Entry, Lines: m.Lines}
		}
		h.writeJSON(w, http.StatusOK, map[string]any{"matches": out})
	})
	mux.HandleFunc("GET /summary", func(w http.ResponseWriter, r *http.Request) {
		summary, err := backing.Summary(r.Context())
		if err != nil {
			h.kegError(w, err)
			return
		}
		h.writeJSON(w, http.StatusOK, summary)
	})

	// Archive
	mux.HandleFunc("GET /archive", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var ids []kegpkg.NodeId
		if raw := strings.TrimSpace(q.Get("nodes")); raw != "" {
			for _, part := range strings.Split(raw, ",") {
				id, err := kegpkg.ParseNode(strings.TrimSpace(part))
				if err != nil {
					h.writeError(w, http.StatusBadRequest, "invalid node id", "BAD_REQUEST")
					return
				}
				ids = append(ids, *id)
			}
		}
		rc, err := backing.ExportNodes(r.Context(), kegpkg.ExportNodesOptions{
			NodeIDs:     ids,
			WithHistory: q.Get("history") == "1",
			WithAssets:  q.Get("assets") == "1",
		})
		if err != nil {
			h.kegError(w, err)
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = io.Copy(w, rc)
	})
	mux.HandleFunc("POST /archive", func(w http.ResponseWriter, r *http.Request) {
		imported, err := backing.ImportNodes(r.Context(), r.Body, kegpkg.ImportNodesOptions{
			AssignNewIDs: r.URL.Query().Get("assign_new_ids") == "1",
		})
		if err != nil {
			h.kegError(w, err)
			return
		}
		type importedWire struct {
			Source string `json:"source"`
			ID     string `json:"id"`
		}
		out := make([]importedWire, len(imported))
		for i, n := range imported {
			out[i] = importedWire{Source: n.SourceID, ID: n.ID.Path()}
		}
		h.writeJSON(w, http.StatusOK, map[string]any{"imported": out})
	})

	// Locks
	mux.HandleFunc("POST /nodes/{id}/lock", func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.parseID(w, r)
		if !ok {
			return
		}
		lock, err := backing.Lock(r.Context(), id)
		if err != nil {
			h.kegError(w, err)
			return
		}
		info, err := backing.LockStatus(r.Context(), id)
		if err != nil {
			info = kegpkg.LockInfo{}
		}
		info.Token = lock.Token
		resp := map[string]any{
			"token":       string(info.Token),
			"ttl_seconds": info.TTLSeconds,
			"holder":      info.Holder,
		}
		if !info.AcquiredAt.IsZero() {
			resp["acquired_at"] = info.AcquiredAt.Format(time.RFC3339)
		}
		h.writeJSON(w, http.StatusOK, resp)
	})
	mux.HandleFunc("GET /nodes/{id}/lock", func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.parseID(w, r)
		if !ok {
			return
		}
		info, err := backing.LockStatus(r.Context(), id)
		if err != nil {
			h.kegError(w, err)
			return
		}
		resp := map[string]any{
			"token":       string(info.Token),
			"ttl_seconds": info.TTLSeconds,
			"holder":      info.Holder,
		}
		if !info.AcquiredAt.IsZero() {
			resp["acquired_at"] = info.AcquiredAt.Format(time.RFC3339)
		}
		h.writeJSON(w, http.StatusOK, resp)
	})
	mux.HandleFunc("DELETE /nodes/{id}/lock", func(w http.ResponseWriter, r *http.Request) {
		id, ok := h.parseID(w, r)
		if !ok {
			return
		}
		if r.URL.Query().Get("force") == "1" {
			if err := backing.ForceUnlock(r.Context(), id); err != nil {
				h.kegError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		token := kegpkg.LockToken(r.Header.Get("X-Lock-Token"))
		if err := backing.Unlock(r.Context(), id, token); err != nil {
			h.kegError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// Wrap with request counting + bearer auth.
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.requests.Add(1)
		if h.token != "" && r.Header.Get("Authorization") != "Bearer "+h.token {
			h.writeError(w, http.StatusUnauthorized, "authentication required", "UNAUTHORIZED")
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(h.srv.Close)
	return h
}

const mockHubToken = "test-token"

func newRemoteKegFixture(t *testing.T) (*sandbox.Sandbox, *mockOpsHub, *kegpkg.RemoteKeg) {
	t.Helper()
	f := NewSandbox(t)
	hub := newMockOpsHub(t, f, mockHubToken)
	rk := kegpkg.NewRemoteKeg(hub.srv.URL, mockHubToken, f.Runtime())
	return f, hub, rk
}

func TestRemoteKegRoundTripBasics(t *testing.T) {
	t.Parallel()
	f, _, rk := newRemoteKegFixture(t)
	ctx := f.Context()

	// Create with tags carries composed content + meta in one request.
	id, err := rk.Create(ctx, &kegpkg.CreateOptions{
		Title: "Gamma node",
		Lead:  "A gamma lead",
		Tags:  []string{"gamma"},
	})
	require.NoError(t, err)
	require.Equal(t, 3, id.ID.ID)

	// ReadNode assembles content/meta/stats in one round trip.
	view, err := rk.ReadNode(ctx, id.ID)
	require.NoError(t, err)
	require.Contains(t, string(view.Content), "Gamma node")
	require.Contains(t, string(view.Meta), "gamma")
	require.NotNil(t, view.Stats)

	// SetContent / GetContent round-trip raw bytes.
	require.NoError(t, rk.SetContent(ctx, id.ID, []byte("# Gamma node\n\nupdated body\n")))
	content, err := rk.GetContent(ctx, id.ID)
	require.NoError(t, err)
	require.Contains(t, string(content), "updated body")

	// Meta survives the round trip with tags applied.
	meta, err := rk.GetMeta(ctx, id.ID)
	require.NoError(t, err)
	require.Contains(t, meta.Tags(), "gamma")
	meta.SetTags([]string{"gamma", "json-transport"})
	require.NoError(t, rk.SetMeta(ctx, id.ID, meta))
	meta, err = rk.GetMeta(ctx, id.ID)
	require.NoError(t, err)
	require.Contains(t, meta.Tags(), "json-transport")

	// Server-side query and grep.
	entries, err := rk.Query(ctx, kegpkg.QueryOptions{Expr: "gamma"})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "3", entries[0].ID)

	matches, err := rk.Grep(ctx, kegpkg.GrepOptions{Pattern: "Alpha body"})
	require.NoError(t, err)
	require.NotEmpty(t, matches)

	// Summary counts the zero node plus the three created nodes.
	summary, err := rk.Summary(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, summary.NodeCount)

	// Dex returns parsed entries built from the wire artifacts.
	dex, err := rk.Dex(ctx)
	require.NoError(t, err)
	nodes := dex.Nodes(ctx)
	require.NotEmpty(t, nodes)
	ids := make([]string, len(nodes))
	for i, n := range nodes {
		ids[i] = n.ID
	}
	require.Contains(t, ids, "3")

	// NodeExists distinguishes present from absent without an error.
	exists, err := rk.NodeExists(ctx, id.ID)
	require.NoError(t, err)
	require.True(t, exists)
	exists, err = rk.NodeExists(ctx, kegpkg.NodeId{ID: 4242})
	require.NoError(t, err)
	require.False(t, exists)

	// ListNodes and Next agree with the backing keg.
	nodeIDs, err := rk.ListNodes(ctx)
	require.NoError(t, err)
	require.Len(t, nodeIDs, 4)
	next, err := rk.Next(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, next.ID)
}

func TestRemoteMutationBatchesPreserveOrderAndAtomicity(t *testing.T) {
	t.Parallel()
	fx, hub, remote := newRemoteKegFixture(t)
	ctx := fx.Context()
	require.NoError(t, hub.backing.CreateSchema(ctx, "note", []byte("type: note\n")))
	require.NoError(t, hub.backing.CreateSchema(ctx, "task", []byte("type: task\n")))

	created, err := remote.CreateNodes(ctx, []kegpkg.NodeCreate{
		{Key: "forward", Schema: "note", Body: []byte("# Forward\n\n[Back](../{{node:back}})\n")},
		{Key: "back", Schema: "note", Body: []byte("# Back\n\n[Forward](../{{node:forward}})\n")},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"forward", "back"}, []string{created[0].Key, created[1].Key})
	forwardBefore, err := remote.ReadNode(ctx, created[0].ID)
	require.NoError(t, err)
	require.Contains(t, string(forwardBefore.Content), fmt.Sprintf("../%d", created[1].ID.ID))
	forwardMeta, err := remote.GetMeta(ctx, created[0].ID)
	require.NoError(t, err)
	forwardType, ok := forwardMeta.Get("type")
	require.True(t, ok)
	require.Equal(t, "note", forwardType)

	_, err = remote.UpdateNodes(ctx, []kegpkg.NodeUpdateOptions{
		{ID: created[0].ID, Schema: "task", Content: []byte("# Changed\n"), HasContent: true, SnapshotBefore: true},
		{ID: created[1].ID, Schema: "task", Content: []byte("# Never\n"), HasContent: true, ExpectedHash: "stale"},
	})
	require.ErrorIs(t, err, kegpkg.ErrConflict)
	forwardAfter, err := remote.ReadNode(ctx, created[0].ID)
	require.NoError(t, err)
	require.Equal(t, forwardBefore.Content, forwardAfter.Content)
	history, err := remote.ListSnapshots(ctx, created[0].ID)
	require.NoError(t, err)
	require.Empty(t, history)
	forwardMeta, err = remote.GetMeta(ctx, created[0].ID)
	require.NoError(t, err)
	forwardType, _ = forwardMeta.Get("type")
	require.Equal(t, "note", forwardType, "failed remote batch changed the stored schema")

	_, err = remote.UpdateNodes(ctx, []kegpkg.NodeUpdateOptions{{
		ID: created[0].ID, Schema: "task", Content: []byte("# Reclassified\n"), HasContent: true,
	}})
	require.NoError(t, err)
	forwardMeta, err = remote.GetMeta(ctx, created[0].ID)
	require.NoError(t, err)
	forwardType, _ = forwardMeta.Get("type")
	require.Equal(t, "task", forwardType)

	snapshots, err := remote.AppendSnapshots(ctx, []kegpkg.NodeSnapshotRequest{
		{ID: created[0].ID, Message: "forward point"},
		{ID: created[1].ID, Message: "back point"},
	})
	require.NoError(t, err)
	require.Equal(t, []kegpkg.NodeId{created[0].ID, created[1].ID}, []kegpkg.NodeId{snapshots[0].Node, snapshots[1].Node})
}

func TestRemoteKegMoveRemoveRewritten(t *testing.T) {
	t.Parallel()
	f, _, rk := newRemoteKegFixture(t)
	ctx := f.Context()

	// Node 1 links to ../2; moving 2 rewrites node 1.
	rewritten, err := rk.Move(ctx, kegpkg.NodeId{ID: 2}, kegpkg.NodeId{ID: 5})
	require.NoError(t, err)
	require.Contains(t, rewritten, kegpkg.NodeId{ID: 1})

	// Removing the moved node rewrites node 1 again (link drop).
	rewritten, err = rk.Remove(ctx, kegpkg.NodeId{ID: 5})
	require.NoError(t, err)
	require.Contains(t, rewritten, kegpkg.NodeId{ID: 1})
}

func TestRemoteKegLocks(t *testing.T) {
	t.Parallel()
	f, _, rk := newRemoteKegFixture(t)
	ctx := f.Context()
	id := kegpkg.NodeId{ID: 1}

	token, err := rk.Lock(ctx, id)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	info, err := rk.LockStatus(ctx, id)
	require.NoError(t, err)
	require.Equal(t, token.Token, info.Token)

	require.NoError(t, rk.Unlock(ctx, id, token.Token))

	info, err = rk.LockStatus(ctx, id)
	require.NoError(t, err)
	require.Empty(t, info.Token)
}

func TestRemoteKegExportImport(t *testing.T) {
	t.Parallel()
	f, _, rk := newRemoteKegFixture(t)
	ctx := f.Context()

	rc, err := rk.ExportNodes(ctx, kegpkg.ExportNodesOptions{})
	require.NoError(t, err)
	defer rc.Close()

	// Land the archive in a second, freshly initialized LocalKeg.
	dst := kegpkg.NewLocalKeg(kegpkg.NewMemoryRepo(f.Runtime()), f.Runtime())
	initNonStrictTestKeg(t, dst, ctx)
	imported, err := dst.ImportNodes(ctx, rc, kegpkg.ImportNodesOptions{})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(imported), 3)

	content, err := dst.GetContent(ctx, kegpkg.NodeId{ID: 1})
	require.NoError(t, err)
	require.Contains(t, string(content), "Alpha body")
}

func TestRemoteKegImportRoundTrip(t *testing.T) {
	t.Parallel()
	f, _, rk := newRemoteKegFixture(t)
	ctx := f.Context()

	// Export a node from a local keg and import it into the remote keg
	// with fresh ids.
	src := kegpkg.NewLocalKeg(kegpkg.NewMemoryRepo(f.Runtime()), f.Runtime())
	initNonStrictTestKeg(t, src, ctx)
	srcID, err := src.Create(ctx, &kegpkg.CreateOptions{
		Title: "Imported node",
		Body:  []byte("# Imported node\n\ntravels by archive"),
	})
	require.NoError(t, err)

	rc, err := src.ExportNodes(ctx, kegpkg.ExportNodesOptions{NodeIDs: []kegpkg.NodeId{srcID.ID}})
	require.NoError(t, err)
	defer rc.Close()

	imported, err := rk.ImportNodes(ctx, rc, kegpkg.ImportNodesOptions{AssignNewIDs: true})
	require.NoError(t, err)
	require.Len(t, imported, 1)

	content, err := rk.GetContent(ctx, imported[0].ID)
	require.NoError(t, err)
	require.Contains(t, string(content), "travels by archive")
}

// TestRemoteKegSingleRoundTrip is the headline guarantee of the RemoteKeg
// refactor: each Keg operation is exactly one HTTP request.
func TestRemoteKegSingleRoundTrip(t *testing.T) {
	t.Parallel()
	f, hub, rk := newRemoteKegFixture(t)
	ctx := f.Context()
	id := kegpkg.NodeId{ID: 1}

	cases := []struct {
		name string
		op   func() error
	}{
		{"ReadNode", func() error {
			_, err := rk.ReadNode(ctx, id)
			return err
		}},
		{"SetContent", func() error {
			return rk.SetContent(ctx, id, []byte("# Alpha node\n\nrewritten\n"))
		}},
		{"Query", func() error {
			_, err := rk.Query(ctx, kegpkg.QueryOptions{Expr: "shared"})
			return err
		}},
		{"Create", func() error {
			_, err := rk.Create(ctx, &kegpkg.CreateOptions{
				Title: "Budget node",
				Tags:  []string{"budget"},
			})
			return err
		}},
		{"Index", func() error {
			return rk.Index(ctx, kegpkg.IndexOptions{})
		}},
	}
	for _, tc := range cases {
		hub.requests.Store(0)
		require.NoError(t, tc.op(), tc.name)
		require.Equal(t, int64(1), hub.requests.Load(),
			"%s must be exactly one HTTP round trip", tc.name)
	}
}

func TestRemoteKegErrorMapping(t *testing.T) {
	t.Parallel()
	f, hub, rk := newRemoteKegFixture(t)
	ctx := f.Context()

	t.Run("missing node maps to ErrNotExist", func(t *testing.T) {
		_, err := rk.ReadNode(ctx, kegpkg.NodeId{ID: 4242})
		require.Error(t, err)
		require.ErrorIs(t, err, kegpkg.ErrNotExist)

		_, err = rk.GetContent(ctx, kegpkg.NodeId{ID: 4242})
		require.ErrorIs(t, err, kegpkg.ErrNotExist)
	})

	t.Run("bad token maps to ErrUnauthorized", func(t *testing.T) {
		bad := kegpkg.NewRemoteKeg(hub.srv.URL, "wrong-token", f.Runtime())
		_, err := bad.ReadNode(ctx, kegpkg.NodeId{ID: 1})
		require.ErrorIs(t, err, kegpkg.ErrUnauthorized)
	})

	t.Run("Init is not supported remotely", func(t *testing.T) {
		require.ErrorIs(t, rk.Init(ctx), kegpkg.ErrNotSupported)
	})
}
