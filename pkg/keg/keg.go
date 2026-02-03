package keg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jlrickert/cli-toolkit/clock"
	"github.com/jlrickert/cli-toolkit/toolkit"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

// Keg is a concrete high-level service providing KEG node operations backed by a
// KegRepository. It abstracts storage implementation details, allowing operations
// over nodes to work uniformly across memory, filesystem, and remote backends.
// Keg delegates low-level storage operations to its underlying repository and
// maintains an in-memory dex for indexing.
type Keg struct {
	// Target is the keg URL/location (nil for memory-backed kegs)
	Target *kegurl.Target
	// Repo is the storage backend implementation
	Repo KegRepository

	// dex is an optional in-memory index of nodes, lazily loaded from repo
	dex *Dex
}

// Option is a functional option for configuring Keg behavior
type Option func(*Keg)

// NewKegFromTarget constructs a Keg from a kegurl.Target. It automatically
// selects the appropriate repository implementation based on the target's scheme:
// - memory:// targets use an in-memory repository
// - file:// targets use a filesystem repository
// Returns an error if the target scheme is not supported.
func NewKegFromTarget(ctx context.Context, target kegurl.Target) (*Keg, error) {
	switch target.Scheme() {
	case kegurl.SchemeMemory:
		repo := NewMemoryRepo()
		keg := Keg{Repo: repo}
		return &keg, nil
	case kegurl.SchemeFile:
		repo := FsRepo{
			Root:            toolkit.AbsPath(ctx, target.Path()),
			ContentFilename: MarkdownContentFilename,
			MetaFilename:    YAMLMetaFilename,
		}
		keg := Keg{Target: &target, Repo: &repo}
		return &keg, nil
	}
	return nil, fmt.Errorf("unsupported target scheme: %s", target.Scheme())
}

// NewKeg returns a Keg service backed by the provided repository.
// Functional options can be provided to customize Keg behavior.
func NewKeg(repo KegRepository, opts ...Option) *Keg {
	keg := &Keg{
		Repo: repo,
	}
	for _, o := range opts {
		o(keg)
	}
	return keg
}

// RepoContainsKeg checks if a keg has been properly initialized within a repository.
// It verifies both that a keg config exists and that a zero node (node ID 0) is present.
// Returns true only if both conditions are met, indicating a fully initialized keg.
func RepoContainsKeg(ctx context.Context, repo KegRepository) (bool, error) {
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
	cfg := NewKegConfig()
	if err := k.Repo.WriteConfig(ctx, cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	// Create the zero node as a special case during Init. We do this here so
	// Create can continue to require an initiated keg.
	clk := clock.ClockFromContext(ctx)
	now := clk.Now()

	rawContent := RawZeroNodeContent
	zeroContent, _ := ParseContent(ctx, []byte(rawContent), MarkdownContentFilename)

	m := NewMeta(ctx, now)
	// no attrs to apply for the zero node; leave as empty map
	_ = m.SetAttrs(ctx, nil)
	m.Update(ctx, zeroContent, &now)

	id := NodeId{ID: 0}

	if err := k.Repo.WriteContent(ctx, id, []byte(rawContent)); err != nil {
		return fmt.Errorf("Init: write content to backend %s: %w", k.Repo.Name(), err)
	}
	if err := k.Repo.WriteMeta(ctx, id, []byte(m.ToYAML())); err != nil {
		return fmt.Errorf("Init: write meta to backend %s: %w", k.Repo.Name(), err)
	}

	nodeData := &NodeData{ID: id, Content: zeroContent, Meta: m}
	if err := k.addNodeToDex(ctx, nodeData, &now); err != nil {
		return fmt.Errorf("failed to index zero node: %w", err)
	}

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

	clk := clock.ClockFromContext(ctx)
	now := clk.Now()

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

	content, err := ParseContent(ctx, rawContent, MarkdownContentFilename)
	if err != nil {
		return NodeId{}, fmt.Errorf("invalid content: %w", err)
	}
	m := NewMeta(ctx, now)
	if len(opts.Attrs) > 0 {
		m.SetAttrs(ctx, opts.Attrs)
	}

	if len(opts.Tags) > 0 {
		m.SetTags(ctx, opts.Tags)
	}

	m.Update(ctx, content, &now)

	// Persist empty content so repo implementations that require a content file
	// are satisfied.
	if err := k.Repo.WriteContent(ctx, id, []byte(content.Body)); err != nil {
		return id, fmt.Errorf("create: write content to backend %s: %w", k.Repo.Name(), err)
	}
	if err := k.Repo.WriteMeta(ctx, id, []byte(m.ToYAML())); err != nil {
		return id, fmt.Errorf("create: write meta to backend %s: %w", k.Repo.Name(), err)
	}

	nodeData := &NodeData{ID: id, Content: content, Meta: m}
	return id, k.addNodeToDex(ctx, nodeData, &now)
}

// Config returns the keg's configuration.
func (k *Keg) Config(ctx context.Context) (*KegConfig, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve config: %w", err)
	}

	return k.Repo.ReadConfig(ctx)
}

