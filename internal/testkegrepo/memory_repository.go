package testkegrepo

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	. "github.com/jlrickert/tapper/pkg/keg"
)

// MemoryRepository is an in-memory Repository used only by this package's
// tests. PostgreSQL remains the sole production LocalKeg repository.
type MemoryRepository struct {
	runtime *toolkit.Runtime

	boundary sync.RWMutex
	mu       sync.RWMutex
	nodes    map[NodeId]*memoryNode
	reserved map[NodeId]struct{}
	indexes  map[string][]byte
	settings []byte
	schemas  map[string][]byte
	snaps    map[NodeId][]memorySnapshot
	locks    map[NodeId]LockInfo
	nodeMu   map[NodeId]*sync.Mutex

	watchersMu sync.Mutex
	watchers   map[*memoryWatcher]struct{}
}

type memoryNode struct {
	content []byte
	meta    []byte
	stats   *NodeStats
	files   map[string][]byte
	images  map[string][]byte
}

type memorySnapshot struct {
	snapshot Snapshot
	content  []byte
	meta     []byte
	stats    *NodeStats
}

type memoryWatcher struct {
	ids map[NodeId]struct{}
	ch  chan NodeEvent
}

type memoryBoundaryKey struct{}

type memoryNodeLockKey struct{}

func contextHasMemoryNodeLock(ctx context.Context, id NodeId) bool {
	locked, _ := ctx.Value(memoryNodeLockKey{}).(map[NodeId]struct{})
	_, ok := locked[id]
	return ok
}

func contextWithMemoryNodeLock(ctx context.Context, id NodeId) context.Context {
	previous, _ := ctx.Value(memoryNodeLockKey{}).(map[NodeId]struct{})
	locked := make(map[NodeId]struct{}, len(previous)+1)
	for held := range previous {
		locked[held] = struct{}{}
	}
	locked[id] = struct{}{}
	return context.WithValue(ctx, memoryNodeLockKey{}, locked)
}

type memoryBoundary struct {
	owner *MemoryRepository
	write bool
}

// NewMemoryRepository returns a concurrency-safe test repository.
func NewMemoryRepository(rt *toolkit.Runtime) *MemoryRepository {
	return &MemoryRepository{
		runtime:  rt,
		nodes:    make(map[NodeId]*memoryNode),
		reserved: make(map[NodeId]struct{}),
		indexes:  make(map[string][]byte),
		schemas:  make(map[string][]byte),
		snaps:    make(map[NodeId][]memorySnapshot),
		locks:    make(map[NodeId]LockInfo),
		nodeMu:   make(map[NodeId]*sync.Mutex),
		watchers: make(map[*memoryWatcher]struct{}),
	}
}

func (r *MemoryRepository) Name() string { return "memory-test" }

func (r *MemoryRepository) WithKegRead(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("fn required")
	}
	if held, _ := ctx.Value(memoryBoundaryKey{}).(memoryBoundary); held.owner == r {
		return fn(ctx)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrLockTimeout, err)
	}
	r.boundary.RLock()
	defer r.boundary.RUnlock()
	return fn(context.WithValue(ctx, memoryBoundaryKey{}, memoryBoundary{owner: r}))
}

func (r *MemoryRepository) WithKegWrite(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("fn required")
	}
	if held, _ := ctx.Value(memoryBoundaryKey{}).(memoryBoundary); held.owner == r {
		if !held.write {
			return ErrKegLockUpgrade
		}
		return fn(ctx)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrLockTimeout, err)
	}
	r.boundary.Lock()
	defer r.boundary.Unlock()
	return fn(context.WithValue(ctx, memoryBoundaryKey{}, memoryBoundary{owner: r, write: true}))
}

func (r *MemoryRepository) SupportsConcurrentAccess(context.Context) bool { return true }

func (r *MemoryRepository) HasNode(ctx context.Context, id NodeId) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.nodes[id]
	return ok, nil
}

