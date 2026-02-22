package keg

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// MemoryRepo is an in-memory implementation of Repository intended for
// tests and lightweight tooling that doesn't require persistent storage.
//
// Concurrency / locking:
//
//   - MemoryRepo uses an internal sync.RWMutex (mu) to guard all internal maps
//     and per-node structures. Readers should use RLock/RUnlock; mutating
//     operations use Lock/Unlock.
//   - The implementation is safe for concurrent use by multiple goroutines.
//
// Semantics / behavior:
//
//   - NodeId entries are created on demand when writing content, meta, items, or
//     images.
//   - Index files are kept in-memory by name (for example "nodes.tsv") and are
//     accessible via WriteIndex/GetIndex.
//   - Methods return sentinel or typed errors defined in the package to match the
//     Repository contract (for example NewNodeNotFoundError, ErrNotFound).
type MemoryRepo struct {
	mu sync.RWMutex
	// nodes stores per-node data keyed by NodeID.
	nodes map[NodeId]*memoryNode
	// nodeLocks tracks active per-node lock ownership.
	nodeLocks map[NodeId]struct{}
	// indexes stores raw index files by name (for example: "nodes.tsv").
	indexes map[string][]byte
	// config holds the in-memory Config if written.
	config *Config

	runtime *toolkit.Runtime
}

type memoryNode struct {
	content []byte
	meta    []byte
	items   map[string][]byte
	images  map[string][]byte
}

// NewMemoryRepo constructs a ready-to-use in-memory repository.
func NewMemoryRepo(rt *toolkit.Runtime) *MemoryRepo {
	return &MemoryRepo{
		nodes:     make(map[NodeId]*memoryNode),
		nodeLocks: make(map[NodeId]struct{}),
		indexes:   make(map[string][]byte),
		runtime:   rt,
	}
}

// ensureNode returns an existing node or creates one if absent.
// Caller must hold r.mu (write lock) when invoking this helper.
func (r *MemoryRepo) ensureNode(id NodeId) *memoryNode {
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

func (r *MemoryRepo) Runtime() *toolkit.Runtime {
	if r == nil {
		return nil
	}
	return r.runtime
}

// Next returns a new NodeID. The context is accepted to satisfy the repository
// interface but is not used by this in-memory implementation.
//
// This implementation finds the highest existing node id and returns that value
// + 1. If no nodes exist, it returns 0.
func (r *MemoryRepo) Next(ctx context.Context) (NodeId, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Find the maximum existing NodeID.
	max := -1
	for id := range r.nodes {
		if int(id.ID) > max {
			max = int(id.ID)
		}
	}

	if max < 0 {
		// No nodes yet, start at 0.
		return NodeId{ID: 0}, nil
	}

	next := max + 1
	return NodeId{ID: next}, nil
}

// ReadContent returns the primary content for the given node id.
//
// - If the node does not exist, ErrNodeNotFound is returned.
// - If the node exists but has no content, (nil, nil) is returned.
// - The returned slice is a copy to prevent caller-visible mutation.
func (r *MemoryRepo) ReadContent(ctx context.Context, id NodeId) ([]byte, error) {
	r.mu.RLock()
	n, ok := r.nodes[id]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrNotExist
	}

	if n.content == nil {
		// NodeContent may legitimately be absent; return nil rather than ErrNotFound.
		return nil, nil
	}
	cp := make([]byte, len(n.content))
	copy(cp, n.content)
	return cp, nil
}

// ReadMeta returns the serialized node metadata (usually meta.yaml).
//
// - If the node does not exist, ErrNodeNotFound is returned.
// - If meta is absent, ErrNotFound is returned.
// - The returned bytes are a copy.
func (r *MemoryRepo) ReadMeta(ctx context.Context, id NodeId) ([]byte, error) {
	r.mu.RLock()
	n, ok := r.nodes[id]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrNotExist
	}
	if n.meta == nil {
		return nil, ErrNotExist
	}
	cp := make([]byte, len(n.meta))
	copy(cp, n.meta)
	return cp, nil
}

// ReadStats returns parsed programmatic stats for a node.
func (r *MemoryRepo) ReadStats(ctx context.Context, id NodeId) (*NodeStats, error) {
	r.mu.RLock()
	n, ok := r.nodes[id]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrNotExist
	}
	if n.meta == nil {
		return &NodeStats{links: []NodeId{}}, nil
	}
	cp := make([]byte, len(n.meta))
	copy(cp, n.meta)
	stats, err := ParseStats(ctx, cp)
	if err != nil {
		return nil, err
	}
	return stats, nil
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

// ClearIndexes removes all stored index artifacts.
func (r *MemoryRepo) ClearIndexes(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indexes = make(map[string][]byte)
	return nil
}

