package keg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"gopkg.in/yaml.v3"
)

// Keg is a concrete high-level service providing KEG node operations backed by a
// Repository. It abstracts storage implementation details, allowing operations
// over nodes to work uniformly across memory, filesystem, and remote backends.
// Keg delegates low-level storage operations to its underlying repository and
// maintains an in-memory dex for indexing.
type Keg struct {
	// Target is the keg URL/location (nil for memory-backed kegs)
	Target *kegurl.Target
	// Repo is the storage backend implementation
	Repo Repository
	// Runtime provides clock/hash/fs helpers used by high-level keg operations.
	Runtime *toolkit.Runtime

	// dexMu guards lazy initialization of dex and the mtime/gen fields below.
	dexMu sync.Mutex
	// dex is an optional in-memory index of nodes, lazily loaded from repo
	dex *Dex
	// dexWriteGen is a monotonic counter bumped after every successful
	// Dex.Write by this process. Used for diagnostics.
	dexWriteGen uint64
	// dexLoadMtime records the ModTime of dex/nodes.tsv at the time the
	// cached dex was last loaded from disk. Used by dexStale() to detect
	// whether another process has modified the index files since this
	// process last read them.
	dexLoadMtime time.Time

	// configMu guards the read-modify-write cycle in UpdateConfig.
	configMu sync.Mutex

	// kegExistsVerified is set to true after the first successful
	// checkKegExists call. Once a keg is confirmed to exist, it won't
	// un-exist, so subsequent checks are skipped.
	kegExistsVerified atomic.Bool

	// extraDexOpts holds additional DexOptions injected by higher-level
	// packages (e.g. pkg/tapper) via SetExtraDexOpts. These are prepended
	// before WithConfig in dexOptions() so that, for example, a
	// WithQueryResolver is available when WithConfig creates
	// QueryFilteredIndex instances.
	extraDexOpts []DexOption
}

// Option is a functional option for configuring Keg behavior
type Option func(*Keg)

// SetExtraDexOpts stores additional DexOptions that will be included whenever
// the dex is loaded or refreshed. These options are prepended before
// WithConfig so that injected resolvers (e.g. WithQueryResolver) are available
// when WithConfig creates QueryFilteredIndex instances.
//
// This is the injection point for higher-level packages (e.g. pkg/tapper) to
// provide capabilities that pkg/keg cannot import directly.
func (k *Keg) SetExtraDexOpts(opts ...DexOption) {
	k.dexMu.Lock()
	defer k.dexMu.Unlock()
	k.extraDexOpts = opts
	// Invalidate the cached dex so the next access rebuilds with the new options.
	k.dex = nil
}

// NewKegFromTarget constructs a Keg from a kegurl.Target. It automatically
// selects the appropriate repository implementation based on the target's scheme:
//   - memory:// targets use an in-memory repository
//   - file:// targets use a filesystem repository
//   - http:// and https:// targets use an API repository (ApiRepo)
//   - registry targets use an API repository resolved from repo/user/keg fields
//
// Returns an error if the target scheme is not supported.
func NewKegFromTarget(ctx context.Context, target kegurl.Target, rt *toolkit.Runtime) (*Keg, error) {
	switch target.Scheme() {
	case kegurl.SchemeMemory:
		repo := NewMemoryRepo(rt)
		keg := Keg{Repo: repo, Runtime: rt}
		return &keg, nil
	case kegurl.SchemeFile:
		repo := FsRepo{
			Root:            filepath.Clean(target.Path()),
			ContentFilename: MarkdownContentFilename,
			MetaFilename:    YAMLMetaFilename,
			StatsFilename:   JSONStatsFilename,
			runtime:         rt,
		}
		keg := Keg{Target: &target, Repo: &repo, Runtime: rt}
		return &keg, nil
	case kegurl.SchemeHTTP, kegurl.SchemeHTTPs:
		token := resolveTargetToken(&target, rt)
		baseURL := strings.TrimRight(target.Url, "/")
		repo := NewApiRepo(baseURL, token)
		keg := Keg{Target: &target, Repo: repo, Runtime: rt}
		return &keg, nil
	case kegurl.SchemeRegistry:
		token := resolveTargetToken(&target, rt)
		// Build the API base URL from the registry repo, user, and keg fields.
		// Convention: https://<repo>/api/v1/kegs/@<user>/<keg>
		baseURL := fmt.Sprintf("https://%s/api/v1/kegs/@%s/%s",
			target.Repo, target.User, target.Keg)
		repo := NewApiRepo(baseURL, token)
		keg := Keg{Target: &target, Repo: repo, Runtime: rt}
		return &keg, nil
	}
	return nil, fmt.Errorf("unsupported target scheme: %s", target.Scheme())
}

// resolveTargetToken extracts the bearer token from a Target. It prefers
// TokenEnv (environment variable name) over a literal Token value. Returns an
// empty string when no credential is configured.
func resolveTargetToken(target *kegurl.Target, rt *toolkit.Runtime) string {
	if target.TokenEnv != "" {
		if v := rt.Get(target.TokenEnv); v != "" {
			return v
		}
	}
	return target.Token
}

// NewKeg returns a Keg service backed by the provided repository.
// Functional options can be provided to customize Keg behavior.
func NewKeg(repo Repository, rt *toolkit.Runtime, opts ...Option) *Keg {
	keg := &Keg{
		Repo:    repo,
		Runtime: rt,
	}
	for _, o := range opts {
		o(keg)
	}
	return keg
}

// RepoContainsKeg checks if a keg has been properly initialized within a repository.
// It verifies both that a keg config exists and that a zero node (node ID 0) is present.
// Returns true only if both conditions are met, indicating a fully initialized keg.
func RepoContainsKeg(ctx context.Context, repo Repository) (bool, error) {
	if repo == nil {
		return false, fmt.Errorf("no repository provided")
	}

	var configExists bool

	// Check for a config. If it is missing, keg is not initialized.
	_, err := repo.ReadConfig(ctx)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			configExists = false
		} else {
			return false, fmt.Errorf("failed to check config existence: %w", err)
		}
	} else {
		configExists = true
	}

	var zeroNodeExists bool

	// Ensure a zero node exists by attempting to read content for ID 0.
	_, err = repo.ReadContent(ctx, NodeId{ID: 0})
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			zeroNodeExists = false
		} else {
			return false, fmt.Errorf("failed to check zero node existence: %w", err)
		}
	} else {
		zeroNodeExists = true
	}
	return configExists && zeroNodeExists, nil
}