func (r *MemoryRepository) Next(ctx context.Context) (NodeId, error) {
	if err := ctx.Err(); err != nil {
		return NodeId{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	maxID := -1
	for id := range r.nodes {
		if id.Code == "" && id.ID > maxID {
			maxID = id.ID
		}
	}
	for id := range r.reserved {
		if id.Code == "" && id.ID > maxID {
			maxID = id.ID
		}
	}
	id := NodeId{ID: maxID + 1}
	r.reserved[id] = struct{}{}
	return id, nil
}

func (r *MemoryRepository) ListNodes(ctx context.Context) ([]NodeId, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]NodeId, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b NodeId) int { return a.Compare(b) })
	return ids, nil
}

func (r *MemoryRepository) MoveNode(ctx context.Context, id, dst NodeId) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	node, ok := r.nodes[id]
	if !ok {
		return ErrNotExist
	}
	if _, exists := r.nodes[dst]; exists {
		return ErrDestinationExists
	}
	r.nodes[dst] = node
	delete(r.nodes, id)
	delete(r.reserved, dst)
	if snaps := r.snaps[id]; snaps != nil {
		for i := range snaps {
			snaps[i].snapshot.Node = dst
		}
		r.snaps[dst] = snaps
		delete(r.snaps, id)
	}
	return nil
}

func (r *MemoryRepository) DeleteNode(ctx context.Context, id NodeId) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return ErrNotExist
	}
	delete(r.nodes, id)
	delete(r.reserved, id)
	delete(r.snaps, id)
	delete(r.locks, id)
	return nil
}

func (r *MemoryRepository) WithNodeLock(ctx context.Context, id NodeId, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("fn required")
	}
	if contextHasMemoryNodeLock(ctx, id) {
		return fn(ctx)
	}
	r.mu.Lock()
	lock := r.nodeMu[id]
	if lock == nil {
		lock = &sync.Mutex{}
		r.nodeMu[id] = lock
	}
	r.mu.Unlock()
	acquired := make(chan struct{})
	go func() {
		lock.Lock()
		close(acquired)
	}()
	select {
	case <-ctx.Done():
		go func() { <-acquired; lock.Unlock() }()
		return fmt.Errorf("%w: %w", ErrLockTimeout, ctx.Err())
	case <-acquired:
	}
	defer lock.Unlock()
	return fn(contextWithMemoryNodeLock(ctx, id))
}

func (r *MemoryRepository) ReadContent(ctx context.Context, id NodeId) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	node := r.nodes[id]
	var out []byte
	if node != nil {
		out = cloneBytes(node.content)
	}
	r.mu.RUnlock()
	if node == nil {
		return nil, ErrNotExist
	}
	r.Emit(NodeEvent{Kind: NodeEventAccessed, NodeID: id, Field: "content"})
	return out, nil
}

func (r *MemoryRepository) WriteContent(ctx context.Context, id NodeId, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	node, existed := r.nodes[id]
	if node == nil {
		node = newMemoryNode()
		r.nodes[id] = node
	}
	node.content = cloneBytes(data)
	delete(r.reserved, id)
	r.mu.Unlock()
	kind := NodeEventModified
	if !existed {
		kind = NodeEventCreated
	}
	r.Emit(NodeEvent{Kind: kind, NodeID: id, Field: "content"})
	return nil
}

func (r *MemoryRepository) ReadMeta(ctx context.Context, id NodeId) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	node := r.nodes[id]
	if node == nil {
		return nil, ErrNotExist
	}
	return cloneBytes(node.meta), nil
}

func (r *MemoryRepository) WriteMeta(ctx context.Context, id NodeId, data []byte) error {
	return r.updateNode(ctx, id, func(node *memoryNode) { node.meta = cloneBytes(data) })
}

func (r *MemoryRepository) ReadStats(ctx context.Context, id NodeId) (*NodeStats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	node := r.nodes[id]
	if node == nil || node.stats == nil {
		return nil, ErrNotExist
	}
	return cloneStats(ctx, node.stats)
}

func (r *MemoryRepository) WriteStats(ctx context.Context, id NodeId, stats *NodeStats) error {
	copyStats, err := cloneStats(ctx, stats)
	if err != nil {
		return err
	}
	return r.updateNode(ctx, id, func(node *memoryNode) { node.stats = copyStats })
}

