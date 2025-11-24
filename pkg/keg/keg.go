package keg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/clock"
	"github.com/jlrickert/cli-toolkit/toolkit"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

// Keg is a concrete high-level service backed by a KegRepository.
// It implements common node operations by delegating low-level storage to the
// repo.
type Keg struct {
	Target *kegurl.Target
	Repo   KegRepository

	dex *Dex
}

type KegOption func(*Keg)

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
func NewKeg(repo KegRepository, opts ...KegOption) *Keg {
	keg := &Keg{
		Repo: repo,
	}
	for _, o := range opts {
		o(keg)
	}
	return keg
}

// IsKegInitiated checks if a keg has been initiated within the repo. It
// verifies that the keg config exists and that a zero node is present.
func IsKegInitiated(ctx context.Context, repo KegRepository) (bool, error) {
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
	_, err = repo.ReadContent(ctx, Node{ID: 0})
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

// Init creates a new keg.
func (keg *Keg) Init(ctx context.Context) error {
	if keg == nil || keg.Repo == nil {
		return fmt.Errorf("no repository configured")
	}

	// refuse to init when a keg already exists
	exists, err := IsKegInitiated(ctx, keg.Repo)
	if err != nil {
		return fmt.Errorf("failed to check keg existence: %w", err)
	}
	if exists {
		return fmt.Errorf("keg already exists: %w", ErrExist)
	}

	// Ensure we have a config file. UpdateConfig must be allowed to write the
	// repo-level config even when the keg is not fully initiated.
	cfg := NewKegConfig()
	if err := keg.Repo.WriteConfig(ctx, cfg); err != nil {
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

	id := Node{ID: 0}

	if err := keg.Repo.WriteContent(ctx, id, []byte(rawContent)); err != nil {
		return fmt.Errorf("Init: write content to backend %s: %w", keg.Repo.Name(), err)
	}
	if err := keg.Repo.WriteMeta(ctx, id, []byte(m.ToYAML())); err != nil {
		return fmt.Errorf("Init: write meta to backend %s: %w", keg.Repo.Name(), err)
	}

	nodeData := &NodeData{ID: id, Content: zeroContent, Meta: m}
	if err := keg.addNodeToDex(ctx, nodeData, &now); err != nil {
		return fmt.Errorf("failed to index zero node: %w", err)
	}

	return nil
}

func (keg *Keg) Next(ctx context.Context) (Node, error) {
	return keg.Repo.Next(ctx)
}

type KegCreateOptions struct {
	Title string
	Lead  string
	Tags  []string
	Body  []byte
	Attrs map[string]any
}

// Create creates a new node: allocates an ID, writes content, creates and
// writes meta.
func (keg *Keg) Create(ctx context.Context, opts *KegCreateOptions) (Node, error) {
	if err := keg.checkKegExists(ctx); err != nil {
		return Node{}, fmt.Errorf("failed to create node: %w", err)
	}

	if opts == nil {
		opts = &KegCreateOptions{}
	}

	// Reserve next ID
	id, err := keg.Repo.Next(ctx)
	if err != nil {
		return Node{}, fmt.Errorf("failed to allocate node id: %w", err)
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
			b.WriteString(fmt.Sprintf("# Node %s\n", id.Path()))
		}

		if opts.Lead != "" {
			b.WriteString(fmt.Sprintf("\n%s\n", opts.Lead))
		}
		rawContent = []byte(b.String())
	}

	content, err := ParseContent(ctx, []byte(rawContent), MarkdownContentFilename)
	if err != nil {
		return Node{}, fmt.Errorf("invalid content: %w", err)
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
	if err := keg.Repo.WriteContent(ctx, id, []byte(content.Body)); err != nil {
		return id, fmt.Errorf("Create: write content to backend %s: %w", keg.Repo.Name(), err)
	}
	if err := keg.Repo.WriteMeta(ctx, id, []byte(m.ToYAML())); err != nil {
		return id, fmt.Errorf("Create: write meta to backend %s: %w", keg.Repo.Name(), err)
	}

	nodeData := &NodeData{ID: id, Content: content, Meta: m}
	return id, keg.addNodeToDex(ctx, nodeData, &now)
}

func (keg *Keg) Config(ctx context.Context) (*KegConfig, error) {
	if err := keg.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve config: %w", err)
	}

	return keg.Repo.ReadConfig(ctx)
}

func (keg *Keg) UpdateConfig(ctx context.Context, f func(*KegConfig)) error {
	if err := keg.checkKegExists(ctx); err != nil {
		return fmt.Errorf("unable to update config: %w", err)
	}

	// Read config directly from the repository to allow Init to create it when
	// the keg is not yet fully initiated.
	cfg, err := keg.Repo.ReadConfig(ctx)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			cfg = NewKegConfig()
		} else {
			return fmt.Errorf("failed to read config: %w", err)
		}
	}
	f(cfg)
	if err := keg.Repo.WriteConfig(ctx, cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

func (keg *Keg) SetConfig(ctx context.Context, data []byte) error {
	if err := keg.checkKegExists(ctx); err != nil {
		return fmt.Errorf("unable to set config: %w", err)
	}
	cfg, err := ParseKegConfig(data)
	if err != nil {
		return fmt.Errorf("unable to parse config: %w", err)
	}
	if err := keg.Repo.WriteConfig(ctx, cfg); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

// GetContent retrieves content from a node.
func (keg *Keg) GetContent(ctx context.Context, id Node) ([]byte, error) {
	if err := keg.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve node content: %w", err)
	}

	b, err := keg.Repo.ReadContent(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to read content: %w", err)
	}
	return b, nil
}

// SetContent sets the content of the node and updates the meta and dex.
func (keg *Keg) SetContent(ctx context.Context, id Node, data []byte) error {
	if err := keg.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to set node content: %w", err)
	}

	// write content
	if err := keg.Repo.WriteContent(ctx, id, data); err != nil {
		return fmt.Errorf("unable to write content: %w", err)
	}
	return keg.IndexNode(ctx, id)
}

