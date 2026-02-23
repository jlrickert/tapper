package keg

import (
	"context"
	"time"
)

// AssetKind identifies an asset namespace for a node.
type AssetKind string

const (
	AssetKindImage AssetKind = "image"
	AssetKindItem  AssetKind = "item"
)

// Repository is the storage backend contract used by KEG. Implementations are
// responsible for moving node data between storage and the service layer.
type Repository interface {
	// Backend identity

	// Name returns a short, human-friendly backend identifier.
	Name() string

	// Node lifecycle

	// HasNode reports whether id exists as a node in the backend.
	// Missing nodes should return (false, nil). Backend/storage failures should
	// be returned as non-nil errors.
	HasNode(ctx context.Context, id NodeId) (bool, error)
	// Next returns the next available node id allocation candidate.
	// Implementations should honor ctx cancellation where applicable.
	Next(ctx context.Context) (NodeId, error)
	// ListNodes returns all node ids present in the backend.
	// Returned ids should be deterministic (stable ordering) when possible.
	ListNodes(ctx context.Context) ([]NodeId, error)
	// MoveNode renames or relocates a node from id to dst.
	// Implementations should return typed/sentinel errors when source is missing
	// or destination already exists.
	MoveNode(ctx context.Context, id NodeId, dst NodeId) error
	// DeleteNode removes the node and all associated persisted data.
	// If id does not exist, implementations should return a typed/sentinel
	// not-exist error.
	DeleteNode(ctx context.Context, id NodeId) error

	// Node primary data

	// WithNodeLock executes fn while holding an exclusive lock for node id.
	// Implementations should block until the lock is acquired or ctx is
	// canceled, and must release the lock after fn returns.
	WithNodeLock(ctx context.Context, id NodeId, fn func(context.Context) error) error
	// ReadContent reads the primary node content bytes (for example README.md).
	// Missing nodes should return a typed/sentinel not-exist error.
	ReadContent(ctx context.Context, id NodeId) ([]byte, error)
	// WriteContent writes primary node content bytes for id.
	// Implementations should perform atomic writes when possible.
	WriteContent(ctx context.Context, id NodeId, data []byte) error
	// ReadMeta reads raw node metadata bytes (for example meta.yaml).
	// Missing nodes should return a typed/sentinel not-exist error.
	ReadMeta(ctx context.Context, id NodeId) ([]byte, error)
	// WriteMeta writes raw node metadata bytes.
	// Implementations should preserve atomicity when possible.
	WriteMeta(ctx context.Context, id NodeId, data []byte) error
	// ReadStats returns parsed programmatic node stats for id.
	// Backends that persist stats inside meta.yaml should parse and return those
	// fields while preserving any manual metadata concerns at higher layers.
	ReadStats(ctx context.Context, id NodeId) (*NodeStats, error)
	// WriteStats writes programmatic node stats for id.
	// Implementations should preserve manually edited metadata fields when stats
	// and metadata share a storage representation.
	WriteStats(ctx context.Context, id NodeId, stats *NodeStats) error

	// Indexes

	// GetIndex reads an index artifact by name (for example "nodes.tsv").
	// Callers should treat returned bytes as immutable.
	GetIndex(ctx context.Context, name string) ([]byte, error)
	// WriteIndex writes an index artifact by name.
	// Implementations should prefer atomic file replacement semantics.
	WriteIndex(ctx context.Context, name string, data []byte) error
	// ListIndexes returns available index artifact names.
	// Results should be deterministic when possible.
	ListIndexes(ctx context.Context) ([]string, error)
	// ClearIndexes removes or resets index artifacts in the backend.
	// This method should be idempotent and context-aware.
	ClearIndexes(ctx context.Context) error

	// Repository config. This is the keg file

	// ReadConfig reads repository-level keg configuration.
	// Missing config should return typed/sentinel not-exist errors.
	ReadConfig(ctx context.Context) (*Config, error)
	// WriteConfig persists repository-level keg configuration.
	// Implementations should perform atomic writes when possible.
	WriteConfig(ctx context.Context, config *Config) error
}

