package keg

import (
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// MemoryRepo is an in-memory implementation of KegRepository intended for
// tests and lightweight tooling that doesn't require persistent storage.
//
// Concurrency:
//   - It is safe for concurrent use by multiple goroutines. An internal RWMutex
//     protects access to nodes, indexes, and config.
//   - Methods that mutate state take the write lock; readers take the read lock.
//
// Semantics:
//   - Node IDs are allocated implicitly when writing content/meta/items/images.
//   - Index files are stored in-memory by name (for example "nodes.tsv") and can
//     be written/read with WriteIndex/GetIndex.
//   - Errors returned attempt to follow the pkg/keg sentinel/typed error policy
//     (e.g., NewNodeNotFoundError, ErrMetaNotFound).
type MemoryRepo struct {
	mu sync.RWMutex
	// nodes stores per-node data keyed by NodeID.
	nodes map[NodeID]*memoryNode
	// indexes stores raw index files by name (for example: "nodes.tsv").
	indexes map[string][]byte
	// config holds the in-memory Config if written.
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

// NewMemoryRepo constructs a ready-to-use in-memory repository.
func NewMemoryRepo() *MemoryRepo {
	return &MemoryRepo{
		nodes:   make(map[NodeID]*memoryNode),
		indexes: make(map[string][]byte),
	}
}

// ensureNode returns an existing node or creates one if absent.
// Caller must hold repo.mu (at least write) when invoking this helper.
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

// ReadContent returns the primary content for the given node id.
// If the node does not exist, a typed NodeNotFoundError is returned.
// If the node exists but has no content, nil is returned with a nil error.
// The returned slice is a copy to avoid caller-visible mutation.
func (r *MemoryRepo) ReadContent(id NodeID) ([]byte, error) {
	r.mu.RLock()
	n, ok := r.nodes[id]
	r.mu.RUnlock()
	if !ok {
		return nil, NewNodeNotFoundError(id)
	}
	// update access time under write lock
	r.mu.Lock()
	n.stats.Access = time.Now().UTC()
	r.mu.Unlock()

	if n.content == nil {
		// Content may legitimately be absent; return nil rather than ErrMetaNotFound.
		return nil, nil
	}
	cp := make([]byte, len(n.content))
	copy(cp, n.content)
	return cp, nil
}

// ReadMeta returns the serialized node metadata (usually meta.yaml).
// If the node does not exist, a typed NodeNotFoundError is returned.
// If meta is absent, ErrMetaNotFound is returned.
// The returned bytes are a copy.
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

// Stats returns timestamp statistics for the node. If the node does not exist,
// a typed NodeNotFoundError is returned.
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

// ListIndexes returns the names of stored index files sorted lexicographically.
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

// ListNodesID returns all known NodeIDs sorted in ascending numeric order.
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

// ListNodes returns NodeRef entries for all nodes. Results are sorted by numeric
// id ascending for deterministic ordering.
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

// ListItems lists ancillary item names stored for a node, sorted lexicographically.
// If the node does not exist, a typed NodeNotFoundError is returned.
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

// ListImages lists stored image names for a node, sorted lexicographically.
// Returns NodeNotFoundError if the node doesn't exist.
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

// WriteContent writes the primary content for the given node id, creating the
// node if necessary. It updates the node's Updated timestamp.
//
// The stored content is a copy of the provided slice.
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

// WriteMeta sets the node metadata (meta.yaml bytes), updates the cached title
// extracted from the meta, and updates the node's Updated timestamp.
//
// The stored meta is a copy of the provided slice.
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

// UploadImage stores an image blob for the node. Name is used as the key and the
// provided data is copied. Updates the node's Updated timestamp.
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

// UploadItem stores an ancillary item blob for the node. The data is copied and
// the node's Updated timestamp is bumped.
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

// MoveNode renames or moves a node from id to dst. If the source node does not
// exist, a NodeNotFoundError is returned. If the destination already exists,
// a DestinationExistsError is returned.
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

// GetIndex reads a stored index by name. If not present, ErrMetaNotFound
// is returned. The returned bytes are a copy.
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

// WriteIndex writes or replaces an in-memory index file. The data is copied.
func (r *MemoryRepo) WriteIndex(name string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	r.indexes[name] = cp
	return nil
}

// ClearDex removes all stored index artifacts (resets the indexes map).
func (r *MemoryRepo) ClearDex() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indexes = make(map[string][]byte)
	return nil
}

// DeleteNode removes the node and all associated content/metadata/items.
// If the node does not exist, a NodeNotFoundError is returned.
func (r *MemoryRepo) DeleteNode(id NodeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return NewNodeNotFoundError(id)
	}
	delete(r.nodes, id)
	return nil
}

// DeleteImage removes a stored image by name for the node.
// Returns NodeNotFoundError if the node doesn't exist or ErrMetaNotFound if the
// image name is not present.
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

// DeleteItem removes an ancillary item by name for the node.
// Returns NodeNotFoundError if the node doesn't exist or ErrMetaNotFound if the
// item name is not present.
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

// ReadConfig returns the repository-level config previously written with
// WriteConfig. If no config has been written, ErrMetaNotFound is returned.
func (r *MemoryRepo) ReadConfig() (Config, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.config == nil {
		return Config{}, ErrMetaNotFound
	}
	return *r.config, nil
}

// WriteConfig stores the provided Config in-memory. A copy of the value is kept.
func (r *MemoryRepo) WriteConfig(config Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := config
	r.config = &c
	return nil
}

// parseTitleFromMeta performs a lightweight extraction of a top-level "title:"
// YAML-ish line from the provided bytes. The function is intentionally simple:
// it scans lines for a "title:" prefix and returns the text after the colon,
// with surrounding single or double quotes stripped if present.
//
// This helper is best-effort and not a full YAML parser; it exists to provide a
// quick title cache for the in-memory repo. For robust metadata parsing, use
// ParseMeta and the Meta helpers.
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

// Ensure MemoryRepo implements KegRepository at compile time.
var _ KegRepository = (*MemoryRepo)(nil)