// Init initializes a new keg by creating the config file, zero node with default
// content, and updating the dex. It returns an error if the keg already exists.
// Init is idempotent in the sense that it checks for existing kegs first.
func (k *Keg) Init(ctx context.Context) error {
	if k == nil || k.Repo == nil {
		return fmt.Errorf("no repository configured")
	}

	// refuse to init when a keg already exists
	exists, err := RepoContainsKeg(ctx, k.Repo)
	if err != nil {
		return fmt.Errorf("failed to check keg existence: %w", err)
	}
	if exists {
		return fmt.Errorf("keg already exists: %w", ErrExist)
	}

	// Ensure we have a config file. UpdateConfig must be allowed to write the
	// repo-level config even when the keg is not fully initiated.
	cfg := NewConfig()
	if err := k.Repo.WriteConfig(ctx, cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	// Create the zero node as a special case during InitKeg. We do this here so
	// Create can continue to require an initiated keg.
	now := k.Runtime.Clock().Now()

	rawContent := RawZeroNodeContent
	zeroContent, _ := ParseContent(k.Runtime, []byte(rawContent), MarkdownContentFilename)

	m := NewMeta(ctx, now)
	stats := NewStats(now)
	// no attrs to apply for the zero node; leave as empty map
	_ = m.SetAttrs(ctx, nil)
	nodeData := &NodeData{ID: NodeId{ID: 0}, Content: zeroContent, Meta: m, Stats: stats}
	_ = nodeData.UpdateMeta(ctx, &now)
	nodeData.Stats.EnsureTimes(now)

	id := NodeId{ID: 0}

	if err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		if err := k.Repo.WriteContent(lockCtx, id, []byte(rawContent)); err != nil {
			return fmt.Errorf("InitKeg: write content to backend %s: %w", k.Repo.Name(), err)
		}
		if err := k.Repo.WriteMeta(lockCtx, id, []byte(m.ToYAML())); err != nil {
			return fmt.Errorf("InitKeg: write meta to backend %s: %w", k.Repo.Name(), err)
		}
		if err := k.Repo.WriteStats(lockCtx, id, stats); err != nil {
			return fmt.Errorf("InitKeg: write stats to backend %s: %w", k.Repo.Name(), err)
		}
		return nil
	}); err != nil {
		return err
	}

	nodeData.ID = id
	if err := k.addNodeToDex(ctx, nodeData, &now); err != nil {
		return fmt.Errorf("failed to index zero node: %w", err)
	}

	k.kegExistsVerified.Store(true)
	return nil
}

// Next reserves and returns the next available node ID from the repository.
func (k *Keg) Next(ctx context.Context) (NodeId, error) {
	return k.Repo.Next(ctx)
}

// CreateOptions specifies parameters for creating a new node
type CreateOptions struct {
	// Title is the human-readable title for the node
	Title string
	// Lead is a one-line summary
	Lead string
	// Tags are searchable labels for the node
	Tags []string
	// Body is the raw markdown content; if empty, default content is generated from Title/Lead
	Body []byte
	// Attrs are arbitrary key-value attributes attached to the node
	Attrs map[string]any
}

// Create creates a new node: allocates an ID, parses content, generates metadata,
// and indexes the node in the dex. The node is immediately persisted to the repository.
// If Body is empty, default markdown content is generated from Title and Lead.
func (k *Keg) Create(ctx context.Context, opts *CreateOptions) (NodeId, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return NodeId{}, fmt.Errorf("failed to create node: %w", err)
	}

	if opts == nil {
		opts = &CreateOptions{}
	}

	// Reserve next ID
	id, err := k.Repo.Next(ctx)
	if err != nil {
		return NodeId{}, fmt.Errorf("failed to allocate node id: %w", err)
	}

	now := k.Runtime.Clock().Now()

	var rawContent []byte
	if len(opts.Body) > 0 {
		rawContent = opts.Body
	} else {
		// Build default content/meta for a new node
		b := strings.Builder{}
		if opts.Title != "" {
			b.WriteString(fmt.Sprintf("# %s\n", opts.Title))
		} else {
			b.WriteString(fmt.Sprintf("# NodeId %s\n", id.Path()))
		}

		if opts.Lead != "" {
			b.WriteString(fmt.Sprintf("\n%s\n", opts.Lead))
		}
		rawContent = []byte(b.String())
	}

	content, err := ParseContent(k.Runtime, rawContent, MarkdownContentFilename)
	if err != nil {
		return NodeId{}, fmt.Errorf("invalid content: %w", err)
	}
	m := NewMeta(ctx, now)
	if len(opts.Attrs) > 0 {
		m.SetAttrs(ctx, opts.Attrs)
	}

	stats := NewStats(now)
	if len(opts.Tags) > 0 {
		m.SetTags(opts.Tags)
	}
	nodeData := &NodeData{ID: id, Content: content, Meta: m, Stats: stats}
	_ = nodeData.UpdateMeta(ctx, &now)
	nodeData.Stats.EnsureTimes(now)

	// Persist content and metadata atomically for this node.
	if err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		if err := k.Repo.WriteContent(lockCtx, id, []byte(content.Body)); err != nil {
			return fmt.Errorf("create: write content to backend %s: %w", k.Repo.Name(), err)
		}
		if err := k.Repo.WriteMeta(lockCtx, id, []byte(m.ToYAML())); err != nil {
			return fmt.Errorf("create: write meta to backend %s: %w", k.Repo.Name(), err)
		}
		if err := k.Repo.WriteStats(lockCtx, id, stats); err != nil {
			return fmt.Errorf("create: write stats to backend %s: %w", k.Repo.Name(), err)
		}
		return nil
	}); err != nil {
		return id, err
	}

	return id, k.addNodeToDex(ctx, nodeData, &now)
}

// Config returns the keg's configuration.
func (k *Keg) Config(ctx context.Context) (*Config, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve config: %w", err)
	}

	return k.Repo.ReadConfig(ctx)
}