func (r *MemoryRepository) updateNode(ctx context.Context, id NodeId, update func(*memoryNode)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	node := r.nodes[id]
	if node == nil {
		return ErrNotExist
	}
	update(node)
	return nil
}

func (r *MemoryRepository) ReadMetaBatch(ctx context.Context, ids []NodeId) (map[string][]byte, error) {
	out := make(map[string][]byte)
	for _, id := range ids {
		raw, err := r.ReadMeta(ctx, id)
		if err == nil {
			out[id.Path()] = raw
		} else if !errors.Is(err, ErrNotExist) {
			return nil, err
		}
	}
	return out, nil
}

func (r *MemoryRepository) ReadStatsBatch(ctx context.Context, ids []NodeId) (map[string]*NodeStats, error) {
	out := make(map[string]*NodeStats)
	for _, id := range ids {
		stats, err := r.ReadStats(ctx, id)
		if err == nil {
			out[id.Path()] = stats
		} else if !errors.Is(err, ErrNotExist) {
			return nil, err
		}
	}
	return out, nil
}

func (r *MemoryRepository) GetIndex(ctx context.Context, name string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, ok := r.indexes[name]
	if !ok {
		return nil, ErrNotExist
	}
	return cloneBytes(data), nil
}

func (r *MemoryRepository) WriteIndex(ctx context.Context, name string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indexes[name] = cloneBytes(data)
	return nil
}

func (r *MemoryRepository) ListIndexes(ctx context.Context) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(r.indexes))
	for name := range r.indexes {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func (r *MemoryRepository) ClearIndexes(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.indexes = make(map[string][]byte)
	return nil
}

func (r *MemoryRepository) ReadSettings(ctx context.Context) (*Settings, error) {
	raw, err := r.ReadSettingsDocument(ctx)
	if err != nil {
		return nil, err
	}
	return ParseKegSettings(raw)
}

func (r *MemoryRepository) WriteSettings(ctx context.Context, settings *Settings) error {
	raw, err := settings.ToYAML()
	if err != nil {
		return err
	}
	return r.WriteSettingsDocument(ctx, raw)
}

func (r *MemoryRepository) ReadSettingsDocument(ctx context.Context) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.settings == nil {
		return nil, ErrNotExist
	}
	return cloneBytes(r.settings), nil
}

func (r *MemoryRepository) WriteSettingsDocument(ctx context.Context, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settings = cloneBytes(data)
	return nil
}

