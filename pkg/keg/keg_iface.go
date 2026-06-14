package keg

import (
	"context"
	"io"
)

// Keg is the single-keg business API. It is the abstraction boundary between
// callers (the Tap layer, the hub's HTTP handlers) and keg storage: every
// method is one logical operation, and implementations own their orchestration
// internally (locking discipline, dex/index maintenance, stats touching).
//
// Two implementations exist:
//
//   - LocalKeg orchestrates a Repository (FsRepo, MemoryRepo, or the hub's
//     PgRepo) and maintains derived state itself.
//   - RemoteKeg speaks the tapper-hub operation API; each method is a single
//     HTTP round trip and all orchestration happens server-side.
//
// Capability errors: methods backed by optional storage features (files,
// images, snapshots, locks, events) return ErrNotSupported when the backend
// lacks the capability.
type Keg interface {
	// Target returns the keg's resolved location, or nil for anonymous
	// (memory-backed) kegs.
	Target() *Target

	// Init bootstraps an empty keg: config file plus zero node. Remote kegs
	// are created through the hub's keg-creation endpoint instead and return
	// ErrNotSupported.
	Init(ctx context.Context) error

	// Config returns the keg-level configuration (the `keg` file).
	Config(ctx context.Context) (*Config, error)

	// SetConfig replaces the keg configuration with the supplied raw YAML.
	// Raw bytes preserve user formatting for round-trip editing.
	SetConfig(ctx context.Context, data []byte) error

	// Node lifecycle

	// Create allocates a node id and writes initial content, meta, and stats.
	Create(ctx context.Context, opts *CreateOptions) (NodeId, error)

	// Next reports the next node id that Create would allocate.
	Next(ctx context.Context) (NodeId, error)

	// ListNodes returns all node ids present in the keg.
	ListNodes(ctx context.Context) ([]NodeId, error)

	// NodeExists reports whether id is a fully written node (content
	// present), as opposed to a bare reservation directory.
	NodeExists(ctx context.Context, id NodeId) (bool, error)

	// Move relocates src to dst and rewrites inbound links. It returns the
	// ids of nodes whose content was rewritten to follow the move.
	Move(ctx context.Context, src NodeId, dst NodeId) ([]NodeId, error)

	// Remove deletes a node and rewrites or drops inbound links. It returns
	// the ids of nodes whose content was rewritten.
	Remove(ctx context.Context, id NodeId) ([]NodeId, error)

	// Commit creates a snapshot revision of the node's current state.
	Commit(ctx context.Context, id NodeId) error

	// Node data

	// ReadNode returns the node's full state in one operation: content, raw
	// meta, stats, and asset name lists.
	ReadNode(ctx context.Context, id NodeId) (*NodeView, error)

	// GetContent returns the node's primary content (README.md).
	GetContent(ctx context.Context, id NodeId) ([]byte, error)

	// SetContent replaces the node's primary content and refreshes derived
	// state (stats, dex) as the implementation requires.
	SetContent(ctx context.Context, id NodeId, data []byte) error

	// GetMeta returns the node's parsed metadata.
	GetMeta(ctx context.Context, id NodeId) (*NodeMeta, error)

	// GetMetaRaw returns the node's metadata bytes exactly as stored,
	// preserving formatting for round-trip editing.
	GetMetaRaw(ctx context.Context, id NodeId) ([]byte, error)

	// SetMeta replaces the node's metadata.
	SetMeta(ctx context.Context, id NodeId, meta *NodeMeta) error

	// GetStats returns the node's programmatic stats.
	GetStats(ctx context.Context, id NodeId) (*NodeStats, error)

	// Touch marks the node accessed, updating access stats.
	Touch(ctx context.Context, id NodeId) error

	// Listing, query, and index

	// Dex returns the keg's current index aggregate. The returned dex
	// reflects committed state at call time (always-fresh semantics).
	Dex(ctx context.Context) (*Dex, error)

	// Query evaluates a boolean query expression (tags, key=value attribute
	// predicates, .field stats predicates) and returns matching index
	// entries in dex order.
	Query(ctx context.Context, opts QueryOptions) ([]NodeIndexEntry, error)

	// Grep scans node content for a regular expression and returns per-node
	// line matches in dex order.
	Grep(ctx context.Context, opts GrepOptions) ([]GrepMatch, error)

	// Index rebuilds all dex indexes from node state.
	Index(ctx context.Context, opts IndexOptions) error

	// ListIndexes returns available index artifact names.
	ListIndexes(ctx context.Context) ([]string, error)

	// ReadIndex returns a raw index artifact by name (e.g. "nodes.tsv").
	ReadIndex(ctx context.Context, name string) ([]byte, error)

	// Summary returns keg-level diagnostics: node count and asset totals.
	Summary(ctx context.Context) (*KegSummary, error)

	// Files and images

	ListFiles(ctx context.Context, id NodeId) ([]string, error)
	ReadFile(ctx context.Context, id NodeId, name string) ([]byte, error)
	WriteFile(ctx context.Context, id NodeId, name string, data []byte) error
	DeleteFile(ctx context.Context, id NodeId, name string) error

	ListImages(ctx context.Context, id NodeId) ([]string, error)
	ReadImage(ctx context.Context, id NodeId, name string) ([]byte, error)
	WriteImage(ctx context.Context, id NodeId, name string, data []byte) error
	DeleteImage(ctx context.Context, id NodeId, name string) error

	// Snapshots

	// AppendSnapshot records the node's current state as a new revision.
	AppendSnapshot(ctx context.Context, id NodeId, msg string) (Snapshot, error)

	// ListSnapshots returns the node's revisions in order.
	ListSnapshots(ctx context.Context, id NodeId) ([]Snapshot, error)

	// GetSnapshot returns revision metadata and, per opts, resolved payloads.
	GetSnapshot(ctx context.Context, id NodeId, rev RevisionID, opts SnapshotReadOptions) (Snapshot, []byte, []byte, *NodeStats, error)

	// ReadContentAt reconstructs node content at a revision.
	ReadContentAt(ctx context.Context, id NodeId, rev RevisionID) ([]byte, error)

	// RestoreSnapshot restores live node state to a revision.
	RestoreSnapshot(ctx context.Context, id NodeId, rev RevisionID) error

	// Bulk transfer

	// ExportNodes streams a keg-archive (gzip tar) of the selected nodes.
	// The caller must Close the returned reader.
	ExportNodes(ctx context.Context, opts ExportNodesOptions) (io.ReadCloser, error)

	// ImportNodes loads a keg-archive stream into the keg, replacing
	// existing nodes with matching ids, and rebuilds derived state.
	ImportNodes(ctx context.Context, r io.Reader, opts ImportNodesOptions) ([]ImportedNode, error)

	// Cross-process locks

	// Lock acquires a cross-process advisory lock on a node.
	Lock(ctx context.Context, id NodeId) (LockToken, error)

	// Unlock releases a lock acquired by Lock; the token must match.
	Unlock(ctx context.Context, id NodeId, token LockToken) error

	// LockStatus reports the node's current lock state; a zero LockInfo
	// means unheld.
	LockStatus(ctx context.Context, id NodeId) (LockInfo, error)

	// ForceUnlock removes a lock regardless of token ownership.
	ForceUnlock(ctx context.Context, id NodeId) error

	// Events

	// Watch streams node change events until ctx is canceled. With no ids,
	// all nodes are watched.
	Watch(ctx context.Context, ids ...NodeId) (<-chan NodeEvent, error)
}

