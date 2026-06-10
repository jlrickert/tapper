package keg_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// mockHub is a minimal in-memory mock of the tapper-hub REST API, sufficient
// for unit-testing ApiRepo's HTTP client logic. It stores nodes as
// map[int]nodeData and indexes as map[string][]byte.
type mockHub struct {
	mu     sync.Mutex
	nodes  map[int]*mockNode
	snaps  map[int][]mockSnapshot
	config *keg.Config
	idxs   map[string][]byte
	nextID int

	// authToken is the expected bearer token. Empty means no auth check.
	authToken string

	// locks tracks which node IDs are currently locked and by which token.
	locks map[int]string
}

type mockNode struct {
	content []byte
	meta    []byte
	stats   []byte
	files   map[string][]byte
	images  map[string][]byte
}

type mockSnapshot struct {
	snap    keg.Snapshot
	content []byte
	meta    []byte
	stats   []byte
}

func newMockHub() *mockHub {
	return &mockHub{
		nodes:  make(map[int]*mockNode),
		snaps:  make(map[int][]mockSnapshot),
		idxs:   make(map[string][]byte),
		locks:  make(map[int]string),
		nextID: 0,
	}
}

func (h *mockHub) ensureNode(id int) *mockNode {
	if n, ok := h.nodes[id]; ok {
		return n
	}
	n := &mockNode{
		files:  make(map[string][]byte),
		images: make(map[string][]byte),
	}
	h.nodes[id] = n
	return n
}