func (r *MemoryRepository) ListSchemas(ctx context.Context) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(r.schemas))
	for name := range r.schemas {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func (r *MemoryRepository) ReadSchema(ctx context.Context, name string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, ok := r.schemas[name]
	if !ok {
		return nil, ErrNotExist
	}
	return cloneBytes(data), nil
}

func (r *MemoryRepository) CreateSchema(ctx context.Context, name string, data []byte) error {
	if _, err := SchemaFilename(name); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.schemas[name]; ok {
		return ErrExist
	}
	r.schemas[name] = cloneBytes(data)
	return nil
}

func (r *MemoryRepository) WriteSchema(ctx context.Context, name string, data []byte) error {
	if _, err := SchemaFilename(name); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.schemas[name] = cloneBytes(data)
	return nil
}

func (r *MemoryRepository) DeleteSchema(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.schemas[name]; !ok {
		return ErrNotExist
	}
	delete(r.schemas, name)
	return nil
}

func (r *MemoryRepository) ListFiles(ctx context.Context, id NodeId) ([]string, error) {
	return r.listAssets(ctx, id, false)
}

func (r *MemoryRepository) ListImages(ctx context.Context, id NodeId) ([]string, error) {
	return r.listAssets(ctx, id, true)
}

func (r *MemoryRepository) listAssets(ctx context.Context, id NodeId, images bool) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	node := r.nodes[id]
	if node == nil {
		return nil, ErrNotExist
	}
	assets := node.files
	if images {
		assets = node.images
	}
	names := make([]string, 0, len(assets))
	for name := range assets {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func (r *MemoryRepository) ReadFile(ctx context.Context, id NodeId, name string) ([]byte, error) {
	return r.readAsset(ctx, id, name, false)
}

func (r *MemoryRepository) ReadImage(ctx context.Context, id NodeId, name string) ([]byte, error) {
	return r.readAsset(ctx, id, name, true)
}

func (r *MemoryRepository) readAsset(ctx context.Context, id NodeId, name string, images bool) ([]byte, error) {
	if err := ValidateAssetName(name); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	node := r.nodes[id]
	if node == nil {
		return nil, ErrNotExist
	}
	assets := node.files
	if images {
		assets = node.images
	}
	data, ok := assets[name]
	if !ok {
		return nil, ErrNotExist
	}
	return cloneBytes(data), nil
}

func (r *MemoryRepository) WriteFile(ctx context.Context, id NodeId, name string, data []byte) error {
	return r.writeAsset(ctx, id, name, data, false)
}

func (r *MemoryRepository) WriteImage(ctx context.Context, id NodeId, name string, data []byte) error {
	return r.writeAsset(ctx, id, name, data, true)
}

func (r *MemoryRepository) writeAsset(ctx context.Context, id NodeId, name string, data []byte, images bool) error {
	if err := ValidateAssetName(name); err != nil {
		return err
	}
	return r.updateNode(ctx, id, func(node *memoryNode) {
		if images {
			node.images[name] = cloneBytes(data)
		} else {
			node.files[name] = cloneBytes(data)
		}
	})
}

func (r *MemoryRepository) DeleteFile(ctx context.Context, id NodeId, name string) error {
	return r.deleteAsset(ctx, id, name, false)
}

func (r *MemoryRepository) DeleteImage(ctx context.Context, id NodeId, name string) error {
	return r.deleteAsset(ctx, id, name, true)
}

func (r *MemoryRepository) deleteAsset(ctx context.Context, id NodeId, name string, images bool) error {
	if err := ValidateAssetName(name); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	node := r.nodes[id]
	if node == nil {
		return ErrNotExist
	}
	assets := node.files
	if images {
		assets = node.images
	}
	if _, ok := assets[name]; !ok {
		return ErrNotExist
	}
	delete(assets, name)
	return nil
}

func (r *MemoryRepository) WithKegAtomicWrite(ctx context.Context, fn func(context.Context) error) error {
	return r.WithKegWrite(ctx, func(writeCtx context.Context) error {
		r.mu.Lock()
		backup := r.cloneStateLocked()
		r.mu.Unlock()
		if err := fn(writeCtx); err != nil {
			r.mu.Lock()
			r.restoreStateLocked(backup)
			r.mu.Unlock()
			return err
		}
		return nil
	})
}

type memoryState struct {
	nodes    map[NodeId]*memoryNode
	reserved map[NodeId]struct{}
	indexes  map[string][]byte
	settings []byte
	schemas  map[string][]byte
	snaps    map[NodeId][]memorySnapshot
	locks    map[NodeId]LockInfo
}

func (r *MemoryRepository) cloneStateLocked() memoryState {
	state := memoryState{
		nodes: make(map[NodeId]*memoryNode), reserved: make(map[NodeId]struct{}),
		indexes: make(map[string][]byte), settings: cloneBytes(r.settings),
		schemas: make(map[string][]byte), snaps: make(map[NodeId][]memorySnapshot),
		locks: make(map[NodeId]LockInfo),
	}
	for id, node := range r.nodes {
		state.nodes[id] = cloneMemoryNode(node)
	}
	for id := range r.reserved {
		state.reserved[id] = struct{}{}
	}
	for name, data := range r.indexes {
		state.indexes[name] = cloneBytes(data)
	}
	for name, data := range r.schemas {
		state.schemas[name] = cloneBytes(data)
	}
	for id, snaps := range r.snaps {
		state.snaps[id] = cloneMemorySnapshots(snaps)
	}
	for id, info := range r.locks {
		state.locks[id] = info
	}
	return state
}

func (r *MemoryRepository) restoreStateLocked(state memoryState) {
	r.nodes, r.reserved, r.indexes = state.nodes, state.reserved, state.indexes
	r.settings, r.schemas, r.snaps, r.locks = state.settings, state.schemas, state.snaps, state.locks
}

func (r *MemoryRepository) AppendSnapshot(ctx context.Context, id NodeId, in SnapshotWrite) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return Snapshot{}, ErrNotExist
	}
	history := r.snaps[id]
	parent := RevisionID(0)
	if len(history) > 0 {
		parent = history[len(history)-1].snapshot.ID
	}
	if in.ExpectedParent != parent {
		return Snapshot{}, ErrConflict
	}
	content := cloneBytes(in.Content.Data)
	if in.Content.Kind == SnapshotContentKindPatch {
		var base []byte
		for _, record := range history {
			if record.snapshot.ID == in.Content.Base {
				base = record.content
				break
			}
		}
		var err error
		content, err = applySnapshotPatch(r.runtime.Hasher(), base, in.Content.Data)
		if err != nil {
			// Repository contract tests may provide already materialized content;
			// LocalKeg supplies encoded line-patch data in normal operation.
			content = cloneBytes(in.Content.Data)
		}
	}
	createdAt := in.CreatedAt
	if createdAt.IsZero() {
		createdAt = r.runtime.Clock().Now()
	}
	statsBytes, err := snapshotStatsToBytes(in.Stats)
	if err != nil {
		return Snapshot{}, err
	}
	contentHash, metaHash, statsHash := snapshotWriteHashes(r.runtime, content, in.Meta, statsBytes)
	snapshot := Snapshot{
		ID: RevisionID(len(history) + 1), Node: id, Parent: parent,
		CreatedAt: createdAt, Message: in.Message, ContentHash: contentHash,
		MetaHash: metaHash, StatsHash: statsHash,
		IsCheckpoint: in.Content.Kind != SnapshotContentKindPatch,
	}
	stats, err := cloneStats(ctx, in.Stats)
	if err != nil {
		return Snapshot{}, err
	}
	r.snaps[id] = append(history, memorySnapshot{snapshot: snapshot, content: content, meta: cloneBytes(in.Meta), stats: stats})
	return snapshot, nil
}

