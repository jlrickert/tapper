package keg

import (
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryRepo is an in-memory implementation of KegRepository intended for
// tests and simple tooling that doesn't require persistence.
type MemoryRepo struct {
	mu sync.RWMutex
	// nodes stores per-node data
	nodes map[NodeID]*memoryNode
	// indexes stores raw index files by name (for example: "nodes.tsv")
	indexes map[string][]byte
	// config holds the in-memory Config if written
	config *Config
}

type memoryNode struct {
	content []byte
	meta    []byte
	items   map[string][]byte
	images  map[string][]byte
	stats   NodeStats
	title   string // cached title parsed from meta
}

func NewMemoryRepo() *MemoryRepo {
	return &MemoryRepo{
		nodes:   make(map[NodeID]*memoryNode),
		indexes: make(map[string][]byte),
	}
}

// ensureNode returns existing node or creates one if absent.
// Caller must hold repo.mu (at least write).
func (r *MemoryRepo) ensureNode(id NodeID) *memoryNode {
	n, ok := r.nodes[id]
	if !ok {
		now := time.Now().UTC()
		n = &memoryNode{
			items:  make(map[string][]byte),
			images: make(map[string][]byte),
			stats: NodeStats{
				Created: now,
				Updated: now,
				Access:  now,
			},
		}
		r.nodes[id] = n
	}
	return n
}

func (r *MemoryRepo) ReadContent(id NodeID) ([]byte, error) {
	r.mu.RLock()
	n, ok := r.nodes[id]
	r.mu.RUnlock()
	if !ok {
		return nil, NewNodeNotFoundError(id)
	}
	r.mu.Lock()
	n.stats.Access = time.Now().UTC()
	r.mu.Unlock()
	if n.content == nil {
		// return empty content rather than ErrMetaNotFound; content may be absent
		return nil, nil
	}
	// return a copy
	cp := make([]byte, len(n.content))
	copy(cp, n.content)
	return cp, nil
}

func (r *MemoryRepo) ReadMeta(id NodeID) ([]byte, error) {
	r.mu.RLock()
	n, ok := r.nodes[id]
	r.mu.RUnlock()
	if !ok {
		return nil, NewNodeNotFoundError(id)
	}
	r.mu.Lock()
	n.stats.Access = time.Now().UTC()
	r.mu.Unlock()
	if n.meta == nil {
		return nil, ErrMetaNotFound
	}
	cp := make([]byte, len(n.meta))
	copy(cp, n.meta)
	return cp, nil
}

func (r *MemoryRepo) Stats(id NodeID) (NodeStats, error) {
	r.mu.RLock()
	n, ok := r.nodes[id]
	r.mu.RUnlock()
	if !ok {
		return NodeStats{}, NewNodeNotFoundError(id)
	}
	r.mu.RLock()
	stats := n.stats
	r.mu.RUnlock()
	return stats, nil
}

func (r *MemoryRepo) ListIndexes() ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.indexes))
	for k := range r.indexes {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}

func (r *MemoryRepo) ListNodesID() ([]NodeID, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]NodeID, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

func (r *MemoryRepo) ListNodes() ([]NodeRef, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	re := make([]NodeRef, 0, len(r.nodes))
	for id, n := range r.nodes {
		re = append(re, NodeRef{
			ID:      id,
			Updated: n.stats.Updated,
			Title:   n.title,
		})
	}
	// Sort by numeric id ascending for determinism
	sort.Slice(re, func(i, j int) bool { return re[i].ID < re[j].ID })
	return re, nil
}

func (r *MemoryRepo) ListItems(id NodeID) ([]string, error) {
	r.mu.RLock()
	n, ok := r.nodes[id]
	r.mu.RUnlock()
	if !ok {
		return nil, NewNodeNotFoundError(id)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(n.items))
	for k := range n.items {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}

func (r *MemoryRepo) ListImages(id NodeID) ([]string, error) {
	r.mu.RLock()
	n, ok := r.nodes[id]
	r.mu.RUnlock()
	if !ok {
		return nil, NewNodeNotFoundError(id)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(n.images))
	for k := range n.images {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}

func (r *MemoryRepo) WriteContent(id NodeID, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.ensureNode(id)
	now := time.Now().UTC()
	n.content = make([]byte, len(data))
	copy(n.content, data)
	n.stats.Updated = now
	return nil
}

func (r *MemoryRepo) WriteMeta(id NodeID, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.ensureNode(id)
	n.meta = make([]byte, len(data))
	copy(n.meta, data)
	n.title = parseTitleFromMeta(data)
	now := time.Now().UTC()
	n.stats.Updated = now
	return nil
}

func (r *MemoryRepo) UploadImage(id NodeID, name string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.ensureNode(id)
	buf := make([]byte, len(data))
	copy(buf, data)
	n.images[name] = buf
	n.stats.Updated = time.Now().UTC()
	return nil
}

func (r *MemoryRepo) UploadItem(id NodeID, name string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.ensureNode(id)
	buf := make([]byte, len(data))
	copy(buf, data)
	n.items[name] = buf
	n.stats.Updated = time.Now().UTC()
	return nil
}

func (r *MemoryRepo) MoveNode(id NodeID, dst NodeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	srcNode, ok := r.nodes[id]
	if !ok {
		return NewNodeNotFoundError(id)
	}
	if _, exists := r.nodes[dst]; exists {
		return NewDestinationExistsError(dst)
	}
	// Move
	r.nodes[dst] = srcNode
	delete(r.nodes, id)
	return nil
}

func (r *MemoryRepo) GetIndex(name string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.indexes[name]
	if !ok {
		return nil, ErrMetaNotFound
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return cp, nil
}

func (r *MemoryRepo) WriteIndex(name string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	r.indexes[name] = cp
	return nil
}

func (r *MemoryRepo) ClearDex() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indexes = make(map[string][]byte)
	return nil
}

func (r *MemoryRepo) DeleteNode(id NodeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return NewNodeNotFoundError(id)
	}
	delete(r.nodes, id)
	return nil
}

func (r *MemoryRepo) DeleteImage(id NodeID, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		return NewNodeNotFoundError(id)
	}
	if _, ok := n.images[name]; !ok {
		return ErrMetaNotFound
	}
	delete(n.images, name)
	n.stats.Updated = time.Now().UTC()
	return nil
}

func (r *MemoryRepo) DeleteItem(id NodeID, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		return NewNodeNotFoundError(id)
	}
	if _, ok := n.items[name]; !ok {
		return ErrMetaNotFound
	}
	delete(n.items, name)
	n.stats.Updated = time.Now().UTC()
	return nil
}

func (r *MemoryRepo) ReadConfig() (Config, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.config == nil {
		return Config{}, ErrMetaNotFound
	}
	return *r.config, nil
}

func (r *MemoryRepo) WriteConfig(config Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := config
	r.config = &c
	return nil
}

// parseTitleFromMeta attempts a lightweight extraction of a top-level "title:"
// YAML-ish line. It's intentionally simple: scan lines and find the first
// "title:" prefix.
func parseTitleFromMeta(data []byte) string {
	s := string(data)
	for _, line := range strings.Split(s, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "title:") {
			// get text after "title:"
			val := strings.TrimSpace(strings.TrimPrefix(trim, "title:"))
			// strip surrounding quotes if present
			if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
				val = val[1 : len(val)-1]
			}
			return val
		}
	}
	return ""
}

var _ KegRepository = (*MemoryRepo)(nil)