// ListNodes returns all known NodeIDs sorted in ascending numeric order.
func (r *MemoryRepo) ListNodes(ctx context.Context) ([]NodeId, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]NodeId, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b NodeId) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return ids, nil
}

// getNode is a small helper that returns the node and a boolean indicating
// presence. It uses RLock/RUnlock internally.
func (r *MemoryRepo) getNode(id NodeId) (*memoryNode, bool) {
	r.mu.RLock()
	n, ok := r.nodes[id]
	r.mu.RUnlock()
	return n, ok
}

// ListAssets lists asset names for a node and asset kind, sorted lexicographically.
func (r *MemoryRepo) ListAssets(ctx context.Context, id NodeId, kind AssetKind) ([]string, error) {
	_ = ctx
	n, ok := r.getNode(id)
	if !ok {
		return nil, ErrNotExist
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	var src map[string][]byte
	switch kind {
	case AssetKindImage:
		src = n.images
	case AssetKindItem:
		src = n.items
	default:
		return nil, fmt.Errorf("unknown asset kind %q", kind)
	}

	names := make([]string, 0, len(src))
	for k := range src {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}

// WriteContent writes the primary content for the given node id, creating the
// node if necessary.
//
// Note: this implementation stores the provided slice reference in-memory.
// Callers should avoid mutating the provided slice after calling this method.
func (r *MemoryRepo) WriteContent(ctx context.Context, id NodeId, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.ensureNode(id)
	n.content = data
	return nil
}

// WriteMeta sets the node metadata (meta.yaml bytes), creating the node if needed.
//
// Note: the provided slice is stored as-is in-memory; do not modify it after
// writing.
func (r *MemoryRepo) WriteMeta(ctx context.Context, id NodeId, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.ensureNode(id)
	n.meta = data
	return nil
}

// WriteStats writes programmatic stats while preserving manually edited meta
// fields.
func (r *MemoryRepo) WriteStats(ctx context.Context, id NodeId, stats *NodeStats) error {
	if stats == nil {
		stats = &NodeStats{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	n := r.ensureNode(id)

	var raw []byte
	if n.meta != nil {
		raw = make([]byte, len(n.meta))
		copy(raw, n.meta)
	}

	meta, err := ParseMeta(ctx, raw)
	if err != nil {
		return err
	}
	n.meta = []byte(meta.ToYAMLWithStats(stats))
	return nil
}

// WriteAsset stores a named asset blob for a node.
func (r *MemoryRepo) WriteAsset(ctx context.Context, id NodeId, kind AssetKind, name string, data []byte) error {
	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.ensureNode(id)

	switch kind {
	case AssetKindImage:
		n.images[name] = data
	case AssetKindItem:
		n.items[name] = data
	default:
		return fmt.Errorf("unknown asset kind %q", kind)
	}
	return nil
}

// MoveNode renames or moves a node from id to dst.
//
// - If the source node does not exist, ErrNodeNotFound is returned.
// - If the destination already exists, a DestinationExistsError is returned.
// The move is performed by transferring the in-memory node pointer.
func (r *MemoryRepo) MoveNode(ctx context.Context, id NodeId, dst NodeId) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	srcNode, ok := r.nodes[id]
	if !ok {
		return ErrNotExist
	}
	if _, exists := r.nodes[dst]; exists {
		return ErrDestinationExists
	}
	// Move (transfer pointer)
	r.nodes[dst] = srcNode
	delete(r.nodes, id)
	return nil
}

// GetIndex reads a stored index by name. If not present, ErrNotFound is returned.
// The returned bytes are a copy.
func (r *MemoryRepo) GetIndex(ctx context.Context, name string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.indexes[name]
	if !ok {
		return nil, ErrNotExist
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return cp, nil
}

// WriteIndex writes or replaces an in-memory index file.
func (r *MemoryRepo) WriteIndex(ctx context.Context, name string, data []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indexes[name] = data
	return nil
}

// ClearDex removes all stored index artifacts.
func (r *MemoryRepo) ClearDex() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indexes = make(map[string][]byte)
	return nil
}

// DeleteNode removes the node and all associated content/metadata/items.
// If the node does not exist, NewNodeNotFoundError is returned.
func (r *MemoryRepo) DeleteNode(ctx context.Context, id NodeId) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return ErrNotExist
	}
	delete(r.nodes, id)
	return nil
}

// DeleteAsset removes an asset by name for a node.
func (r *MemoryRepo) DeleteAsset(ctx context.Context, id NodeId, kind AssetKind, name string) error {
	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.nodes[id]
	if !ok {
		return ErrNotExist
	}

	switch kind {
	case AssetKindImage:
		if _, ok := n.images[name]; !ok {
			return ErrNotExist
		}
		delete(n.images, name)
	case AssetKindItem:
		if _, ok := n.items[name]; !ok {
			return ErrNotExist
		}
		delete(n.items, name)
	default:
		return fmt.Errorf("unknown asset kind %q", kind)
	}
	return nil
}

// Compatibility wrappers for pre-assets API callers.
func (r *MemoryRepo) ListItems(ctx context.Context, id NodeId) ([]string, error) {
	return r.ListAssets(ctx, id, AssetKindItem)
}

func (r *MemoryRepo) ListImages(ctx context.Context, id NodeId) ([]string, error) {
	return r.ListAssets(ctx, id, AssetKindImage)
}

func (r *MemoryRepo) UploadImage(ctx context.Context, id NodeId, name string, data []byte) error {
	return r.WriteAsset(ctx, id, AssetKindImage, name, data)
}

func (r *MemoryRepo) UploadItem(ctx context.Context, id NodeId, name string, data []byte) error {
	return r.WriteAsset(ctx, id, AssetKindItem, name, data)
}

func (r *MemoryRepo) DeleteImage(ctx context.Context, id NodeId, name string) error {
	return r.DeleteAsset(ctx, id, AssetKindImage, name)
}

func (r *MemoryRepo) DeleteItem(ctx context.Context, id NodeId, name string) error {
	return r.DeleteAsset(ctx, id, AssetKindItem, name)
}

// ReadConfig returns the repository-level config previously written with
// WriteConfig. If no config has been written, ErrNotFound is returned.
// A copy of the stored Config is returned to avoid external mutation.
func (r *MemoryRepo) ReadConfig(ctx context.Context) (*Config, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.config == nil {
		return nil, ErrNotExist
	}
	c := *r.config
	return &c, nil
}

// WriteConfig stores the provided Config in-memory. A copy of the value is kept.
func (r *MemoryRepo) WriteConfig(ctx context.Context, config *Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := config
	r.config = c
	return nil
}

// ClearNodeLock removes an active per-node lock marker.
func (r *MemoryRepo) ClearNodeLock(ctx context.Context, id NodeId) error {
	_ = ctx
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.nodeLocks, lockNodeKey(id))
	return nil
}