// UpdateConfig reads the keg config, applies the provided mutation function,
// and writes the result back to the repository. This is the preferred way to
// modify keg configuration to ensure updates are atomically persisted.
func (k *Keg) UpdateConfig(ctx context.Context, f func(*Config)) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("unable to update config: %w", err)
	}

	k.configMu.Lock()
	defer k.configMu.Unlock()

	// Read config directly from the repository to allow InitKeg to create it when
	// the keg is not yet fully initiated.
	cfg, err := k.Repo.ReadConfig(ctx)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			cfg = NewConfig()
		} else {
			return fmt.Errorf("failed to read config: %w", err)
		}
	}
	f(cfg)
	if err := k.Repo.WriteConfig(ctx, cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

// SetConfig parses and writes keg configuration from raw bytes.
// Prefer UpdateConfig for most use cases as it handles read-modify-write atomically.
func (k *Keg) SetConfig(ctx context.Context, data []byte) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("unable to set config: %w", err)
	}
	cfg, err := ParseKegConfig(data)
	if err != nil {
		return fmt.Errorf("unable to parse config: %w", err)
	}
	if err := k.Repo.WriteConfig(ctx, cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

// GetContent retrieves the raw markdown content for a node.
func (k *Keg) GetContent(ctx context.Context, id NodeId) ([]byte, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve node content: %w", err)
	}

	b, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to read content: %w", err)
	}
	return b, nil
}

// setContentNoDex writes content for a node and updates its metadata by
// re-indexing the node locally, but does NOT write the dex to disk. This
// allows callers (e.g. Move, Remove) to batch multiple content changes and
// perform a single dex write at the end. Returns the updated NodeData if
// content changed, nil if content was identical, and any error encountered.
func (k *Keg) setContentNoDex(ctx context.Context, id NodeId, data []byte) (*NodeData, error) {
	var nodeData *NodeData
	err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		// Verify the node truly exists (has content) under the lock to
		// prevent resurrecting a concurrently removed node. HasNode
		// alone is not enough for FsRepo because WithNodeLock creates
		// the node directory as a side effect.
		exists, err := k.nodeExistsWithContent(lockCtx, id)
		if err != nil {
			return fmt.Errorf("unable to check node existence: %w", err)
		}
		if !exists {
			return fmt.Errorf("node %s not found: %w", id.Path(), ErrNotExist)
		}

		// Compare new content with existing on-disk content. Skip the
		// write entirely when bytes are identical.
		existing, readErr := k.Repo.ReadContent(lockCtx, id)
		if readErr == nil && bytes.Equal(existing, data) {
			return nil
		}

		if err := k.Repo.WriteContent(lockCtx, id, data); err != nil {
			return fmt.Errorf("unable to write content: %w", err)
		}
		updated, changed, err := k.indexNodeLocked(lockCtx, id)
		if err != nil {
			return err
		}
		if changed {
			nodeData = updated
		}
		return nil
	})
	return nodeData, err
}

// SetContent writes content for a node and updates its metadata by re-indexing.
// This ensures the node's title, lead, and other metadata are kept in sync with content changes.
func (k *Keg) SetContent(ctx context.Context, id NodeId, data []byte) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to set node content: %w", err)
	}

	nodeData, err := k.setContentNoDex(ctx, id, data)
	if err != nil {
		return err
	}
	if nodeData == nil {
		return nil
	}
	return k.writeNodeToDex(ctx, id, nodeData)
}

// GetMeta retrieves the parsed metadata for a node.
func (k *Keg) GetMeta(ctx context.Context, id NodeId) (*NodeMeta, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to get node meta: %w", err)
	}
	return k.getMeta(ctx, id)
}

// GetStats retrieves programmatic node stats for a node.
func (k *Keg) GetStats(ctx context.Context, id NodeId) (*NodeStats, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to get node stats: %w", err)
	}
	return k.getStats(ctx, id)
}

// SetMeta writes metadata for a node and updates the dex.
// If the new meta bytes are identical to the existing on-disk meta,
// the write and dex/config update are skipped entirely.
func (k *Keg) SetMeta(ctx context.Context, id NodeId, meta *NodeMeta) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to update node meta: %w", err)
	}

	var nodeData *NodeData
	err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		// Verify the node truly exists (has content) under the lock to
		// prevent resurrecting a concurrently removed node.
		exists, err := k.nodeExistsWithContent(lockCtx, id)
		if err != nil {
			return fmt.Errorf("unable to check node existence: %w", err)
		}
		if !exists {
			return fmt.Errorf("node %s not found: %w", id.Path(), ErrNotExist)
		}

		stats, err := k.getStats(lockCtx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return fmt.Errorf("failed to read node stats: %w", err)
		}
		if stats == nil {
			stats = &NodeStats{}
		}

		// Compare new meta with existing on-disk meta. Skip the write
		// and dex update when the bytes are identical.
		newMetaBytes := []byte(meta.ToYAML())
		existingMeta, readErr := k.Repo.ReadMeta(lockCtx, id)
		if readErr == nil && bytes.Equal(
			bytes.TrimSpace(existingMeta),
			bytes.TrimSpace(newMetaBytes),
		) {
			// Meta unchanged — skip write and dex update.
			return nil
		}

		if err := k.Repo.WriteMeta(lockCtx, id, newMetaBytes); err != nil {
			return fmt.Errorf("UpdateMeta: write meta to backend %s: %w", k.Repo.Name(), err)
		}
		if err := k.Repo.WriteStats(lockCtx, id, stats); err != nil {
			return fmt.Errorf("UpdateMeta: write stats to backend %s: %w", k.Repo.Name(), err)
		}

		// Read content so the dex entry has complete data (links, title
		// fallback). Without content, link indexes would be lost when
		// SetContent later skips due to unchanged hash.
		content, _ := k.getContent(lockCtx, id)

		nodeData = &NodeData{ID: id, Content: content, Meta: meta, Stats: stats}
		return nil
	})
	if err != nil {
		return err
	}

	// nodeData is nil when meta was unchanged — skip dex and config update.
	if nodeData == nil {
		return nil
	}

	now := k.Runtime.Clock().Now()
	return k.addNodeToDex(ctx, nodeData, &now)
}