// NodeView is a node's full state assembled in one operation.
type NodeView struct {
	ID NodeId
	// Content is the node's primary content (README.md).
	Content []byte
	// Meta is the raw metadata bytes as stored (may be empty).
	Meta []byte
	// Stats is the parsed programmatic stats (zero-value when absent).
	Stats *NodeStats
	// Files and Images list asset names; nil when the backend lacks the
	// capability.
	Files  []string
	Images []string
}

// QueryOptions configures Keg.Query.
type QueryOptions struct {
	// Expr is the boolean query expression, e.g. "project and .updated>2026-01-01".
	Expr string
}

// GrepOptions configures Keg.Grep.
type GrepOptions struct {
	// Pattern is a Go regular expression matched against content lines.
	Pattern string
	// IgnoreCase makes the match case-insensitive.
	IgnoreCase bool
	// MaxLines caps matched lines per node. 0 means no cap.
	MaxLines int
}

// GrepMatch reports one node's content matches for Keg.Grep.
type GrepMatch struct {
	Entry NodeIndexEntry
	// Lines are rendered "lineno:text" match lines.
	Lines []string
}

// AssetSummary aggregates one asset kind for KegSummary.
type AssetSummary struct {
	Supported       bool `json:"supported" yaml:"supported"`
	NodesWithAssets int  `json:"nodes_with_assets" yaml:"nodes_with_assets"`
	TotalAssets     int  `json:"total_assets" yaml:"total_assets"`
}

// KegSummary is keg-level diagnostic data returned by Keg.Summary.
type KegSummary struct {
	NodeCount int          `json:"node_count" yaml:"node_count"`
	Files     AssetSummary `json:"assets" yaml:"assets"`
	Images    AssetSummary `json:"images" yaml:"images"`
}

// ExportNodesOptions configures Keg.ExportNodes.
type ExportNodesOptions struct {
	// NodeIDs selects nodes to export; empty exports every node.
	NodeIDs []NodeId
	// WithHistory includes snapshot revisions.
	WithHistory bool
	// WithAssets includes per-node files and images.
	WithAssets bool
	// Source labels the archive manifest with the origin keg reference.
	Source string
}

// ImportNodesOptions configures Keg.ImportNodes.
type ImportNodesOptions struct {
	// AssignNewIDs allocates fresh sequential node ids for the archive's
	// nodes instead of landing them on their archive ids. Links between
	// imported nodes are rewritten to the new ids.
	AssignNewIDs bool
	// SourceAlias, when set, rewrites relative links that point at
	// un-imported source nodes to keg:SourceAlias/N cross-keg links.
	SourceAlias string
	// TargetAlias, when set, rewrites keg:TargetAlias/N links in imported
	// content to relative ../N links.
	TargetAlias string
}

// ImportedNode maps an archive source id to the node id it landed on.
type ImportedNode struct {
	SourceID string
	ID       NodeId
}