func (h *mockHub) handler() http.Handler {
	mux := http.NewServeMux()

	// Auth middleware helper.
	checkAuth := func(w http.ResponseWriter, r *http.Request) bool {
		if h.authToken == "" {
			return true
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+h.authToken {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized", "code": "UNAUTHORIZED"})
			return false
		}
		return true
	}

	// POST /nodes — reserve the next id and, when a payload is supplied,
	// persist the node's content/meta/stats in the same call. A bare POST
	// (empty body) just reserves the id, as Next() does.
	mux.HandleFunc("POST /nodes", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Content *string         `json:"content"`
			Meta    *string         `json:"meta"`
			Stats   json.RawMessage `json:"stats"`
		}
		if len(bytes.TrimSpace(body)) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
				return
			}
		}
		h.mu.Lock()
		id := h.nextID
		n := h.ensureNode(id)
		if req.Content != nil {
			n.content = []byte(*req.Content)
		}
		if req.Meta != nil {
			n.meta = []byte(*req.Meta)
		}
		if len(bytes.TrimSpace(req.Stats)) > 0 {
			n.stats = req.Stats
		}
		h.nextID++
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]int{"id": id})
	})

	// GET /nodes
	mux.HandleFunc("GET /nodes", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		h.mu.Lock()
		ids := make([]int, 0, len(h.nodes))
		for id := range h.nodes {
			ids = append(ids, id)
		}
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ids)
	})

	// HEAD /nodes/{id} and DELETE /nodes/{id}
	mux.HandleFunc("/nodes/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		if id < 0 {
			http.NotFound(w, r)
			return
		}

		switch r.Method {
		case http.MethodHead:
			h.mu.Lock()
			_, ok := h.nodes[id]
			h.mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			h.mu.Lock()
			if _, ok := h.nodes[id]; !ok {
				h.mu.Unlock()
				writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "node not found")
				return
			}
			delete(h.nodes, id)
			delete(h.snaps, id)
			h.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// POST /nodes/{id}/move
	mux.HandleFunc("POST /nodes/{id}/move", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		if id < 0 {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Dst int `json:"dst"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		src, ok := h.nodes[id]
		if !ok {
			writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "source not found")
			return
		}
		if _, exists := h.nodes[body.Dst]; exists {
			writeJSONError(w, http.StatusConflict, "CONFLICT", "destination exists")
			return
		}
		h.nodes[body.Dst] = src
		if snaps, ok := h.snaps[id]; ok {
			h.snaps[body.Dst] = snaps
			delete(h.snaps, id)
		}
		delete(h.nodes, id)
		w.WriteHeader(http.StatusNoContent)
	})

	// Content endpoints: GET/PUT /nodes/{id}/content
	mux.HandleFunc("/nodes/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		if id < 0 {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			h.mu.Lock()
			n, ok := h.nodes[id]
			h.mu.Unlock()
			if !ok || n.content == nil {
				writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "content not found")
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(n.content)
		case http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			h.mu.Lock()
			n := h.ensureNode(id)
			n.content = data
			h.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// Meta endpoints: GET/PUT /nodes/{id}/meta
	mux.HandleFunc("/nodes/{id}/meta", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		if id < 0 {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			h.mu.Lock()
			n, ok := h.nodes[id]
			h.mu.Unlock()
			if !ok || n.meta == nil {
				writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "meta not found")
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(n.meta)
		case http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			h.mu.Lock()
			n := h.ensureNode(id)
			n.meta = data
			h.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// Stats endpoints: GET/PUT /nodes/{id}/stats
	mux.HandleFunc("/nodes/{id}/stats", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		if id < 0 {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			h.mu.Lock()
			n, ok := h.nodes[id]
			h.mu.Unlock()
			if !ok || n.stats == nil {
				writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "stats not found")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(n.stats)
		case http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			h.mu.Lock()
			n := h.ensureNode(id)
			n.stats = data
			h.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// Snapshot endpoints
	mux.HandleFunc("GET /nodes/{id}/snapshots", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		if id < 0 {
			http.NotFound(w, r)
			return
		}
		h.mu.Lock()
		snaps := append([]mockSnapshot(nil), h.snaps[id]...)
		h.mu.Unlock()

		result := make([]map[string]any, len(snaps))
		for i, s := range snaps {
			result[i] = snapshotMap(s.snap)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	mux.HandleFunc("POST /nodes/{id}/snapshots", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		if id < 0 {
			http.NotFound(w, r)
			return
		}
		var req struct {
			ExpectedParent int64           `json:"expected_parent"`
			Message        string          `json:"message"`
			CreatedAt      string          `json:"created_at"`
			Meta           []byte          `json:"meta"`
			Stats          json.RawMessage `json:"stats"`
			Content        struct {
				Kind string `json:"kind"`
				Data []byte `json:"data"`
				Hash string `json:"hash"`
			} `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body")
			return
		}
		if req.Content.Kind != "" && req.Content.Kind != string(keg.SnapshotContentKindFull) {
			writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "unsupported snapshot content kind")
			return
		}

		createdAt := time.Now().UTC()
		if strings.TrimSpace(req.CreatedAt) != "" {
			parsed, err := time.Parse(time.RFC3339, req.CreatedAt)
			if err != nil {
				writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid created_at")
				return
			}
			createdAt = parsed
		}

		h.mu.Lock()
		defer h.mu.Unlock()
		if _, ok := h.nodes[id]; !ok {
			writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "node not found")
			return
		}
		existing := h.snaps[id]
		latest := int64(0)
		if len(existing) > 0 {
			latest = int64(existing[len(existing)-1].snap.ID)
		}
		if req.ExpectedParent != latest {
			writeJSONError(w, http.StatusConflict, "CONFLICT", "snapshot parent mismatch")
			return
		}
		snap := keg.Snapshot{
			ID:           keg.RevisionID(latest + 1),
			Node:         keg.NodeId{ID: id},
			Parent:       keg.RevisionID(latest),
			CreatedAt:    createdAt,
			Message:      req.Message,
			ContentHash:  req.Content.Hash,
			IsCheckpoint: true,
		}
		if len(req.Meta) > 0 {
			snap.MetaHash = "meta"
		}
		if len(req.Stats) > 0 {
			snap.StatsHash = "stats"
		}
		h.snaps[id] = append(existing, mockSnapshot{
			snap:    snap,
			content: append([]byte(nil), req.Content.Data...),
			meta:    append([]byte(nil), req.Meta...),
			stats:   append([]byte(nil), req.Stats...),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(snapshotMap(snap))
	})

	mux.HandleFunc("GET /nodes/{id}/snapshots/{rev}", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		rev := parseID(r.PathValue("rev"))
		if id < 0 || rev <= 0 {
			http.NotFound(w, r)
			return
		}
		h.mu.Lock()
		snap, ok := h.findSnapshotLocked(id, keg.RevisionID(rev))
		h.mu.Unlock()
		if !ok {
			writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "snapshot not found")
			return
		}
		result := snapshotMap(snap.snap)
		if r.URL.Query().Get("resolve_content") == "true" {
			result["content"] = snap.content
			result["meta"] = snap.meta
			if len(bytes.TrimSpace(snap.stats)) > 0 {
				result["stats"] = json.RawMessage(snap.stats)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	mux.HandleFunc("GET /nodes/{id}/snapshots/{rev}/content", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		rev := parseID(r.PathValue("rev"))
		if id < 0 || rev <= 0 {
			http.NotFound(w, r)
			return
		}
		h.mu.Lock()
		snap, ok := h.findSnapshotLocked(id, keg.RevisionID(rev))
		h.mu.Unlock()
		if !ok {
			writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "snapshot not found")
			return
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write(snap.content)
	})

	mux.HandleFunc("POST /nodes/{id}/snapshots/{rev}/restore", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		rev := parseID(r.PathValue("rev"))
		if id < 0 || rev <= 0 {
			http.NotFound(w, r)
			return
		}
		var req struct {
			CreateRestoreSnapshot bool `json:"create_restore_snapshot"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		h.mu.Lock()
		snap, ok := h.findSnapshotLocked(id, keg.RevisionID(rev))
		if !ok {
			h.mu.Unlock()
			writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "snapshot not found")
			return
		}
		n := h.ensureNode(id)
		n.content = append([]byte(nil), snap.content...)
		n.meta = append([]byte(nil), snap.meta...)
		n.stats = append([]byte(nil), snap.stats...)
		if req.CreateRestoreSnapshot {
			existing := h.snaps[id]
			latest := int64(0)
			if len(existing) > 0 {
				latest = int64(existing[len(existing)-1].snap.ID)
			}
			restoreSnap := keg.Snapshot{
				ID:           keg.RevisionID(latest + 1),
				Node:         keg.NodeId{ID: id},
				Parent:       keg.RevisionID(latest),
				CreatedAt:    time.Now().UTC(),
				Message:      "restore from rev " + strconv.Itoa(rev),
				ContentHash:  snap.snap.ContentHash,
				MetaHash:     snap.snap.MetaHash,
				StatsHash:    snap.snap.StatsHash,
				IsCheckpoint: true,
			}
			h.snaps[id] = append(existing, mockSnapshot{
				snap:    restoreSnap,
				content: append([]byte(nil), snap.content...),
				meta:    append([]byte(nil), snap.meta...),
				stats:   append([]byte(nil), snap.stats...),
			})
		}
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	// Index endpoints
	mux.HandleFunc("GET /indexes", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		h.mu.Lock()
		names := make([]string, 0, len(h.idxs))
		for name := range h.idxs {
			names = append(names, name)
		}
		h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(names)
	})

	mux.HandleFunc("DELETE /indexes", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		h.mu.Lock()
		h.idxs = make(map[string][]byte)
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("/indexes/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		name := r.PathValue("name")
		switch r.Method {
		case http.MethodGet:
			h.mu.Lock()
			data, ok := h.idxs[name]
			h.mu.Unlock()
			if !ok {
				writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "index not found")
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(data)
		case http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			h.mu.Lock()
			h.idxs[name] = data
			h.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// Config endpoints
	mux.HandleFunc("/config", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		switch r.Method {
		case http.MethodGet:
			h.mu.Lock()
			cfg := h.config
			h.mu.Unlock()
			if cfg == nil {
				writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "config not found")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(cfg)
		case http.MethodPut:
			var cfg keg.Config
			if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
				writeJSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
				return
			}
			h.mu.Lock()
			h.config = &cfg
			h.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// Lock endpoints: POST /nodes/{id}/lock, DELETE /nodes/{id}/lock
	mux.HandleFunc("/nodes/{id}/lock", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		if id < 0 {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodPost:
			h.mu.Lock()
			if _, locked := h.locks[id]; locked {
				h.mu.Unlock()
				writeJSONError(w, http.StatusConflict, "CONFLICT", "already locked")
				return
			}
			token := "test-lock-token-" + strconv.Itoa(id)
			h.locks[id] = token
			h.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"token": token, "ttl": 30})
		case http.MethodDelete:
			lockToken := r.Header.Get("X-Lock-Token")
			h.mu.Lock()
			held, ok := h.locks[id]
			if !ok {
				h.mu.Unlock()
				w.WriteHeader(http.StatusNoContent)
				return
			}
			if held != lockToken {
				h.mu.Unlock()
				writeJSONError(w, http.StatusConflict, "CONFLICT", "wrong lock token")
				return
			}
			delete(h.locks, id)
			h.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// Lock renew: POST /nodes/{id}/lock/renew
	mux.HandleFunc("POST /nodes/{id}/lock/renew", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		if id < 0 {
			http.NotFound(w, r)
			return
		}
		lockToken := r.Header.Get("X-Lock-Token")
		h.mu.Lock()
		held, ok := h.locks[id]
		h.mu.Unlock()
		if !ok || held != lockToken {
			writeJSONError(w, http.StatusConflict, "CONFLICT", "lock not held or wrong token")
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// File endpoints
	mux.HandleFunc("GET /nodes/{id}/files", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		if id < 0 {
			http.NotFound(w, r)
			return
		}
		h.mu.Lock()
		n, ok := h.nodes[id]
		h.mu.Unlock()
		if !ok {
			writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "node not found")
			return
		}
		names := make([]string, 0, len(n.files))
		for name := range n.files {
			names = append(names, name)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(names)
	})

	mux.HandleFunc("/nodes/{id}/files/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		name := r.PathValue("name")
		if id < 0 {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			h.mu.Lock()
			n, ok := h.nodes[id]
			h.mu.Unlock()
			if !ok {
				writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "node not found")
				return
			}
			data, exists := n.files[name]
			if !exists {
				writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "file not found")
				return
			}
			w.Write(data)
		case http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			h.mu.Lock()
			n := h.ensureNode(id)
			n.files[name] = data
			h.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			h.mu.Lock()
			n, ok := h.nodes[id]
			if !ok {
				h.mu.Unlock()
				writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "node not found")
				return
			}
			if _, exists := n.files[name]; !exists {
				h.mu.Unlock()
				writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "file not found")
				return
			}
			delete(n.files, name)
			h.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	// Image endpoints
	mux.HandleFunc("GET /nodes/{id}/images", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		if id < 0 {
			http.NotFound(w, r)
			return
		}
		h.mu.Lock()
		n, ok := h.nodes[id]
		h.mu.Unlock()
		if !ok {
			writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "node not found")
			return
		}
		names := make([]string, 0, len(n.images))
		for name := range n.images {
			names = append(names, name)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(names)
	})

	mux.HandleFunc("/nodes/{id}/images/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(w, r) {
			return
		}
		id := parseID(r.PathValue("id"))
		name := r.PathValue("name")
		if id < 0 {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			h.mu.Lock()
			n, ok := h.nodes[id]
			h.mu.Unlock()
			if !ok {
				writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "node not found")
				return
			}
			data, exists := n.images[name]
			if !exists {
				writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "image not found")
				return
			}
			w.Write(data)
		case http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			h.mu.Lock()
			n := h.ensureNode(id)
			n.images[name] = data
			h.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			h.mu.Lock()
			n, ok := h.nodes[id]
			if !ok {
				h.mu.Unlock()
				writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "node not found")
				return
			}
			if _, exists := n.images[name]; !exists {
				h.mu.Unlock()
				writeJSONError(w, http.StatusNotFound, "NOT_FOUND", "image not found")
				return
			}
			delete(n.images, name)
			h.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	return mux
}

func parseID(s string) int {
	id, err := strconv.Atoi(s)
	if err != nil {
		return -1
	}
	return id
}

func (h *mockHub) findSnapshotLocked(id int, rev keg.RevisionID) (mockSnapshot, bool) {
	for _, snap := range h.snaps[id] {
		if snap.snap.ID == rev {
			return snap, true
		}
	}
	return mockSnapshot{}, false
}

func snapshotMap(s keg.Snapshot) map[string]any {
	return map[string]any{
		"id":            int64(s.ID),
		"node":          s.Node.ID,
		"parent":        int64(s.Parent),
		"created_at":    s.CreatedAt.Format(time.RFC3339),
		"message":       s.Message,
		"content_hash":  s.ContentHash,
		"meta_hash":     s.MetaHash,
		"stats_hash":    s.StatsHash,
		"is_checkpoint": s.IsCheckpoint,
	}
}

func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": code})
}

// --- Test helpers ---

func setupApiRepo(t *testing.T) (*keg.ApiRepo, *mockHub, *httptest.Server) {
	t.Helper()
	hub := newMockHub()
	srv := httptest.NewServer(hub.handler())
	t.Cleanup(srv.Close)
	repo := keg.NewApiRepo(srv.URL, "")
	return repo, hub, srv
}

func setupApiRepoWithAuth(t *testing.T, token string) (*keg.ApiRepo, *mockHub, *httptest.Server) {
	t.Helper()
	hub := newMockHub()
	hub.authToken = token
	srv := httptest.NewServer(hub.handler())
	t.Cleanup(srv.Close)
	repo := keg.NewApiRepo(srv.URL, token)
	return repo, hub, srv
}

// --- Tests ---

func TestApiRepo_Name(t *testing.T) {
	repo := keg.NewApiRepo("http://example.com", "")
	require.Equal(t, "api", repo.Name())
}

func TestApiRepo_HasNode(t *testing.T) {
	repo, hub, _ := setupApiRepo(t)
	ctx := context.Background()

	// Node does not exist.
	exists, err := repo.HasNode(ctx, keg.NodeId{ID: 0})
	require.NoError(t, err)
	require.False(t, exists)

	// Create a node.
	hub.mu.Lock()
	hub.ensureNode(0)
	hub.mu.Unlock()

	exists, err = repo.HasNode(ctx, keg.NodeId{ID: 0})
	require.NoError(t, err)
	require.True(t, exists)
}

func TestApiRepo_Next(t *testing.T) {
	repo, _, _ := setupApiRepo(t)
	ctx := context.Background()

	id1, err := repo.Next(ctx)
	require.NoError(t, err)
	require.Equal(t, 0, id1.ID)

	id2, err := repo.Next(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, id2.ID)
}

func TestApiRepo_CreateNode(t *testing.T) {
	repo, _, _ := setupApiRepo(t)
	ctx := context.Background()

	creator, ok := any(repo).(keg.RepositoryNodeCreator)
	require.True(t, ok, "ApiRepo must implement RepositoryNodeCreator")

	id, err := creator.CreateNode(ctx, keg.NodeCreate{Content: []byte("# Created\n")})
	require.NoError(t, err)
	require.Equal(t, 0, id.ID)

	// The content persisted by the single create call reads back.
	got, err := repo.ReadContent(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "# Created\n", string(got))

	// A second create reserves the next id.
	id2, err := creator.CreateNode(ctx, keg.NodeCreate{Content: []byte("# Second\n")})
	require.NoError(t, err)
	require.Equal(t, 1, id2.ID)
}

func TestApiRepo_ListNodes(t *testing.T) {
	repo, hub, _ := setupApiRepo(t)
	ctx := context.Background()

	hub.mu.Lock()
	hub.ensureNode(0)
	hub.ensureNode(5)
	hub.ensureNode(10)
	hub.mu.Unlock()

	ids, err := repo.ListNodes(ctx)
	require.NoError(t, err)
	require.Len(t, ids, 3)
}

func TestApiRepo_ContentReadWrite(t *testing.T) {
	repo, hub, _ := setupApiRepo(t)
	ctx := context.Background()

	hub.mu.Lock()
	hub.ensureNode(1)
	hub.mu.Unlock()

	// Write content.
	content := []byte("# Hello World\n\nThis is a test.")
	err := repo.WriteContent(ctx, keg.NodeId{ID: 1}, content)
	require.NoError(t, err)

	// Read content back.
	got, err := repo.ReadContent(ctx, keg.NodeId{ID: 1})
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestApiRepo_Snapshots(t *testing.T) {
	repo, hub, _ := setupApiRepo(t)
	ctx := context.Background()

	hub.mu.Lock()
	n := hub.ensureNode(1)
	n.content = []byte("# Current\n")
	n.meta = []byte("tags: []\n")
	n.stats = []byte(`{"title":"Current"}`)
	hub.mu.Unlock()

	snapRepo, ok := any(repo).(keg.RepositorySnapshots)
	require.True(t, ok, "ApiRepo must implement RepositorySnapshots")

	stats, err := keg.ParseStats(ctx, []byte(`{"title":"Historical"}`))
	require.NoError(t, err)
	snap, err := snapRepo.AppendSnapshot(ctx, keg.NodeId{ID: 1}, keg.SnapshotWrite{
		ExpectedParent: 0,
		Message:        "before change",
		Meta:           []byte("tags:\n- old\n"),
		Stats:          stats,
		Content: keg.SnapshotContentWrite{
			Kind: keg.SnapshotContentKindFull,
			Data: []byte("# Historical\n"),
			Hash: "abc123",
		},
	})
	require.NoError(t, err)
	require.Equal(t, keg.RevisionID(1), snap.ID)

	history, err := snapRepo.ListSnapshots(ctx, keg.NodeId{ID: 1})
	require.NoError(t, err)
	require.Len(t, history, 1)
	require.Equal(t, "before change", history[0].Message)

	content, err := snapRepo.ReadContentAt(ctx, keg.NodeId{ID: 1}, 1)
	require.NoError(t, err)
	require.Equal(t, "# Historical\n", string(content))

	loaded, loadedContent, loadedMeta, loadedStats, err := snapRepo.GetSnapshot(ctx, keg.NodeId{ID: 1}, 1, keg.SnapshotReadOptions{ResolveContent: true})
	require.NoError(t, err)
	require.Equal(t, snap.ID, loaded.ID)
	require.Equal(t, "# Historical\n", string(loadedContent))
	require.Equal(t, "tags:\n- old\n", string(loadedMeta))
	require.Equal(t, "Historical", loadedStats.Title())

	err = snapRepo.RestoreSnapshot(ctx, keg.NodeId{ID: 1}, 1, true)
	require.NoError(t, err)
	current, err := repo.ReadContent(ctx, keg.NodeId{ID: 1})
	require.NoError(t, err)
	require.Equal(t, "# Historical\n", string(current))

	history, err = snapRepo.ListSnapshots(ctx, keg.NodeId{ID: 1})
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.Contains(t, history[1].Message, "restore from rev 1")
}

func TestApiRepo_MetaReadWrite(t *testing.T) {
	repo, hub, _ := setupApiRepo(t)
	ctx := context.Background()

	hub.mu.Lock()
	hub.ensureNode(2)
	hub.mu.Unlock()

	meta := []byte("tags:\n  - test\n  - golang\n")
	err := repo.WriteMeta(ctx, keg.NodeId{ID: 2}, meta)
	require.NoError(t, err)

	got, err := repo.ReadMeta(ctx, keg.NodeId{ID: 2})
	require.NoError(t, err)
	require.Equal(t, meta, got)
}

func TestApiRepo_StatsReadWrite(t *testing.T) {
	repo, hub, _ := setupApiRepo(t)
	ctx := context.Background()

	hub.mu.Lock()
	hub.ensureNode(3)
	hub.mu.Unlock()

	now, _ := time.Parse(time.RFC3339, "2026-03-12T12:00:00Z")
	stats := keg.NewStats(now)
	stats.SetTitle("Test Node")
	stats.SetHash("abc123", &now)

	err := repo.WriteStats(ctx, keg.NodeId{ID: 3}, stats)
	require.NoError(t, err)

	got, err := repo.ReadStats(ctx, keg.NodeId{ID: 3})
	require.NoError(t, err)
	require.Equal(t, "Test Node", got.Title())
	require.Equal(t, "abc123", got.Hash())
}

func TestApiRepo_MoveNode(t *testing.T) {
	repo, hub, _ := setupApiRepo(t)
	ctx := context.Background()

	hub.mu.Lock()
	n := hub.ensureNode(1)
	n.content = []byte("hello")
	hub.mu.Unlock()

	err := repo.MoveNode(ctx, keg.NodeId{ID: 1}, keg.NodeId{ID: 99})
	require.NoError(t, err)

	// Old node gone.
	exists, _ := repo.HasNode(ctx, keg.NodeId{ID: 1})
	require.False(t, exists)

	// New node exists.
	exists, _ = repo.HasNode(ctx, keg.NodeId{ID: 99})
	require.True(t, exists)
}

func TestApiRepo_DeleteNode(t *testing.T) {
	repo, hub, _ := setupApiRepo(t)
	ctx := context.Background()

	hub.mu.Lock()
	hub.ensureNode(5)
	hub.mu.Unlock()

	err := repo.DeleteNode(ctx, keg.NodeId{ID: 5})
	require.NoError(t, err)

	exists, _ := repo.HasNode(ctx, keg.NodeId{ID: 5})
	require.False(t, exists)
}

func TestApiRepo_DeleteNode_NotExist(t *testing.T) {
	repo, _, _ := setupApiRepo(t)
	ctx := context.Background()

	err := repo.DeleteNode(ctx, keg.NodeId{ID: 999})
	require.Error(t, err)
	require.True(t, errors.Is(err, keg.ErrNotExist))
}

// TestApiRepo_NotFoundError_IncludesRequestURL verifies the 404 error names the
// exact request URL (method + path), so a caller (and the user) can see which
// hub/namespace/keg/node was actually read — not just a bare "not found".
func TestApiRepo_NotFoundError_IncludesRequestURL(t *testing.T) {
	repo, _, srv := setupApiRepo(t)
	ctx := context.Background()

	_, err := repo.ReadContent(ctx, keg.NodeId{ID: 0})
	require.Error(t, err)
	require.True(t, errors.Is(err, keg.ErrNotExist))
	require.Contains(t, err.Error(), srv.URL+"/nodes/0/content",
		"error should include the full request URL that 404'd")
	require.Contains(t, err.Error(), http.MethodGet)
}

func TestApiRepo_IndexReadWrite(t *testing.T) {
	repo, _, _ := setupApiRepo(t)
	ctx := context.Background()

	data := []byte("0\t2026-03-12\tZero node\n")
	err := repo.WriteIndex(ctx, "nodes.tsv", data)
	require.NoError(t, err)

	got, err := repo.GetIndex(ctx, "nodes.tsv")
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestApiRepo_ListIndexes(t *testing.T) {
	repo, _, _ := setupApiRepo(t)
	ctx := context.Background()

	_ = repo.WriteIndex(ctx, "nodes.tsv", []byte("data"))
	_ = repo.WriteIndex(ctx, "tags", []byte("data"))

	names, err := repo.ListIndexes(ctx)
	require.NoError(t, err)
	require.Len(t, names, 2)
}

func TestApiRepo_ClearIndexes(t *testing.T) {
	repo, _, _ := setupApiRepo(t)
	ctx := context.Background()

	_ = repo.WriteIndex(ctx, "nodes.tsv", []byte("data"))
	err := repo.ClearIndexes(ctx)
	require.NoError(t, err)

	names, err := repo.ListIndexes(ctx)
	require.NoError(t, err)
	require.Empty(t, names)
}

func TestApiRepo_ConfigReadWrite(t *testing.T) {
	repo, _, _ := setupApiRepo(t)
	ctx := context.Background()

	cfg := keg.NewConfig()
	cfg.Title = "Test Keg"

	err := repo.WriteConfig(ctx, cfg)
	require.NoError(t, err)

	got, err := repo.ReadConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, "Test Keg", got.Title)
}

func TestApiRepo_ConfigNotFound(t *testing.T) {
	repo, _, _ := setupApiRepo(t)
	ctx := context.Background()

	_, err := repo.ReadConfig(ctx)
	require.Error(t, err)
	require.True(t, errors.Is(err, keg.ErrNotExist))
}

func TestApiRepo_WithNodeLock(t *testing.T) {
	repo, hub, _ := setupApiRepo(t)
	ctx := context.Background()

	hub.mu.Lock()
	hub.ensureNode(1)
	hub.mu.Unlock()

	called := false
	err := repo.WithNodeLock(ctx, keg.NodeId{ID: 1}, func(ctx context.Context) error {
		called = true

		// Verify the lock is held on the server.
		hub.mu.Lock()
		_, locked := hub.locks[1]
		hub.mu.Unlock()
		require.True(t, locked, "lock should be held during callback")

		return nil
	})
	require.NoError(t, err)
	require.True(t, called)

	// Verify lock was released after callback.
	hub.mu.Lock()
	_, locked := hub.locks[1]
	hub.mu.Unlock()
	require.False(t, locked, "lock should be released after callback")
}

func TestApiRepo_WithNodeLock_NilFn(t *testing.T) {
	repo, _, _ := setupApiRepo(t)
	ctx := context.Background()

	err := repo.WithNodeLock(ctx, keg.NodeId{ID: 1}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fn required")
}

func TestApiRepo_Auth_BearerTokenSent(t *testing.T) {
	token := "test-secret-token"
	repo, hub, _ := setupApiRepoWithAuth(t, token)
	ctx := context.Background()

	hub.mu.Lock()
	hub.ensureNode(0)
	hub.mu.Unlock()

	// With correct token, request should succeed.
	exists, err := repo.HasNode(ctx, keg.NodeId{ID: 0})
	require.NoError(t, err)
	require.True(t, exists)
}

func TestApiRepo_Auth_Unauthorized(t *testing.T) {
	hub := newMockHub()
	hub.authToken = "correct-token"
	srv := httptest.NewServer(hub.handler())
	t.Cleanup(srv.Close)

	// Use wrong token.
	repo := keg.NewApiRepo(srv.URL, "wrong-token")
	ctx := context.Background()

	hub.mu.Lock()
	hub.ensureNode(0)
	hub.mu.Unlock()

	_, err := repo.ReadContent(ctx, keg.NodeId{ID: 0})
	require.Error(t, err)
	require.True(t, errors.Is(err, keg.ErrUnauthorized))
}

func TestApiRepo_ErrorMapping_NotFound(t *testing.T) {
	repo, _, _ := setupApiRepo(t)
	ctx := context.Background()

	_, err := repo.ReadContent(ctx, keg.NodeId{ID: 999})
	require.Error(t, err)
	require.True(t, errors.Is(err, keg.ErrNotExist))
}

func TestApiRepo_FileReadWrite(t *testing.T) {
	repo, hub, _ := setupApiRepo(t)
	ctx := context.Background()

	hub.mu.Lock()
	hub.ensureNode(1)
	hub.mu.Unlock()

	data := []byte("file content here")
	err := repo.WriteFile(ctx, keg.NodeId{ID: 1}, "test.txt", data)
	require.NoError(t, err)

	got, err := repo.ReadFile(ctx, keg.NodeId{ID: 1}, "test.txt")
	require.NoError(t, err)
	require.Equal(t, data, got)

	names, err := repo.ListFiles(ctx, keg.NodeId{ID: 1})
	require.NoError(t, err)
	require.Contains(t, names, "test.txt")

	err = repo.DeleteFile(ctx, keg.NodeId{ID: 1}, "test.txt")
	require.NoError(t, err)

	_, err = repo.ReadFile(ctx, keg.NodeId{ID: 1}, "test.txt")
	require.Error(t, err)
	require.True(t, errors.Is(err, keg.ErrNotExist))
}

func TestApiRepo_ImageReadWrite(t *testing.T) {
	repo, hub, _ := setupApiRepo(t)
	ctx := context.Background()

	hub.mu.Lock()
	hub.ensureNode(2)
	hub.mu.Unlock()

	data := []byte("PNG image bytes")
	err := repo.WriteImage(ctx, keg.NodeId{ID: 2}, "photo.png", data)
	require.NoError(t, err)

	got, err := repo.ReadImage(ctx, keg.NodeId{ID: 2}, "photo.png")
	require.NoError(t, err)
	require.Equal(t, data, got)

	names, err := repo.ListImages(ctx, keg.NodeId{ID: 2})
	require.NoError(t, err)
	require.Contains(t, names, "photo.png")

	err = repo.DeleteImage(ctx, keg.NodeId{ID: 2}, "photo.png")
	require.NoError(t, err)

	_, err = repo.ReadImage(ctx, keg.NodeId{ID: 2}, "photo.png")
	require.Error(t, err)
	require.True(t, errors.Is(err, keg.ErrNotExist))
}

func TestApiRepo_CompileTimeInterfaceChecks(t *testing.T) {
	// These are compile-time assertions in repo_api.go, but we verify the
	// types here as well for documentation.
	var _ keg.Repository = (*keg.ApiRepo)(nil)
	var _ keg.RepositoryFiles = (*keg.ApiRepo)(nil)
	var _ keg.RepositoryImages = (*keg.ApiRepo)(nil)
	var _ keg.RepositorySnapshots = (*keg.ApiRepo)(nil)
}

// Suppress unused import warnings -- strings is used by the mock server
// route patterns via http.MethodHead etc.
var _ = strings.TrimSpace