// UpdateConfig reads the keg config, applies the provided mutation function,
// and writes the result back to the repository. This is the preferred way to
// modify keg configuration to ensure updates are atomically persisted.
func (k *Keg) UpdateConfig(ctx context.Context, f func(*KegConfig)) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("unable to update config: %w", err)
	}

	// Read config directly from the repository to allow Init to create it when
	// the keg is not yet fully initiated.
	cfg, err := k.Repo.ReadConfig(ctx)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			cfg = NewKegConfig()
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

// SetContent writes content for a node and updates its metadata by re-indexing.
// This ensures the node's title, lead, and other metadata are kept in sync with content changes.
func (k *Keg) SetContent(ctx context.Context, id NodeId, data []byte) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to set node content: %w", err)
	}

	// write content
	if err := k.Repo.WriteContent(ctx, id, data); err != nil {
		return fmt.Errorf("unable to write content: %w", err)
	}
	return k.IndexNode(ctx, id)
}

// GetMeta retrieves the parsed metadata for a node.
func (k *Keg) GetMeta(ctx context.Context, id NodeId) (*Meta, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to get node meta: %w", err)
	}
	return k.getMeta(ctx, id)
}

// SetMeta writes metadata for a node and updates the dex.
func (k *Keg) SetMeta(ctx context.Context, id NodeId, meta *Meta) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to update node meta: %w", err)
	}

	// write back
	if err := k.Repo.WriteMeta(ctx, id, []byte(meta.ToYAML())); err != nil {
		return fmt.Errorf("UpdateMeta: write meta to backend %s: %w", k.Repo.Name(), err)
	}

	clk := clock.ClockFromContext(ctx)
	now := clk.Now()
	nodeData := &NodeData{ID: id, Meta: meta}
	return k.addNodeToDex(ctx, nodeData, &now)
}

// UpdateMeta reads the node's metadata, applies the provided mutation function,
// and writes the result back to the repository with dex updates.
func (k *Keg) UpdateMeta(ctx context.Context, id NodeId, f func(*Meta)) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to update node meta: %w", err)
	}

	clk := clock.ClockFromContext(ctx)
	now := clk.Now()

	m, err := k.getMeta(ctx, id)
	if errors.Is(err, ErrNotExist) {
		m = NewMeta(ctx, now)
	}

	// apply mutation
	f(m)

	// write back
	if err := k.Repo.WriteMeta(ctx, id, []byte(m.ToYAML())); err != nil {
		return fmt.Errorf("UpdateMeta: write meta to backend %s: %w", k.Repo.Name(), err)
	}
	return nil
}

// Touch updates the access time of a node to the current time.
func (k *Keg) Touch(ctx context.Context, id NodeId) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to touch node: %w", err)
	}

	return k.UpdateMeta(ctx, id, func(m *Meta) {
		clk := clock.ClockFromContext(ctx)
		now := clk.Now()
		m.accessed = now
	})
}

// IndexNode updates a node's metadata by re-parsing its content and extracting
// properties like title, lead, and content hash. The dex is also updated to reflect
// any changes. If content hasn't changed, this is a no-op.
func (k *Keg) IndexNode(ctx context.Context, id NodeId) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to update node: %w", err)
	}

	data, err := k.getNode(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to build node data: %w", err)
	}
	if !data.ContentChanged() {
		return nil
	}

	clk := clock.ClockFromContext(ctx)
	now := clk.Now()
	data.Meta.Update(ctx, data.Content, &now)

	err = k.Repo.WriteMeta(ctx, id, []byte(data.Meta.ToYAML()))
	if err != nil {
		return err
	}
	return k.addNodeToDex(ctx, data, &now)
}