// UpdateMeta reads the node's metadata, applies the provided mutation function,
// and writes the result back to the repository with dex updates.
func (k *Keg) UpdateMeta(ctx context.Context, id NodeId, f func(*NodeMeta)) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to update node meta: %w", err)
	}

	now := k.Runtime.Clock().Now()

	var nodeData *NodeData
	err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		// Verify the node truly exists (has content) under the lock to
		// prevent resurrecting a concurrently removed node.
		exists, exErr := k.nodeExistsWithContent(lockCtx, id)
		if exErr != nil {
			return fmt.Errorf("unable to check node existence: %w", exErr)
		}
		if !exists {
			return fmt.Errorf("node %s not found: %w", id.Path(), ErrNotExist)
		}

		m, stats, err := k.getMetaAndStats(lockCtx, id)
		if errors.Is(err, ErrNotExist) {
			m = NewMeta(lockCtx, now)
			stats = NewStats(now)
		} else if err != nil {
			return fmt.Errorf("failed to read node metadata: %w", err)
		}
		if stats == nil {
			stats = &NodeStats{}
		}

		f(m)

		if err := k.Repo.WriteMeta(lockCtx, id, []byte(m.ToYAML())); err != nil {
			return fmt.Errorf("UpdateMeta: write meta to backend %s: %w", k.Repo.Name(), err)
		}
		if err := k.Repo.WriteStats(lockCtx, id, stats); err != nil {
			return fmt.Errorf("UpdateMeta: write stats to backend %s: %w", k.Repo.Name(), err)
		}

		// Read content so the dex entry has complete data (links, title).
		content, _ := k.getContent(lockCtx, id)
		nodeData = &NodeData{ID: id, Content: content, Meta: m, Stats: stats}
		return nil
	})
	if err != nil {
		return err
	}
	if nodeData == nil {
		return nil
	}
	return k.addNodeToDex(ctx, nodeData, &now)
}

// Touch updates the access time of a node to the current time.
func (k *Keg) Touch(ctx context.Context, id NodeId) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to touch node: %w", err)
	}

	now := k.Runtime.Clock().Now()

	return k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		// Verify the node truly exists (has content) under the lock.
		exists, exErr := k.nodeExistsWithContent(lockCtx, id)
		if exErr != nil {
			return fmt.Errorf("unable to check node existence: %w", exErr)
		}
		if !exists {
			return fmt.Errorf("node %s not found: %w", id.Path(), ErrNotExist)
		}

		meta, stats, err := k.getMetaAndStats(lockCtx, id)
		if errors.Is(err, ErrNotExist) {
			meta = NewMeta(lockCtx, now)
			stats = NewStats(now)
		} else if err != nil {
			return fmt.Errorf("failed to read node metadata: %w", err)
		}
		if stats == nil {
			stats = &NodeStats{}
		}

		stats.SetAccessed(now)
		stats.IncrementAccessCount()
		stats.EnsureTimes(now)
		if err := k.Repo.WriteMeta(lockCtx, id, []byte(meta.ToYAML())); err != nil {
			return err
		}
		return k.Repo.WriteStats(lockCtx, id, stats)
	})
}

// Node retrieves complete node data including content, metadata, items, and images for a given node ID.
// Returns an error if any component fails to load.
func (k *Keg) Node(id NodeId) *Node {
	alias := id.Alias
	if k.Target != nil {
		alias = k.Target.Keg
	}
	return &Node{
		ID: NodeId{
			ID:    id.ID,
			Alias: alias,
			Code:  id.Code,
		},
		Repo:    k.Repo,
		Runtime: k.Runtime,
	}
}

// IndexNode updates a node's metadata by re-parsing its content and extracting
// properties like title, lead, and content hash. The dex is also updated to reflect
// any changes. If content hasn't changed, this is a no-op.
func (k *Keg) IndexNode(ctx context.Context, id NodeId) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to update node: %w", err)
	}

	var nodeData *NodeData
	err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		updated, changed, err := k.indexNodeLocked(lockCtx, id)
		if err != nil {
			return err
		}
		if changed {
			nodeData = updated
		}
		return nil
	})
	if err != nil {
		return err
	}
	if nodeData == nil {
		return nil
	}
	return k.writeNodeToDex(ctx, id, nodeData)
}

type IndexOptions struct {
	NoUpdate bool
}

// Index rebuilds all keg indices from scratch.
// Every node is scanned, metadata and stats are refreshed (unless
// NoUpdate is set), and the full dex is regenerated.
func (k *Keg) Index(ctx context.Context, opts IndexOptions) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to re index keg: %w", err)
	}

	k.dexMu.Lock()
	if k.dex == nil {
		k.dex = &Dex{}
		// Apply config-driven options (e.g. tag-filtered indexes) to the new Dex.
		dexOpts, _ := k.dexOptions(ctx)
		for _, opt := range dexOpts {
			_ = opt(k.dex)
		}
	} else {
		// Clear preserves registered custom IndexBuilders while emptying their data.
		k.dex.Clear(ctx)
	}
	dex := k.dex // capture pointer under lock for use after unlock
	k.dexMu.Unlock()

	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	now := k.Runtime.Clock().Now()

	// Process nodes in parallel. Each node's probe, read, refresh, and
	// persist steps are independent. Results are collected and fed to
	// dex.Add sequentially after all workers complete.
	type indexResult struct {
		id   NodeId
		data *NodeData
		errs []error
	}

	workers := runtime.NumCPU()
	if workers > 16 {
		workers = 16
	}
	if workers < 1 {
		workers = 1
	}

	sem := make(chan struct{}, workers)
	results := make([]indexResult, len(ids))

	var wg sync.WaitGroup
	for i, id := range ids {
		// Check for cancellation before spawning each goroutine.
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(idx int, nodeID NodeId) {
			defer wg.Done()
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release

			// Skip work if context was cancelled while waiting for the semaphore.
			if ctx.Err() != nil {
				return
			}
			res := indexResult{id: nodeID}
			res.data, res.errs = k.indexNode(ctx, nodeID, opts, now)
			results[idx] = res
		}(i, id)
	}
	wg.Wait()

	if ctx.Err() != nil {
		return fmt.Errorf("index rebuild cancelled: %w", ctx.Err())
	}

	// Sequentially add results to the dex and collect errors.
	var errs []error
	for _, res := range results {
		errs = append(errs, res.errs...)
		if res.data != nil {
			if err := dex.Add(ctx, res.data); err != nil {
				errs = append(errs, fmt.Errorf("failed to add node %s: %w", res.id, err))
			}
		}
	}

	if err := dex.Write(ctx, k.Repo); err != nil {
		errs = append(errs, fmt.Errorf("failed to save dex: %w", err))
	} else {
		k.dexMu.Lock()
		k.recordDexWrite()
		k.dexMu.Unlock()
	}
	if err := k.touchConfigUpdated(ctx, now); err != nil {
		errs = append(errs, fmt.Errorf("failed to update index timestamp: %w", err))
	}

	return errors.Join(errs...)
}