// GetMeta gets the meta for a node.
func (keg *Keg) GetMeta(ctx context.Context, id Node) (*Meta, error) {
	if err := keg.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to get node meta: %w", err)
	}
	return keg.getMeta(ctx, id)
}

func (keg *Keg) SetMeta(ctx context.Context, id Node, meta *Meta) error {
	if err := keg.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to update node meta: %w", err)
	}

	// write back
	if err := keg.Repo.WriteMeta(ctx, id, []byte(meta.ToYAML())); err != nil {
		return fmt.Errorf("UpdateMeta: write meta to backend %s: %w", keg.Repo.Name(), err)
	}

	clk := clock.ClockFromContext(ctx)
	now := clk.Now()
	nodeData := &NodeData{ID: id, Meta: meta}
	return keg.addNodeToDex(ctx, nodeData, &now)
}

// UpdateMeta updates meta then writes to repo. f is applied to the parsed Meta.
func (keg *Keg) UpdateMeta(ctx context.Context, id Node, f func(*Meta)) error {
	if err := keg.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to update node meta: %w", err)
	}

	clk := clock.ClockFromContext(ctx)
	now := clk.Now()

	m, err := keg.getMeta(ctx, id)
	if errors.Is(err, ErrNotExist) {
		m = NewMeta(ctx, now)
	}

	// apply mutation
	f(m)

	// write back
	if err := keg.Repo.WriteMeta(ctx, id, []byte(m.ToYAML())); err != nil {
		return fmt.Errorf("UpdateMeta: write meta to backend %s: %w", keg.Repo.Name(), err)
	}
	return nil
}

// Touch bumps the access time for a node.
func (keg *Keg) Touch(ctx context.Context, id Node) error {
	if err := keg.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to touch node: %w", err)
	}

	return keg.UpdateMeta(ctx, id, func(m *Meta) {
		clk := clock.ClockFromContext(ctx)
		now := clk.Now()
		m.accessed = now
	})
}

