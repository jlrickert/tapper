package keg

import (
	"context"
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

	// Node assets (images, attachments/items)

	// ListAssets lists asset names for a node and asset namespace kind.
	// Missing nodes should return a typed/sentinel not-exist error.
	ListAssets(ctx context.Context, id NodeId, kind AssetKind) ([]string, error)
	// WriteAsset stores a named asset payload for a node and asset kind.
	// Implementations may create required directories/containers as needed.
	WriteAsset(ctx context.Context, id NodeId, kind AssetKind, name string, data []byte) error
	// DeleteAsset removes a named asset for a node and asset kind.
	// Missing nodes or missing assets should return typed/sentinel errors.
	DeleteAsset(ctx context.Context, id NodeId, kind AssetKind, name string) error

	// Indexes

	// GetIndex reads an index artifact by name (for example "nodes.tsv").
	// Returned bytes should be treated as immutable by callers.
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

	// Repository config

	// ReadConfig reads repository-level keg configuration.
	// Missing config should return typed/sentinel not-exist errors.
	ReadConfig(ctx context.Context) (*Config, error)
	// WriteConfig persists repository-level keg configuration.
	// Implementations should perform atomic writes when possible.
	WriteConfig(ctx context.Context, config *Config) error
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