func (r *MemoryRepository) GetSnapshot(ctx context.Context, id NodeId, rev RevisionID, opts SnapshotReadOptions) (Snapshot, []byte, []byte, *NodeStats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return Snapshot{}, nil, nil, nil, err
	}
	record, ok := r.snapshotLocked(id, rev)
	if !ok {
		return Snapshot{}, nil, nil, nil, ErrNotExist
	}
	var content []byte
	if opts.ResolveContent {
		content = cloneBytes(record.content)
	}
	stats, err := cloneStats(ctx, record.stats)
	return record.snapshot, content, cloneBytes(record.meta), stats, err
}

func (r *MemoryRepository) ListSnapshots(ctx context.Context, id NodeId) ([]Snapshot, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	history := r.snaps[id]
	out := make([]Snapshot, len(history))
	for i := range history {
		out[i] = history[i].snapshot
	}
	return out, nil
}

func (r *MemoryRepository) ReadContentAt(ctx context.Context, id NodeId, rev RevisionID) ([]byte, error) {
	_, content, _, _, err := r.GetSnapshot(ctx, id, rev, SnapshotReadOptions{ResolveContent: true})
	return content, err
}

func (r *MemoryRepository) RestoreSnapshot(ctx context.Context, id NodeId, rev RevisionID, createRestoreSnapshot bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.snapshotLocked(id, rev)
	if !ok {
		return ErrNotExist
	}
	node := r.nodes[id]
	if node == nil {
		return ErrNotExist
	}
	node.content = cloneBytes(record.content)
	node.meta = cloneBytes(record.meta)
	node.stats, _ = cloneStats(ctx, record.stats)
	if createRestoreSnapshot {
		history := r.snaps[id]
		parent := history[len(history)-1].snapshot.ID
		statsBytes, _ := snapshotStatsToBytes(record.stats)
		contentHash, metaHash, statsHash := snapshotWriteHashes(r.runtime, record.content, record.meta, statsBytes)
		snapshot := Snapshot{
			ID: RevisionID(len(history) + 1), Node: id, Parent: parent,
			CreatedAt: r.runtime.Clock().Now(), Message: fmt.Sprintf("restore from rev %d", rev),
			ContentHash: contentHash, MetaHash: metaHash, StatsHash: statsHash, IsCheckpoint: true,
		}
		r.snaps[id] = append(history, memorySnapshot{snapshot: snapshot, content: cloneBytes(record.content), meta: cloneBytes(record.meta), stats: record.stats})
	}
	return nil
}