// LockNode attempts to acquire a per-node lock. It will retry at the provided
// retryInterval until the context is cancelled. On success it returns an unlock
// function which the caller MUST call to release the lock.
//
// Behavior notes:
//
// - If retryInterval <= 0, a sensible default is used.
// - If ctx is cancelled while waiting, ErrLockTimeout is returned.
func (r *MemoryRepo) LockNode(ctx context.Context, id NodeId, retryInterval time.Duration) (func() error, error) {
	key := lockNodeKey(id)

	// Default retry interval if caller gives zero or negative.
	if retryInterval <= 0 {
		retryInterval = 100 * time.Millisecond
	}

	// Fast path: try to acquire immediately.
	r.mu.Lock()
	if _, locked := r.nodeLocks[key]; !locked {
		// Acquire lock immediately.
		r.nodeLocks[key] = struct{}{}
		r.mu.Unlock()

		unlock := func() error {
			r.mu.Lock()
			defer r.mu.Unlock()
			delete(r.nodeLocks, key)
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
			return nil, fmt.Errorf("%w: %w", ErrLockTimeout, ctx.Err())
		case <-ticker.C:
			r.mu.Lock()
			if _, locked := r.nodeLocks[key]; !locked {
				r.nodeLocks[key] = struct{}{}
				r.mu.Unlock()

				unlock := func() error {
					r.mu.Lock()
					defer r.mu.Unlock()
					delete(r.nodeLocks, key)
					return nil
				}
				return unlock, nil
			}
			r.mu.Unlock()
			// continue retrying
		}
	}
}

// WithNodeLock executes fn while holding an exclusive lock for node id.
func (r *MemoryRepo) WithNodeLock(ctx context.Context, id NodeId, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("fn required")
	}
	if contextHasNodeLock(ctx, id) {
		return fn(ctx)
	}

	unlock, err := r.LockNode(ctx, id, 100*time.Millisecond)
	if err != nil {
		if errors.Is(err, ErrLockTimeout) {
			return err
		}
		return errors.Join(ErrLock, err)
	}

	lockedCtx := contextWithNodeLock(ctx, id)
	runErr := fn(lockedCtx)
	unlockErr := unlock()
	return errors.Join(runErr, unlockErr)
}

// Ensure MemoryRepo implements Repository at compile time.
var _ Repository = (*MemoryRepo)(nil)