// indexNode processes a single node for index rebuild: probes for missing
// files, reads content/meta/stats, refreshes if needed, and persists
// changes. It is safe to call concurrently for different nodes.
func (k *Keg) indexNode(ctx context.Context, id NodeId, opts IndexOptions, now time.Time) (*NodeData, []error) {
	// Skip bare directories created by Next() but not yet populated by Create().
	exists, existErr := k.nodeExistsWithContent(ctx, id)
	if existErr != nil {
		return nil, []error{existErr}
	}
	if !exists {
		return nil, nil
	}

	var errs []error

	metaMissing, statsMissing, probeErr := k.nodeFilesMissing(ctx, id)
	if probeErr != nil {
		return nil, []error{probeErr}
	}

	data, nodeErrs := k.getNodeBestEffort(ctx, id)
	if len(nodeErrs) > 0 {
		errs = append(errs, nodeErrs...)
	}

	if data.Meta == nil {
		data.Meta = NewMeta(ctx, time.Time{})
	}
	if data.Stats == nil {
		data.Stats = &NodeStats{}
	}

	needsRefresh := metaMissing ||
		statsMissing ||
		(!opts.NoUpdate && (data.ContentChanged() || data.Stats.Title() == "" ||
			data.Stats.Hash() == "" ||
			data.Stats.Created().IsZero() ||
			data.Stats.Updated().IsZero()))

	if needsRefresh {
		if err := data.UpdateMeta(ctx, &now); err != nil {
			errs = append(errs, err)
			// Still return data so the node is not silently dropped
			// from the index. The best-effort data is better than
			// losing the node entirely.
			return data, errs
		}
	}

	data.Stats.EnsureTimes(now)

	needsPersist := metaMissing || statsMissing || needsRefresh
	if needsPersist {
		err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
			if err := k.Repo.WriteMeta(lockCtx, id, []byte(data.Meta.ToYAML())); err != nil {
				return fmt.Errorf("failed to write node meta %s: %w", id.Path(), err)
			}
			if err := k.Repo.WriteStats(lockCtx, id, data.Stats); err != nil {
				return fmt.Errorf("failed to write node stats %s: %w", id.Path(), err)
			}
			return nil
		})
		if err != nil {
			errs = append(errs, err)
			// Still return data so the node is not silently dropped
			// from the index on persist failure.
			return data, errs
		}
	}

	return data, errs
}

// Move renames a node from src to dst and rewrites in-content links that
// target src (../N) across the keg.
func (k *Keg) Move(ctx context.Context, src NodeId, dst NodeId) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to move node: %w", err)
	}

	src = NodeId{ID: src.ID, Code: src.Code}
	dst = NodeId{ID: dst.ID, Code: dst.Code}
	if !src.Valid() || !dst.Valid() {
		return fmt.Errorf("invalid node id: %w", ErrInvalid)
	}
	if src.ID == 0 || dst.ID == 0 {
		return fmt.Errorf("node 0 cannot be moved: %w", ErrInvalid)
	}
	if src.Equals(dst) {
		return nil
	}

	// Use content-aware existence checks so shadow reservations created by
	// FsRepo.Next() / FsRepo.WithNodeLock() do not masquerade as real nodes
	// (F11 of report 842). These are pre-lock gates; the under-lock
	// authoritative check runs inside Repo.MoveNode.
	srcExists, err := k.nodeExistsWithContent(ctx, src)
	if err != nil {
		return fmt.Errorf("failed to check source node: %w", err)
	}
	if !srcExists {
		return fmt.Errorf("source node %s not found: %w", src.Path(), ErrNotExist)
	}

	dstExists, err := k.nodeExistsWithContent(ctx, dst)
	if err != nil {
		return fmt.Errorf("failed to check destination node: %w", err)
	}
	if dstExists {
		return fmt.Errorf("destination node %s already exists: %w", dst.Path(), ErrDestinationExists)
	}

	if err := k.Repo.MoveNode(ctx, src, dst); err != nil {
		return fmt.Errorf("failed to move node %s to %s: %w", src.Path(), dst.Path(), err)
	}

	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return fmt.Errorf("failed to list nodes for link rewrite: %w", err)
	}

	// Rewrite links in all nodes, collecting changed NodeData without
	// writing the dex after each individual change. A single batched dex
	// write follows the loop.
	var errs []error
	var changedNodes []*NodeData
	linkRE := compileNodeLinkPattern(src)
	for _, id := range ids {
		raw, readErr := k.Repo.ReadContent(ctx, id)
		if readErr != nil {
			if errors.Is(readErr, ErrNotExist) {
				continue
			}
			errs = append(errs, fmt.Errorf("failed to read node content %s: %w", id.Path(), readErr))
			continue
		}

		updated, changed := rewriteNodeLinks(raw, linkRE, dst)
		if !changed {
			continue
		}
		nd, err := k.setContentNoDex(ctx, id, updated)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to rewrite links for node %s: %w", id.Path(), err))
			continue
		}
		if nd != nil {
			changedNodes = append(changedNodes, nd)
		}
	}

	// Single batched dex update: load fresh dex, apply all changes, write once.
	dex, err := k.ensureDexFresh(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to retrieve dex after move: %w", err))
	} else {
		if err := dex.Remove(ctx, src); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove stale dex entry for %s: %w", src.Path(), err))
		}
		movedData, err := k.getNode(ctx, dst)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to load moved node %s: %w", dst.Path(), err))
		} else if err := dex.Add(ctx, movedData); err != nil {
			errs = append(errs, fmt.Errorf("failed to add moved node %s to dex: %w", dst.Path(), err))
		}
		for _, nd := range changedNodes {
			// Remove stale entry first so Add replaces rather than merges
			// with outdated link/tag data.
			if err := dex.Remove(ctx, nd.ID); err != nil {
				errs = append(errs, fmt.Errorf("failed to remove stale dex entry for %s: %w", nd.ID.Path(), err))
			}
			if err := dex.Add(ctx, nd); err != nil {
				errs = append(errs, fmt.Errorf("failed to add changed node %s to dex: %w", nd.ID.Path(), err))
			}
		}
		if err := dex.Write(ctx, k.Repo); err != nil {
			errs = append(errs, fmt.Errorf("failed to write dex after move: %w", err))
		} else {
			k.dexMu.Lock()
			k.recordDexWrite()
			k.dexMu.Unlock()
		}
	}

	now := k.Runtime.Clock().Now()
	if err := k.touchConfigUpdated(ctx, now); err != nil {
		errs = append(errs, fmt.Errorf("failed to update config after move: %w", err))
	}

	return errors.Join(errs...)
}

