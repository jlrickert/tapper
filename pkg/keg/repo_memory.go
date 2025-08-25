package keg

import (
	"context"
	"slices"
	"sort"
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

	counter int
}

type memoryNode struct {
	content []byte
	meta    []byte
	items   map[string][]byte
	images  map[string][]byte
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
		n = &memoryNode{
			items:  make(map[string][]byte),
			images: make(map[string][]byte),
		}
		r.nodes[id] = n
	}
	return n
}

func (r *MemoryRepo) Name() string {
	return "memory"
}

func (r *MemoryRepo) Next(ctx context.Context) (NodeID, error) {
	// Context currently unused for MemoryRepo, but accepted to satisfy the
	// context-aware KegRepository interface.
	r.mu.Lock()
	defer r.mu.Unlock()
	count := r.counter
	r.counter = r.counter + 1
	return NodeID(count), nil
}

// ReadContent returns the primary content for the given node id.
// If the node does not exist, a typed NodeNotFoundError is returned.
// If the node exists but has no content, nil is returned with a nil error.
// The returned slice is a copy to avoid caller-visible mutation.
func (r *MemoryRepo) ReadContent(ctx context.Context, id NodeID) ([]byte, error) {
	r.mu.RLock()
	n, ok := r.nodes[id]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrNodeNotFound
	}

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
// If meta is absent, ErrNotFound is returned.
// The returned bytes are a copy.
func (r *MemoryRepo) ReadMeta(ctx context.Context, id NodeID) ([]byte, error) {
	r.mu.RLock()
	n, ok := r.nodes[id]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrNodeNotFound
	}
	if n.meta == nil {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(n.meta))
	copy(cp, n.meta)
	return cp, nil
}

// ListIndexes returns the names of stored index files sorted lexicographically.
func (r *MemoryRepo) ListIndexes(ctx context.Context) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.indexes))
	for k := range r.indexes {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}

// ClearIndexes implements KegRepository.
func (r *MemoryRepo) ClearIndexes(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indexes = make(map[string][]byte)
	return nil
}

// ListNodes returns all known NodeIDs sorted in ascending numeric order.
func (r *MemoryRepo) ListNodes(ctx context.Context) ([]NodeID, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]NodeID, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}

func (r *MemoryRepo) getNode(id NodeID) (*memoryNode, bool) {
	r.mu.RLock()
	n, ok := r.nodes[id]
	r.mu.RUnlock()
	return n, ok
}