func (r *MemoryRepository) snapshotLocked(id NodeId, rev RevisionID) (memorySnapshot, bool) {
	for _, record := range r.snaps[id] {
		if record.snapshot.ID == rev {
			return record, true
		}
	}
	return memorySnapshot{}, false
}

func (r *MemoryRepository) corruptLatestSnapshot(id NodeId, mutate func(*Snapshot, *[]byte)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	history := r.snaps[id]
	if len(history) == 0 {
		return ErrNotExist
	}
	latest := &history[len(history)-1]
	mutate(&latest.snapshot, &latest.content)
	r.snaps[id] = history
	return nil
}

func (r *MemoryRepository) AcquireLock(ctx context.Context, id NodeId) (LockToken, error) {
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		r.mu.Lock()
		info := r.locks[id]
		if info.Token == "" || info.IsStale(r.runtime.Clock().Now()) {
			token := generateLockToken()
			r.locks[id] = LockInfo{Token: token, AcquiredAt: r.runtime.Clock().Now(), TTLSeconds: int(DefaultLockTTL.Seconds()), Holder: "memory-test"}
			r.mu.Unlock()
			return token, nil
		}
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%w: %w", ErrLockTimeout, ctx.Err())
		case <-ticker.C:
		}
	}
}

func (r *MemoryRepository) ReleaseLock(ctx context.Context, id NodeId, token LockToken) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.locks[id]
	if !ok || info.Token == "" {
		return ErrNotLocked
	}
	if info.Token != token {
		return ErrLockTokenMismatch
	}
	delete(r.locks, id)
	return nil
}

func (r *MemoryRepository) LockStatus(ctx context.Context, id NodeId) (LockInfo, error) {
	if err := ctx.Err(); err != nil {
		return LockInfo{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	info := r.locks[id]
	if info.Token == "" || info.IsStale(r.runtime.Clock().Now()) {
		delete(r.locks, id)
		return LockInfo{}, nil
	}
	return info, nil
}

func (r *MemoryRepository) ForceReleaseLock(ctx context.Context, id NodeId) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.locks, id)
	return nil
}

func (r *MemoryRepository) Watch(ctx context.Context, ids ...NodeId) (<-chan NodeEvent, error) {
	watcher := &memoryWatcher{ids: make(map[NodeId]struct{}), ch: make(chan NodeEvent, 32)}
	for _, id := range ids {
		watcher.ids[id] = struct{}{}
	}
	r.watchersMu.Lock()
	r.watchers[watcher] = struct{}{}
	r.watchersMu.Unlock()
	go func() {
		<-ctx.Done()
		r.watchersMu.Lock()
		if _, ok := r.watchers[watcher]; ok {
			delete(r.watchers, watcher)
			close(watcher.ch)
		}
		r.watchersMu.Unlock()
	}()
	return watcher.ch, nil
}

func (r *MemoryRepository) Emit(event NodeEvent) {
	r.watchersMu.Lock()
	defer r.watchersMu.Unlock()
	for watcher := range r.watchers {
		if len(watcher.ids) > 0 {
			if _, ok := watcher.ids[event.NodeID]; !ok {
				continue
			}
		}
		select {
		case watcher.ch <- event:
		default:
		}
	}
}

func newMemoryNode() *memoryNode {
	return &memoryNode{files: make(map[string][]byte), images: make(map[string][]byte)}
}

func cloneBytes(data []byte) []byte {
	return append([]byte(nil), data...)
}

type snapshotHasher interface {
	Hash([]byte) string
}

type textPatch struct {
	BaseHash string        `json:"base_hash,omitempty"`
	Ops      []textPatchOp `json:"ops"`
}