// Remove deletes a node from the repository and updates dex/config artifacts.
func (k *Keg) Remove(ctx context.Context, id NodeId) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to remove node: %w", err)
	}

	id = NodeId{ID: id.ID, Code: id.Code}
	if !id.Valid() {
		return fmt.Errorf("invalid node id: %w", ErrInvalid)
	}
	if id.ID == 0 {
		return fmt.Errorf("node 0 cannot be removed: %w", ErrInvalid)
	}

	// Check existence before acquiring the lock. WithNodeLock will also
	// return ErrNotExist for missing nodes, but this check provides a
	// clearer error message. Use the content-aware helper so shadow
	// reservations (bare directories from FsRepo.Next() / WithNodeLock)
	// are not mistaken for real nodes — F11 of report 842.
	exists, err := k.nodeExistsWithContent(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to check node existence: %w", err)
	}
	if !exists {
		return fmt.Errorf("node %s not found: %w", id.Path(), ErrNotExist)
	}

	// Acquire node lock to prevent concurrent writes from resurrecting
	// the node directory after deletion. Re-check existence under lock.
	if err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		exists, err := k.nodeExistsWithContent(lockCtx, id)
		if err != nil {
			return fmt.Errorf("failed to check node existence: %w", err)
		}
		if !exists {
			return fmt.Errorf("node %s not found: %w", id.Path(), ErrNotExist)
		}
		return k.Repo.DeleteNode(lockCtx, id)
	}); err != nil {
		return err
	}

	// Rewrite all links that pointed to the removed node so they point to
	// the zero node (../0) instead of dangling. These are cosmetic content
	// rewrites that don't need per-node dex updates -- the Remove dex update
	// handles the index cleanup via dex.Remove(id).
	var errs []error
	zeroID := NodeId{ID: 0}
	linkRE := compileNodeLinkPattern(id)
	nodeIDs, listErr := k.Repo.ListNodes(ctx)
	if listErr != nil {
		return fmt.Errorf("failed to list nodes for link rewrite after remove: %w", listErr)
	}
	for _, otherID := range nodeIDs {
		raw, readErr := k.Repo.ReadContent(ctx, otherID)
		if readErr != nil {
			if !errors.Is(readErr, ErrNotExist) {
				errs = append(errs, fmt.Errorf("failed to read node %s for link rewrite: %w", otherID.Path(), readErr))
			}
			continue
		}
		updated, changed := rewriteNodeLinks(raw, linkRE, zeroID)
		if changed {
			if err := k.withNodeLock(ctx, otherID, func(lockCtx context.Context) error {
				exists, exErr := k.nodeExistsWithContent(lockCtx, otherID)
				if exErr != nil || !exists {
					return nil // node was concurrently removed, skip rewrite
				}
				return k.Repo.WriteContent(lockCtx, otherID, updated)
			}); err != nil {
				errs = append(errs, fmt.Errorf("failed to rewrite links in node %s: %w", otherID.Path(), err))
			}
		}
	}

	// Single batched dex update: remove deleted node, write once.
	dex, err := k.ensureDexFresh(ctx)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to retrieve dex after remove: %w", err))
	} else {
		if err := dex.Remove(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove %s from dex: %w", id.Path(), err))
		}
		if err := dex.Write(ctx, k.Repo); err != nil {
			errs = append(errs, fmt.Errorf("failed to write dex after remove: %w", err))
		} else {
			k.dexMu.Lock()
			k.recordDexWrite()
			k.dexMu.Unlock()
		}
	}

	now := k.Runtime.Clock().Now()
	if err := k.touchConfigUpdated(ctx, now); err != nil {
		errs = append(errs, fmt.Errorf("failed to update config after remove: %w", err))
	}

	return errors.Join(errs...)
}

// Commit finalizes a temporary node by allocating a permanent ID and moving it
// from its temporary location (with Code suffix) to the canonical numeric ID.
// For nodes without a Code (already permanent), Commit is a no-op.
func (k *Keg) Commit(ctx context.Context, id NodeId) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to commit node: %w", err)
	}

	// only commit when Code is present (temporary id)
	if id.Code == "" {
		return nil
	}
	dst, err := k.Repo.Next(ctx)
	if err != nil {
		return err
	}
	if err := k.Repo.MoveNode(ctx, id, dst); err != nil {
		return err
	}
	return nil
}

// Dex returns the keg's index, loading it from the repository on first access.
// The dex is lazily loaded and cached in memory for efficient access.
// Config-driven query-filtered indexes are applied automatically via WithConfig.
func (k *Keg) Dex(ctx context.Context) (*Dex, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve dex: %w", err)
	}

	k.dexMu.Lock()
	defer k.dexMu.Unlock()

	if k.dex != nil {
		return k.dex, nil
	}
	opts, _ := k.dexOptions(ctx)
	dex, err := NewDexFromRepo(ctx, k.Repo, opts...)
	k.dex = dex
	k.dexLoadMtime = k.indexFileMtime()
	return dex, err
}

// DexFresh returns the keg's index, reloading from disk if the on-disk index
// files have changed since the last load. This is the correct method for
// long-lived processes (serve handlers, MCP servers) where another process may
// update the dex between calls. For FsRepo backends, it compares the mtime of
// dex/nodes.tsv; for MemoryRepo (single-process) it behaves identically to Dex.
func (k *Keg) DexFresh(ctx context.Context) (*Dex, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve dex: %w", err)
	}
	return k.ensureDexFresh(ctx)
}

// dexOptions reads the keg config and returns DexOptions to apply when
// constructing or initialising a Dex. If the config is absent or cannot be
// read, an empty (nil) slice is returned so callers can proceed without error.
//
// Extra options injected via SetExtraDexOpts are prepended before WithConfig
// so that resolvers (e.g. WithQueryResolver) are installed on the Dex before
// WithConfig creates QueryFilteredIndex instances that reference them.
func (k *Keg) dexOptions(ctx context.Context) ([]DexOption, error) {
	cfg, err := k.Repo.ReadConfig(ctx)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	// Prepend extraDexOpts so resolvers are set before WithConfig runs.
	opts := make([]DexOption, 0, len(k.extraDexOpts)+1)
	opts = append(opts, k.extraDexOpts...)
	opts = append(opts, WithConfig(cfg))
	return opts, nil
}