// IndexNode updates the meta data for a node by re-parsing content and applying
// any discovered properties (title, lead, hash, etc). The dex is also updated.
func (keg *Keg) IndexNode(ctx context.Context, id Node) error {
	if err := keg.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to update node: %w", err)
	}

	data, err := keg.getNode(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to build node data: %w", err)
	}
	if !data.ContentChanged() {
		return nil
	}

	clk := clock.ClockFromContext(ctx)
	now := clk.Now()
	data.Meta.Update(ctx, data.Content, &now)

	err = keg.Repo.WriteMeta(ctx, id, []byte(data.Meta.ToYAML()))
	if err != nil {
		return err
	}
	return keg.addNodeToDex(ctx, data, &now)
}

// Index cleans the dex, rebuilds, and then writes to the repo.
func (k *Keg) Index(ctx context.Context, _ Node) error {
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

	var errs []error
	for _, id := range ids {
		data, err := k.getNode(ctx, id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		// Dex.Add expects a *NodeData
		if err := k.dex.Add(ctx, data); err != nil {
			errs = append(errs, err)
		}
	}
	if err := k.dex.Write(ctx, k.Repo); err != nil {
		errs = append(errs, fmt.Errorf("failed to save dex: %w", err))
	}

	return errors.Join(errs...)
}

// Commit commits a node. For the in-memory and filesystem repo semantics this
// removes any temporary code suffix by moving the node directory to the
// canonical numeric id.
func (keg *Keg) Commit(ctx context.Context, id Node) error {
	if err := keg.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to commit node: %w", err)
	}

	// only commit when Code is present (temporary id)
	if id.Code == "" {
		return nil
	}
	dst, err := keg.Repo.Next(ctx)
	if err != nil {
		return err
	}
	if err := keg.Repo.MoveNode(ctx, id, dst); err != nil {
		return err
	}
	return nil
}

// Dex gets the dex. If it is not available attempt to load it from the repo.
func (keg *Keg) Dex(ctx context.Context) (*Dex, error) {
	if err := keg.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve dex: %w", err)
	}

	if keg.dex != nil {
		return keg.dex, nil
	}
	dex, err := NewDexFromRepo(ctx, keg.Repo)
	keg.dex = dex
	return dex, err
}

// -- utility functions

func (keg *Keg) getContent(ctx context.Context, id Node) (*Content, error) {
	raw, err := keg.Repo.ReadContent(ctx, id)
	if err != nil {
		return nil, err
	}
	return ParseContent(ctx, raw, FormatMarkdown)
}

func (keg *Keg) getMeta(ctx context.Context, id Node) (*Meta, error) {
	raw, err := keg.Repo.ReadMeta(ctx, id)
	if err != nil {
		return nil, err
	}
	return ParseMeta(ctx, raw)
}

func (keg *Keg) getNode(ctx context.Context, n Node) (*NodeData, error) {
	content, err := keg.getContent(ctx, n)
	if err != nil {
		return nil, err
	}
	meta, err := keg.getMeta(ctx, n)
	if err != nil {
		return nil, err
	}

	items, err := keg.Repo.ListItems(ctx, n)
	if err != nil {
		return nil, err
	}

	images, err := keg.Repo.ListImages(ctx, n)
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

// addNodeToDex adds the data node to the dex and updates the config timestamp.
func (keg *Keg) addNodeToDex(ctx context.Context, data *NodeData, now *time.Time) error {
	dex, err := keg.Dex(ctx)
	if err != nil {
		return err
	}

	dex.Add(ctx, data)

	if now != nil {
		if err := dex.Write(ctx, keg.Repo); err != nil {
			return err
		}

		if err := keg.UpdateConfig(ctx, func(kc *KegConfig) {
			kc.Updated = now.Format(time.RFC3339)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (keg *Keg) checkKegExists(ctx context.Context) error {
	if keg == nil || keg.Repo == nil {
		return fmt.Errorf("no repository configured")
	}

	exists, err := IsKegInitiated(ctx, keg.Repo)
	if err != nil {
		return fmt.Errorf("failed to check keg existence: %w", err)
	}
	if !exists {
		return fmt.Errorf("keg not initialized: %w", ErrNotExist)
	}
	return nil
}