// RepositoryFiles provides optional per-node file attachment access.
type RepositoryFiles interface {
	// ListFiles lists file attachment names for a node.
	ListFiles(ctx context.Context, id NodeId) ([]string, error)
	// WriteFile stores a file attachment for a node.
	WriteFile(ctx context.Context, id NodeId, name string, data []byte) error
	// DeleteFile removes a file attachment from a node.
	DeleteFile(ctx context.Context, id NodeId, name string) error
}

// RepositoryImages provides optional per-node image access.
type RepositoryImages interface {
	// ListImages lists image names for a node.
	ListImages(ctx context.Context, id NodeId) ([]string, error)
	// WriteImage stores an image payload for a node.
	WriteImage(ctx context.Context, id NodeId, name string, data []byte) error
	// DeleteImage removes an image from a node.
	DeleteImage(ctx context.Context, id NodeId, name string) error
}

type RevisionID int64

// SnapshotContentKind describes how snapshot content bytes are stored.
type SnapshotContentKind string

const (
	// SnapshotContentKindPatch stores content as a diff from a base revision.
	SnapshotContentKindPatch SnapshotContentKind = "patch"
	// SnapshotContentKindFull stores full reconstructed content bytes.
	SnapshotContentKindFull SnapshotContentKind = "full"
)

type Snapshot struct {
	ID        RevisionID
	Node      NodeId
	Parent    RevisionID // 0 for root
	CreatedAt time.Time
	Message   string

	// Integrity + retrieval hints
	ContentHash  string
	MetaHash     string
	StatsHash    string
	IsCheckpoint bool // full content stored instead of patch
}

// SnapshotContentWrite describes content payload for a new snapshot revision.
type SnapshotContentWrite struct {
	Kind SnapshotContentKind
	Base RevisionID

	// Algorithm identifies the patch format, for example "xdiff-v1".
	Algorithm string
	Data      []byte

	// Hash is the digest of fully materialized content at this revision.
	Hash string
}

// SnapshotWrite describes append parameters for a new node snapshot.
type SnapshotWrite struct {
	ExpectedParent RevisionID
	Message        string

	Meta  []byte
	Stats *NodeStats

	Content SnapshotContentWrite
}

// SnapshotReadOptions configures how snapshots are loaded.
type SnapshotReadOptions struct {
	// ResolveContent reconstructs full content bytes for the selected revision.
	ResolveContent bool
}

// RepositorySnapshots provides revision-based history operations.
type RepositorySnapshots interface {
	// AppendSnapshot appends a new revision with optimistic parent check.
	AppendSnapshot(ctx context.Context, id NodeId, in SnapshotWrite) (Snapshot, error)

	// GetSnapshot returns snapshot metadata and optional state payloads.
	// When opts.ResolveContent is true, returned content must be fully materialized.
	GetSnapshot(ctx context.Context, id NodeId, rev RevisionID, opts SnapshotReadOptions) (snap Snapshot, content []byte, meta []byte, stats *NodeStats, err error)

	// ListSnapshots returns revisions for a node in deterministic order.
	ListSnapshots(ctx context.Context, id NodeId) ([]Snapshot, error)

	// ReadContentAt reconstructs content at a specific revision.
	ReadContentAt(ctx context.Context, id NodeId, rev RevisionID) ([]byte, error)

	// RestoreSnapshot restores live node state to rev. Implementations may append
	// a restore snapshot when createRestoreSnapshot is true.
	RestoreSnapshot(ctx context.Context, id NodeId, rev RevisionID, createRestoreSnapshot bool) error
}

type nodeLockContextKey struct{}
type nodeLockSet map[NodeId]struct{}

func lockNodeKey(id NodeId) NodeId {
	return NodeId{ID: id.ID, Code: id.Code}
}

func contextWithNodeLock(ctx context.Context, id NodeId) context.Context {
	key := lockNodeKey(id)
	current, _ := ctx.Value(nodeLockContextKey{}).(nodeLockSet)
	next := make(nodeLockSet, len(current)+1)
	for existing := range current {
		next[existing] = struct{}{}
	}
	next[key] = struct{}{}
	return context.WithValue(ctx, nodeLockContextKey{}, next)
}

func contextHasNodeLock(ctx context.Context, id NodeId) bool {
	key := lockNodeKey(id)
	current, _ := ctx.Value(nodeLockContextKey{}).(nodeLockSet)
	_, ok := current[key]
	return ok
}