// -- private utility functions

func (k *Keg) withNodeLock(ctx context.Context, id NodeId, fn func(context.Context) error) error {
	if contextHasNodeLock(ctx, id) {
		return fn(ctx)
	}
	return k.Repo.WithNodeLock(ctx, id, fn)
}

// nodeExistsWithContent checks whether a node truly exists by verifying it has
// content (or at minimum an entry in the repo). For FsRepo, WithNodeLock
// creates a bare directory as a side effect of lock acquisition, so HasNode
// alone is insufficient — a bare directory without README.md is not a real
// node. This helper reads the content file to confirm the node was properly
// created via Create/Init and not merely a lock artifact.
func (k *Keg) nodeExistsWithContent(ctx context.Context, id NodeId) (bool, error) {
	_, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (k *Keg) indexNodeLocked(ctx context.Context, id NodeId) (*NodeData, bool, error) {
	n := k.Node(id)
	changed, err := n.Changed(ctx)
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return nil, false, nil
	}
	if err := n.Update(ctx); err != nil {
		return nil, false, fmt.Errorf("failed to update node %s: %w", id, err)
	}
	return n.data, true, nil
}

// indexFileMtime returns the ModTime of dex/nodes.tsv for FsRepo backends.
// For non-filesystem repos (e.g. MemoryRepo) it returns time.Time{} (zero).
func (k *Keg) indexFileMtime() time.Time {
	fsRepo, ok := k.Repo.(*FsRepo)
	if !ok {
		return time.Time{}
	}
	idxPath := filepath.Join(fsRepo.Root, "dex", "nodes.tsv")
	info, err := fsRepo.runtime.Stat(idxPath, false)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// dexStale reports whether the cached dex is out of date with respect to the
// on-disk index files. For MemoryRepo (single-process) this always returns
// false because there are no external writers. For FsRepo it compares the
// current mtime of dex/nodes.tsv against the mtime recorded when the dex was
// last loaded.
//
// Caller must hold k.dexMu.
func (k *Keg) dexStale() bool {
	if _, ok := k.Repo.(*FsRepo); !ok {
		return false
	}
	current := k.indexFileMtime()
	if current.IsZero() {
		// File doesn't exist — treat as stale so we rebuild.
		return true
	}
	return !current.Equal(k.dexLoadMtime)
}

// ensureDexFresh returns the cached dex if it is still current, otherwise
// reloads it from disk. This replaces the pattern of InvalidateDex() +
// Dex(ctx) which unconditionally discarded the cache.
//
// ensureDexFresh acquires k.dexMu internally; callers must NOT hold it.
func (k *Keg) ensureDexFresh(ctx context.Context) (*Dex, error) {
	k.dexMu.Lock()
	defer k.dexMu.Unlock()

	if k.dex != nil && !k.dexStale() {
		return k.dex, nil
	}

	opts, _ := k.dexOptions(ctx)
	dex, err := NewDexFromRepo(ctx, k.Repo, opts...)
	k.dex = dex
	k.dexLoadMtime = k.indexFileMtime()
	return dex, err
}

// recordDexWrite updates the mtime cache and generation counter after a
// successful Dex.Write. Caller must hold k.dexMu.
func (k *Keg) recordDexWrite() {
	k.dexLoadMtime = k.indexFileMtime()
	k.dexWriteGen++
}

func (k *Keg) writeNodeToDex(ctx context.Context, id NodeId, data *NodeData) error {
	dex, err := k.ensureDexFresh(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve dex: %w", err)
	}
	if err := dex.Add(ctx, data); err != nil {
		return fmt.Errorf("failed to add node %s to dex: %w", id, err)
	}
	if err := dex.Write(ctx, k.Repo); err != nil {
		return fmt.Errorf("failed to write dex: %w", err)
	}

	k.dexMu.Lock()
	k.recordDexWrite()
	k.dexMu.Unlock()

	return k.touchConfigUpdated(ctx, k.Runtime.Clock().Now())
}

// compileNodeLinkPattern builds a regexp that matches canonical relative node
// links "../N" for the given source node ID. Pre-compile once and pass to
// rewriteNodeLinks to avoid recompilation per call.
func compileNodeLinkPattern(src NodeId) *regexp.Regexp {
	oldID := src.Path()
	delimiters := `[[:space:]\)\]\}\>\.,;:!?'\"#]`
	pattern := `\.\./\s*` + regexp.QuoteMeta(oldID) + `(` + delimiters + `|$)`
	return regexp.MustCompile(pattern)
}

func rewriteNodeLinks(raw []byte, re *regexp.Regexp, dst NodeId) ([]byte, bool) {
	newID := dst.Path()
	if newID == "" || len(raw) == 0 {
		return raw, false
	}

	original := string(raw)
	rewritten := re.ReplaceAllString(original, "../"+newID+`$1`)
	if rewritten == original {
		return raw, false
	}
	return []byte(rewritten), true
}

func (k *Keg) touchConfigUpdated(ctx context.Context, at time.Time) error {
	if at.IsZero() {
		at = k.Runtime.Clock().Now()
	}
	updated := at.Format(time.RFC3339)

	if fsRepo, ok := k.Repo.(*FsRepo); ok {
		return fsRepoTouchConfigUpdated(fsRepo, updated)
	}

	return k.UpdateConfig(ctx, func(cfg *Config) {
		cfg.Updated = updated
	})
}

func fsRepoTouchConfigUpdated(repo *FsRepo, updated string) error {
	configPath, raw, err := fsRepoReadRawConfig(repo)
	if err != nil {
		return err
	}

	patched, err := patchConfigUpdatedField(raw, updated)
	if err != nil {
		return fmt.Errorf("failed to patch config timestamp: %w", err)
	}

	if bytes.Equal(raw, patched) {
		return nil
	}
	if err := repo.runtime.AtomicWriteFile(configPath, patched, 0o644); err != nil {
		return NewBackendError(repo.Name(), "WriteConfig", 0, err, false)
	}
	return nil
}

func fsRepoReadRawConfig(repo *FsRepo) (string, []byte, error) {
	candidates := []string{"keg", "keg.yaml", "keg.yml"}
	for _, candidate := range candidates {
		path := filepath.Join(repo.Root, candidate)
		if _, err := repo.runtime.Stat(path, false); err == nil {
			b, readErr := repo.runtime.ReadFile(path)
			if readErr != nil {
				return "", nil, NewBackendError(repo.Name(), "ReadConfig", 0, readErr, false)
			}
			return path, b, nil
		}
	}
	return "", nil, ErrNotExist
}

func patchConfigUpdatedField(raw []byte, updated string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("config root must be a mapping")
	}

	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		if key.Kind == yaml.ScalarNode && key.Value == "updated" {
			val := root.Content[i+1]
			val.Kind = yaml.ScalarNode
			val.Tag = "!!str"
			val.Style = 0
			val.Value = updated
			return yaml.Marshal(&doc)
		}
	}

	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "updated"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: updated},
	)
	return yaml.Marshal(&doc)
}