// ListItems lists ancillary item names stored for a node, sorted lexicographically.
// If the node does not exist, a typed NodeNotFoundError is returned.
func (r *MemoryRepo) ListItems(ctx context.Context, id NodeID) ([]string, error) {
	n, ok := r.getNode(id)
	if !ok {
		return nil, ErrNodeNotFound
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
func (r *MemoryRepo) ListImages(ctx context.Context, id NodeID) ([]string, error) {
	n, ok := r.getNode(id)
	if !ok {
		return nil, ErrNodeNotFound
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
func (r *MemoryRepo) WriteContent(ctx context.Context, id NodeID, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.ensureNode(id)
	copy(n.content, data)
	return nil
}

// WriteMeta sets the node metadata (meta.yaml bytes), updates the cached title
// extracted from the meta, and updates the node's Updated timestamp.
//
// The stored meta is a copy of the provided slice.
func (r *MemoryRepo) WriteMeta(ctx context.Context, id NodeID, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.ensureNode(id)
	n.meta = make([]byte, len(data))
	copy(n.meta, data)
	return nil
}

// UploadImage stores an image blob for the node. Name is used as the key and
// the provided data is copied. Updates the node's Updated timestamp.
func (r *MemoryRepo) UploadImage(ctx context.Context, id NodeID, name string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.ensureNode(id)
	buf := make([]byte, len(data))
	copy(buf, data)
	n.images[name] = buf
	return nil
}

// UploadItem stores an ancillary item blob for the node. The data is copied
// and the node's Updated timestamp is bumped.
func (r *MemoryRepo) UploadItem(ctx context.Context, id NodeID, name string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.ensureNode(id)
	buf := make([]byte, len(data))
	copy(buf, data)
	n.items[name] = buf
	return nil
}

// MoveNode renames or moves a node from id to dst. If the source node does not
// exist, a NodeNotFoundError is returned. If the destination already exists,
// a DestinationExistsError is returned.
func (r *MemoryRepo) MoveNode(ctx context.Context, id NodeID, dst NodeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	srcNode, ok := r.nodes[id]
	if !ok {
		return ErrNodeNotFound
	}
	if _, exists := r.nodes[dst]; exists {
		return NewDestinationExistsError(dst)
	}
	// Move
	r.nodes[dst] = srcNode
	delete(r.nodes, id)
	return nil
}

// GetIndex reads a stored index by name. If not present, ErrNotFound is
// returned. The returned bytes are a copy.
func (r *MemoryRepo) GetIndex(ctx context.Context, name string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.indexes[name]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return cp, nil
}

// WriteIndex writes or replaces an in-memory index file. The data is copied.
func (r *MemoryRepo) WriteIndex(ctx context.Context, name string, data []byte) error {
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

// DeleteNode removes the node and all associated content/metadata/items. If
// the node does not exist, a NodeNotFoundError is returned.
func (r *MemoryRepo) DeleteNode(ctx context.Context, id NodeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return NewNodeNotFoundError(id)
	}
	delete(r.nodes, id)
	return nil
}

// DeleteImage removes a stored image by name for the node. Returns
// NodeNotFoundError if the node doesn't exist or ErrNotFound if the image name
// is not present.
func (r *MemoryRepo) DeleteImage(ctx context.Context, id NodeID, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		return NewNodeNotFoundError(id)
	}
	if _, ok := n.images[name]; !ok {
		return ErrNotFound
	}
	delete(n.images, name)
	return nil
}

// DeleteItem removes an ancillary item by name for the node. Returns
// NodeNotFoundError if the node doesn't exist or ErrNotFound if the item name
// is not present.
func (r *MemoryRepo) DeleteItem(ctx context.Context, id NodeID, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		return NewNodeNotFoundError(id)
	}
	if _, ok := n.items[name]; !ok {
		return ErrNotFound
	}
	delete(n.items, name)
	return nil
}

// ReadConfig returns the repository-level config previously written with
// WriteConfig. If no config has been written, ErrNotFound is returned.
func (r *MemoryRepo) ReadConfig(ctx context.Context) (*Config, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.config == nil {
		return nil, ErrNotFound
	}
	// return a copy to avoid external mutation
	c := *r.config
	return &c, nil
}

// WriteConfig stores the provided Config in-memory. A copy of the value is kept.
func (r *MemoryRepo) WriteConfig(ctx context.Context, config Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := config
	r.config = &c
	return nil
}

// ClearNodeLock implements KegRepository.
//
// For the in-memory repo we represent a per-node lock using a reserved
// item key (KegLockFile) inside the node's items map. Clearing the lock is a
// best-effort operation: if the node does not exist we return NewNodeNotFoundError.
func (r *MemoryRepo) ClearNodeLock(ctx context.Context, id NodeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	n, ok := r.nodes[id]
	if !ok {
		return NewNodeNotFoundError(id)
	}

	// Remove the lock marker if present.
	delete(n.items, KegLockFile)
	return nil
}

// LockNode implements KegRepository.
//
// This function attempts to acquire a per-node lock. It will retry at the
// provided retryInterval until the context is cancelled. If the node does not
// exist, a NodeNotFoundError is returned immediately. On success an unlock
// function is returned which the caller MUST call to release the lock.
func (r *MemoryRepo) LockNode(ctx context.Context, id NodeID, retryInterval time.Duration) (func() error, error) {
	// Default retry interval if caller gives zero or negative.
	if retryInterval <= 0 {
		retryInterval = 100 * time.Millisecond
	}

	// Fast path: try to acquire immediately.
	r.mu.Lock()
	n, ok := r.nodes[id]
	if !ok {
		r.mu.Unlock()
		return nil, NewNodeNotFoundError(id)
	}
	if _, locked := n.items[KegLockFile]; !locked {
		// Acquire lock by setting a reserved item key.
		n.items[KegLockFile] = []byte{1}
		r.mu.Unlock()

		unlock := func() error {
			r.mu.Lock()
			defer r.mu.Unlock()
			// If node was deleted, nothing to do; deletion is allowed.
			if nn, ok := r.nodes[id]; ok {
				delete(nn.items, KegLockFile)
			}
			return nil
		}
		return unlock, nil
	}
	r.mu.Unlock()

	// Wait/retry loop until ctx is done or we acquire the lock.
	ticker := time.NewTicker(retryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Per package semantics, use ErrLockTimeout to indicate the lock
			// acquisition was canceled/timed out.
			return nil, ErrLockTimeout
		case <-ticker.C:
			r.mu.Lock()
			n, ok := r.nodes[id]
			if !ok {
				r.mu.Unlock()
				return nil, NewNodeNotFoundError(id)
			}
			if _, locked := n.items[KegLockFile]; !locked {
				n.items[KegLockFile] = []byte{1}
				r.mu.Unlock()

				unlock := func() error {
					r.mu.Lock()
					defer r.mu.Unlock()
					if nn, ok := r.nodes[id]; ok {
						delete(nn.items, KegLockFile)
					}
					return nil
				}
				return unlock, nil
			}
			r.mu.Unlock()
			// continue retrying
		}
	}
}

// Ensure MemoryRepo implements KegRepository at compile time.
var _ KegRepository = (*MemoryRepo)(nil)