type textPatchOp struct {
	Type  string   `json:"type"`
	Count int      `json:"count,omitempty"`
	Lines []string `json:"lines,omitempty"`
}

func applySnapshotPatch(hasher snapshotHasher, base, raw []byte) ([]byte, error) {
	var patch textPatch
	if err := json.Unmarshal(raw, &patch); err != nil {
		return nil, err
	}
	if patch.BaseHash != "" && patch.BaseHash != hashSnapshotBytes(hasher, base) {
		return nil, ErrConflict
	}
	lines := strings.SplitAfter(string(base), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var out strings.Builder
	index := 0
	for _, op := range patch.Ops {
		switch op.Type {
		case "equal":
			if index+op.Count > len(lines) {
				return nil, ErrInvalid
			}
			for _, line := range lines[index : index+op.Count] {
				out.WriteString(line)
			}
			index += op.Count
		case "delete":
			if index+op.Count > len(lines) {
				return nil, ErrInvalid
			}
			index += op.Count
		case "insert":
			for _, line := range op.Lines {
				out.WriteString(line)
			}
		default:
			return nil, ErrInvalid
		}
	}
	if index != len(lines) {
		return nil, ErrInvalid
	}
	return []byte(out.String()), nil
}

func hashSnapshotBytes(hasher snapshotHasher, data []byte) string {
	if hasher == nil || len(data) == 0 {
		return ""
	}
	return hasher.Hash(data)
}

func snapshotStatsToBytes(stats *NodeStats) ([]byte, error) {
	if stats == nil {
		return nil, nil
	}
	return stats.ToJSON()
}

func snapshotWriteHashes(rt *toolkit.Runtime, content, meta, stats []byte) (string, string, string) {
	return hashSnapshotBytes(rt.Hasher(), content), hashSnapshotBytes(rt.Hasher(), meta), hashSnapshotBytes(rt.Hasher(), stats)
}

func generateLockToken() LockToken {
	var uuid [16]byte
	_, _ = rand.Read(uuid[:])
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	uuid[8] = (uuid[8] & 0x3f) | 0x80
	return LockToken(fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16]))
}

func cloneMemoryNode(node *memoryNode) *memoryNode {
	copyNode := newMemoryNode()
	copyNode.content, copyNode.meta = cloneBytes(node.content), cloneBytes(node.meta)
	copyNode.stats, _ = cloneStats(context.Background(), node.stats)
	for name, data := range node.files {
		copyNode.files[name] = cloneBytes(data)
	}
	for name, data := range node.images {
		copyNode.images[name] = cloneBytes(data)
	}
	return copyNode
}

func cloneMemorySnapshots(in []memorySnapshot) []memorySnapshot {
	out := make([]memorySnapshot, len(in))
	for i := range in {
		out[i] = memorySnapshot{snapshot: in[i].snapshot, content: cloneBytes(in[i].content), meta: cloneBytes(in[i].meta)}
		out[i].stats, _ = cloneStats(context.Background(), in[i].stats)
	}
	return out
}

func cloneStats(ctx context.Context, stats *NodeStats) (*NodeStats, error) {
	if stats == nil {
		return nil, nil
	}
	raw, err := stats.ToJSON()
	if err != nil {
		return nil, err
	}
	return ParseStats(ctx, raw)
}

var (
	_ Repository                  = (*MemoryRepository)(nil)
	_ RepositorySettingsDocuments = (*MemoryRepository)(nil)
	_ RepositoryAtomicWrite       = (*MemoryRepository)(nil)
	_ RepositoryConcurrentAccess  = (*MemoryRepository)(nil)
	_ RepositoryBatchRead         = (*MemoryRepository)(nil)
	_ RepositoryFiles             = (*MemoryRepository)(nil)
	_ RepositoryImages            = (*MemoryRepository)(nil)
	_ RepositorySchemas           = (*MemoryRepository)(nil)
	_ RepositorySnapshots         = (*MemoryRepository)(nil)
	_ RepositoryLock              = (*MemoryRepository)(nil)
	_ RepositoryEvents            = (*MemoryRepository)(nil)
)