func (k *Keg) nodeFilesMissing(ctx context.Context, id NodeId) (bool, bool, error) {
	exists, err := k.Repo.HasNode(ctx, id)
	if err != nil {
		return false, false, fmt.Errorf("failed to probe node existence for %s: %w", id.Path(), err)
	}
	if !exists {
		return true, true, nil
	}

	rawMeta, err := k.Repo.ReadMeta(ctx, id)
	metaMissing := err != nil || len(bytes.TrimSpace(rawMeta)) == 0

	_, statsErr := k.Repo.ReadStats(ctx, id)
	statsMissing := statsErr != nil

	return metaMissing, statsMissing, nil
}

// getContent retrieves and parses raw markdown content for a node.
func (k *Keg) getContent(ctx context.Context, id NodeId) (*NodeContent, error) {
	raw, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		return nil, err
	}
	return ParseContent(k.Runtime, raw, FormatMarkdown)
}

// getMeta retrieves and parses YAML metadata for a node.
func (k *Keg) getMeta(ctx context.Context, id NodeId) (*NodeMeta, error) {
	raw, err := k.Repo.ReadMeta(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return NewMeta(ctx, time.Time{}), nil
		}
		return nil, err
	}
	return ParseMeta(ctx, raw)
}

func (k *Keg) getStats(ctx context.Context, id NodeId) (*NodeStats, error) {
	stats, err := k.Repo.ReadStats(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return &NodeStats{}, nil
		}
		return nil, err
	}
	return stats, nil
}

func (k *Keg) getMetaAndStats(ctx context.Context, id NodeId) (*NodeMeta, *NodeStats, error) {
	meta, err := k.getMeta(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	stats, err := k.getStats(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return meta, stats, nil
}

// getNodeBestEffort builds a NodeData by loading each component independently.
// Unlike getNode, it does not abort on the first error. Whichever components
// load successfully are populated; failed components are left nil. The returned
// errors slice contains one wrapped error per failed component.
func (k *Keg) getNodeBestEffort(ctx context.Context, n NodeId) (*NodeData, []error) {
	var errs []error
	data := &NodeData{ID: n}

	content, err := k.getContent(ctx, n)
	if err != nil {
		errs = append(errs, fmt.Errorf("node %s content: %w", n.Path(), err))
	} else {
		data.Content = content
	}

	meta, stats, err := k.getMetaAndStats(ctx, n)
	if err != nil {
		errs = append(errs, fmt.Errorf("node %s meta/stats: %w", n.Path(), err))
	} else {
		data.Meta = meta
		data.Stats = stats
	}

	items, err := repoListFiles(ctx, k.Repo, n)
	if err != nil {
		errs = append(errs, fmt.Errorf("node %s files: %w", n.Path(), err))
	} else {
		data.Items = items
	}

	images, err := repoListImages(ctx, k.Repo, n)
	if err != nil {
		errs = append(errs, fmt.Errorf("node %s images: %w", n.Path(), err))
	} else {
		data.Images = images
	}

	return data, errs
}

// getNode builds a complete NodeData from a node's content, metadata, and attachments.
func (k *Keg) getNode(ctx context.Context, n NodeId) (*NodeData, error) {
	content, err := k.getContent(ctx, n)
	if err != nil {
		return nil, err
	}
	meta, stats, err := k.getMetaAndStats(ctx, n)
	if err != nil {
		return nil, err
	}

	items, err := repoListFiles(ctx, k.Repo, n)
	if err != nil {
		return nil, err
	}

	images, err := repoListImages(ctx, k.Repo, n)
	if err != nil {
		return nil, err
	}

	return &NodeData{
		ID:      n,
		Content: content,
		Meta:    meta,
		Stats:   stats,
		Items:   items,
		Images:  images,
	}, nil
}

// InvalidateDex clears the cached dex so the next Dex() call reloads from
// the repository. This is useful when external processes may have modified
// the index files.
func (k *Keg) InvalidateDex() {
	k.dexMu.Lock()
	k.dex = nil
	k.dexMu.Unlock()
}

// addNodeToDex adds a node to the dex, writes dex changes to the repository,
// and updates the keg's Updated timestamp to the provided time (or now if not specified).
func (k *Keg) addNodeToDex(ctx context.Context, data *NodeData, now *time.Time) error {
	dex, err := k.ensureDexFresh(ctx)
	if err != nil {
		return err
	}

	if err := dex.Add(ctx, data); err != nil {
		return err
	}

	if now != nil {
		if err := dex.Write(ctx, k.Repo); err != nil {
			return err
		}

		k.dexMu.Lock()
		k.recordDexWrite()
		k.dexMu.Unlock()

		if err := k.touchConfigUpdated(ctx, *now); err != nil {
			return err
		}
	}
	return nil
}

// checkKegExists verifies that a keg is properly initialized in the repository.
// Returns an error if the keg is not found or if the repository is not configured.
func (k *Keg) checkKegExists(ctx context.Context) error {
	if k == nil || k.Repo == nil {
		return fmt.Errorf("no repository configured")
	}

	if k.kegExistsVerified.Load() {
		return nil
	}

	exists, err := RepoContainsKeg(ctx, k.Repo)
	if err != nil {
		return fmt.Errorf("failed to check keg existence: %w", err)
	}
	if !exists {
		return fmt.Errorf("keg not initialized: %w", ErrNotExist)
	}
	k.kegExistsVerified.Store(true)
	return nil
}