// Index performs a full keg re-indexing by clearing the dex and rebuilding it
// from scratch by reading all nodes in the repository. This is useful for
// recovering from index corruption or after bulk node modifications.
func (k *Keg) Index(ctx context.Context, _ NodeId) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to re index keg: %w", err)
	}

	// Build a fresh in-memory Dex
	if k.dex == nil {
		k.dex = &Dex{}
	} else {
		// ensure empty state before rebuilding
		k.dex.Clear(ctx)
	}

	// List node ids and populate dex
	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	var wg sync.WaitGroup

	var errs []error
	for _, id := range ids {
		wg.Go(func() {
			data, err := k.getNode(ctx, id)
			if err != nil {
				errs = append(errs, err)
				return
			}
			clk := clock.ClockFromContext(ctx)
			now := clk.Now()
			if data.Meta.accessed.IsZero() {
				data.Meta.accessed = now
			}
			if data.Meta.updated.IsZero() {
				data.Meta.updated = now
			}
			if data.Meta.created.IsZero() {
				data.Meta.created = now
			}
			// Dex.Add expects a *NodeData
			if err := k.dex.Add(ctx, data); err != nil {
				errs = append(errs, fmt.Errorf("failed to add node %s: %w", id, err))
			}
		})
	}
	wg.Wait()

	if err := k.dex.Write(ctx, k.Repo); err != nil {
		errs = append(errs, fmt.Errorf("failed to save dex: %w", err))
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
func (k *Keg) Dex(ctx context.Context) (*Dex, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve dex: %w", err)
	}

	if k.dex != nil {
		return k.dex, nil
	}
	dex, err := NewDexFromRepo(ctx, k.Repo)
	k.dex = dex
	return dex, err
}

// -- private utility functions

// getContent retrieves and parses raw markdown content for a node.
func (k *Keg) getContent(ctx context.Context, id NodeId) (*Content, error) {
	raw, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		return nil, err
	}
	return ParseContent(ctx, raw, FormatMarkdown)
}

// getMeta retrieves and parses YAML metadata for a node.
func (k *Keg) getMeta(ctx context.Context, id NodeId) (*Meta, error) {
	raw, err := k.Repo.ReadMeta(ctx, id)
	if err != nil {
		return nil, err
	}
	return ParseMeta(ctx, raw)
}

// getNode builds a complete NodeData from a node's content, metadata, and attachments.
func (k *Keg) getNode(ctx context.Context, n NodeId) (*NodeData, error) {
	content, err := k.getContent(ctx, n)
	if err != nil {
		return nil, err
	}
	meta, err := k.getMeta(ctx, n)
	if err != nil {
		return nil, err
	}

	items, err := k.Repo.ListItems(ctx, n)
	if err != nil {
		return nil, err
	}

	images, err := k.Repo.ListImages(ctx, n)
	if err != nil {
		return nil, err
	}

	return &NodeData{
		ID:      n,
		Content: content,
		Meta:    meta,
		Items:   items,
		Images:  images,
	}, nil
}

// addNodeToDex adds a node to the dex, writes dex changes to the repository,
// and updates the keg's Updated timestamp to the provided time (or now if not specified).
func (k *Keg) addNodeToDex(ctx context.Context, data *NodeData, now *time.Time) error {
	dex, err := k.Dex(ctx)
	if err != nil {
		return err
	}

	dex.Add(ctx, data)

	if now != nil {
		if err := dex.Write(ctx, k.Repo); err != nil {
			return err
		}

		if err := k.UpdateConfig(ctx, func(kc *KegConfig) {
			kc.Updated = now.Format(time.RFC3339)
		}); err != nil {
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

	exists, err := RepoContainsKeg(ctx, k.Repo)
	if err != nil {
		return fmt.Errorf("failed to check keg existence: %w", err)
	}
	if !exists {
		return fmt.Errorf("keg not initialized: %w", ErrNotExist)
	}
	return nil
}
