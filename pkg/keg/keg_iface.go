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
// Capability errors: methods backed by optional storage features (schemas,
// files, images, snapshots, locks, and events) return ErrNotSupported when the
// backend lacks the capability.
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

	// Info returns the keg configuration and summary from one coherent
	// keg-wide read snapshot.
	Info(ctx context.Context) (*KegInfo, error)

	// ListSchemas returns the defined schema type names in lexicographic order.
	ListSchemas(ctx context.Context) ([]string, error)

	// ReadSchema returns the raw YAML definition for typeName.
	ReadSchema(ctx context.Context, typeName string) ([]byte, error)

	// WriteSchema validates and stores the YAML definition for typeName,
	// replacing any existing definition.
	WriteSchema(ctx context.Context, typeName string, data []byte) error

	// CreateSchema validates and stores the YAML definition for typeName only
	// when it does not exist. Concurrent creators are serialized so exactly one
	// succeeds and the others return ErrExist.
	CreateSchema(ctx context.Context, typeName string, data []byte) error

	// DeleteSchema removes the definition for typeName.
	DeleteSchema(ctx context.Context, typeName string) error

	// ValidateNode validates the stored content and metadata for id against its
	// declared schema without changing the node.
	ValidateNode(ctx context.Context, id NodeId) (*SchemaValidationResult, error)

	// ValidateNodePayload validates a proposed content/meta overlay for an
	// existing node without writing it. Fields not marked present are read from
	// the stored node.
	ValidateNodePayload(ctx context.Context, payload NodeValidationPayload) (*SchemaValidationResult, error)

	// Node lifecycle

	// Create allocates a node id and writes initial content, meta, and stats.
	Create(ctx context.Context, opts *CreateOptions) (CreateResult, error)

	// Next reserves and returns the next available node id. The reservation does
	// not become a complete node until content is written.
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

	// Commit promotes a temporary, code-backed node to a permanent numeric id.
	// It is a no-op for an already-permanent node.
	Commit(ctx context.Context, id NodeId) error

	// Node data

	// ReadNode returns the node's full state in one operation: content, raw
	// meta, stats, and asset name lists from one coherent read snapshot.
	ReadNode(ctx context.Context, id NodeId) (*NodeView, error)

	// OpenNode validates any held advisory lock against opts.LockToken,
	// optionally records an access touch, and returns one coherent node view.
	// If the operation fails after touching, the touch is rolled back.
	OpenNode(ctx context.Context, opts NodeOpenOptions) (*NodeView, error)

	// ReadNodes reads either opts.NodeIDs in caller order or the nodes selected
	// by opts.Query in dex order; the selectors are mutually exclusive. All
	// views come from one coherent keg snapshot. When Touch is set, either every
	// selected node is touched or all touch side effects are rolled back.
	ReadNodes(ctx context.Context, opts ReadNodesOptions) ([]NodeView, error)

	// UpdateNode validates advisory-lock ownership and an optional expected
	// content hash, then commits content, optional metadata, derived stats, and
	// dex state as one node update. It returns the resulting validation and hash.
	UpdateNode(ctx context.Context, opts NodeUpdateOptions) (*NodeUpdateResult, error)

	// ReplaceNodesWithRedirects replaces nodes with redirect stubs in input
	// order, checking each optional expected hash. It stops at the first failure
	// and returns both the successful prefix and a Failure; completed replacements
	// are not rolled back.
	ReplaceNodesWithRedirects(ctx context.Context, redirects []NodeRedirect) (ReplaceNodesWithRedirectsResult, error)

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

	// DexArtifacts returns every raw dex artifact from one coherent generation,
	// materializing snapshot-derived indexes first when necessary.
	DexArtifacts(ctx context.Context) (*DexArtifacts, error)

	// ListEntries returns dex entries, lexicographically sorted tags, and index
	// and repository counts from one coherent snapshot. A non-empty query filters
	// entries in dex order without changing the aggregate counts.
	ListEntries(ctx context.Context, opts ListEntriesOptions) (*ListEntriesResult, error)

	// ListView returns one fully resolved listing page: filtered by the query,
	// ordered, paged, and projected onto the requested field selectors. The
	// server owns the whole projection so a caller displaying metadata does not
	// read each node individually. Field resolution is best-effort: a node whose
	// metadata or stats cannot be read yields empty values rather than failing
	// the listing, because listings render from an index that may be stale.
	ListView(ctx context.Context, opts ListViewOptions) (*ListViewResult, error)

	// RelatedNodes returns the deduplicated union of links or backlinks for the
	// supplied nodes, ordered by node id. It fails if no ids are supplied, an id
	// is missing, or the direction is invalid.
	RelatedNodes(ctx context.Context, opts RelatedNodesOptions) ([]NodeIndexEntry, error)

	// Graph returns a deterministic graph projection of the keg's dex.
	//
	// Deprecated: Tapper Hub supersedes local graph rendering.
	Graph(ctx context.Context) (*GraphView, error)

	// Doctor inspects configuration, content, links, metadata, stats, and schema
	// validation and returns deterministic diagnostic issues without mutating the
	// keg.
	Doctor(ctx context.Context) ([]DoctorIssue, error)

	// RemoveNodes removes the deduplicated union of explicit ids and query
	// matches in ascending node-id order. It stops at the first failure and
	// returns the successful prefix plus a Failure; completed removals and their
	// inbound-link rewrites are not rolled back.
	RemoveNodes(ctx context.Context, opts RemoveNodesOptions) (RemoveNodesResult, error)

	// ValidateNodes validates opts.NodeIDs in caller order, or every node in
	// repository order when none are supplied. It stops at the first operational
	// error and returns results only after every selected node is validated.
	ValidateNodes(ctx context.Context, opts ValidateNodesOptions) ([]SchemaValidationResult, error)

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

	// ListFiles returns file attachment names in lexicographic order.
	ListFiles(ctx context.Context, id NodeId) ([]string, error)

	// ReadFile returns the named file attachment bytes.
	ReadFile(ctx context.Context, id NodeId, name string) ([]byte, error)

	// WriteFile stores or replaces a named file attachment.
	WriteFile(ctx context.Context, id NodeId, name string, data []byte) error

	// DeleteFile removes a named file attachment.
	DeleteFile(ctx context.Context, id NodeId, name string) error

	// ListImages returns image attachment names in lexicographic order.
	ListImages(ctx context.Context, id NodeId) ([]string, error)

	// ReadImage returns the named image attachment bytes.
	ReadImage(ctx context.Context, id NodeId, name string) ([]byte, error)

	// WriteImage validates and stores or replaces a named image attachment.
	WriteImage(ctx context.Context, id NodeId, name string, data []byte) error

	// DeleteImage removes a named image attachment.
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

	// ExportNodes returns a reader for a gzip-tar keg archive. Query matches are
	// unioned with explicit ids, and selected nodes are written in node-id order;
	// an empty selection exports every node. The archive reflects one coherent
	// read snapshot, and the caller must Close the returned reader.
	ExportNodes(ctx context.Context, opts ExportNodesOptions) (io.ReadCloser, error)

	// ImportNodes loads a keg-archive stream into the keg, replacing
	// existing nodes with matching ids, and rebuilds derived state.
	ImportNodes(ctx context.Context, r io.Reader, opts ImportNodesOptions) ([]ImportedNode, error)

	// Cross-process locks

	// Lock acquires a cross-process advisory lock on a node.
	Lock(ctx context.Context, id NodeId) (LockInfo, error)

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
	// Query selects additional nodes as a union with NodeIDs.
	Query string
	// SkipZeroNode excludes the keg root node from the selection.
	SkipZeroNode bool
	// WithHistory includes snapshot revisions.
	WithHistory bool
	// HistoryIfSupported omits history instead of failing when snapshots are unavailable.
	HistoryIfSupported bool
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
	// HistoryIfSupported omits archived history instead of failing when the
	// destination repository does not implement snapshots.
	HistoryIfSupported bool
	// SourceAlias, when set, rewrites relative links that point at
	// un-imported source nodes to keg:SourceAlias/N cross-keg links.
	SourceAlias string
	// TargetAlias, when set, rewrites keg:TargetAlias/N links in imported
	// content to relative ../N links.
	TargetAlias string
}

// ImportedNode maps an archive source id to the node id it landed on.
type ImportedNode struct {
	SourceID   string
	SourceHash string
	ID         NodeId
}
