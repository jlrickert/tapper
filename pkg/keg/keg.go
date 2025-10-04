package keg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	std "github.com/jlrickert/go-std/pkg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

// Keg is a concrete high-level service backed by a KegRepository.
// It implements common node operations by delegating low-level storage to the repo.
type Keg struct {
	Repo KegRepository

	dex *Dex
	mu  sync.Mutex
}

type KegOption func(*Keg)

func NewKegFromTarget(ctx context.Context, target kegurl.Target) (*Keg, error) {
	switch target.Scheme() {
	case kegurl.SchemeFile:
		repo := FsRepo{
			Root:            target.Path(),
			ContentFilename: MarkdownContentFilename,
			MetaFilename:    YAMLMetaFilename,
		}
		keg := Keg{Repo: &repo}
		return &keg, nil
	}
	return nil, fmt.Errorf("target not supported: %w", errors.ErrUnsupported)
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

// KegExists checks if a keg has been initiated within the repo. It verifies
// that the keg config exists and that a zero node is present.
func KegExists(ctx context.Context, repo KegRepository) (bool, error) {
	if repo == nil {
		return false, fmt.Errorf("no repository provided")
	}

	// Check for a config. If it is missing, keg is not initialized.
	if _, err := repo.ReadConfig(ctx); err != nil {
		if errors.Is(err, ErrNotExist) {
			return false, nil
		}
		// Some backends may return other typed errors; wrap them for callers.
		return false, NewBackendError(repo.Name(), "KegExists:ReadConfig", 0, err, false)
	}

	// Ensure a zero node exists by listing nodes and looking for ID 0.
	ids, err := repo.ListNodes(ctx)
	if err != nil {
		return false, NewBackendError(repo.Name(), "KegExists:ListNodes", 0, err, false)
	}
	for _, n := range ids {
		if n.ID == 0 {
			return true, nil
		}
	}
	return false, nil
}

// Init creates a new keg
func (keg *Keg) Init(ctx context.Context) error {
	if keg == nil || keg.Repo == nil {
		return fmt.Errorf("no repository configured")
	}

	// refuse to init when a keg already exists
	exists, err := KegExists(ctx, keg.Repo)
	if err != nil {
		return err
	}
	if exists {
		return ErrExist
	}

	if err := keg.UpdateConfig(ctx, func(kc *KegConfig) {}); err != nil {
		return err
	}

	// Create has a special case for the zero node
	_, err = keg.Create(ctx, nil)
	return err
}

type KegCreateOptions struct {
	Title string
	Lead  string
	Tags  []string
	Attrs map[string]any
}

// Create creates a new node: allocates an ID, writes content, creates and writes meta.
func (keg *Keg) Create(ctx context.Context, opts *KegCreateOptions) (Node, error) {
	if keg == nil || keg.Repo == nil {
		return Node{}, fmt.Errorf("no repository configured")
	}

	if opts == nil {
		opts = &KegCreateOptions{}
	}

	// Reserve next ID
	id, err := keg.Repo.Next(ctx)
	if err != nil {
		return Node{}, NewBackendError("keg", "Create:Next", 0, err, false)
	}

	clock := std.ClockFromContext(ctx)
	now := clock.Now()

	var rawContent string
	var content *Content

	m := NewMeta(ctx, now)
	m.SetAttrs(ctx, opts.Attrs)

	// Special case for the 0 node
	if id.ID == 0 {
		rawContent = RawZeroNodeContent
		zeroContent, _ := ParseContent(ctx, []byte(rawContent), MarkdownContentFilename)
		content = zeroContent
		m.Update(ctx, content, &now)
	} else {
		b := strings.Builder{}
		if opts.Title != "" {
			m.SetTitle(ctx, opts.Title)
			b.WriteString(fmt.Sprintf("# %s\n", opts.Title))
		} else {
			b.WriteString(fmt.Sprintf("# Node %s\n", id.Path()))
		}

		if opts.Lead != "" {
			m.SetLead(ctx, opts.Lead)
			b.WriteString(fmt.Sprintf("\n%s\n", opts.Lead))
		}

		if len(opts.Tags) != 0 {
			m.SetTags(ctx, opts.Tags)
		}

		rawContent = b.String()
		content, _ = ParseContent(ctx, []byte(rawContent), MarkdownContentFilename)
	}

	// Persist empty content if no attrs/content provided (caller may call SetContent separately)
	// We'll write an empty content file so repo implementations that require a content file are happy.
	if err := keg.Repo.WriteContent(ctx, id, []byte(rawContent)); err != nil {
		return id, NewBackendError(keg.Repo.Name(), "Create:WriteContent", 0, err, false)
	}
	if err := keg.Repo.WriteMeta(ctx, id, []byte(m.ToYAML())); err != nil {
		return id, NewBackendError(keg.Repo.Name(), "Create:WriteContent", 0, err, false)
	}

	return id, keg.indexNode(ctx, &NodeData{ID: id, Content: content, Meta: m})
}

func (keg *Keg) Config(ctx context.Context) (*KegConfig, error) {
	return keg.Repo.ReadConfig(ctx)
}

func (keg *Keg) UpdateConfig(ctx context.Context, f func(*KegConfig)) error {
	cfg, err := keg.Config(ctx)
	if errors.Is(err, ErrNotExist) {
		cfg = NewKegConfig()
	} else {
		return err
	}
	f(cfg)
	return keg.Repo.WriteConfig(ctx, cfg)
}

// Content retrieves content from a node
func (keg *Keg) Content(ctx context.Context, id Node) ([]byte, error) {
	if keg == nil || keg.Repo == nil {
		return nil, fmt.Errorf("no repository configured")
	}
	b, err := keg.Repo.ReadContent(ctx, id)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// SetContent sets the content of the node and updates the meta and dex
func (keg *Keg) SetContent(ctx context.Context, id Node, data []byte) error {
	if keg == nil || keg.Repo == nil {
		return fmt.Errorf("no repository configured")
	}
	// write content
	if err := keg.Repo.WriteContent(ctx, id, data); err != nil {
		return NewBackendError(keg.Repo.Name(), "SetContent:WriteContent", 0, err, false)
	}
	return keg.Update(ctx, id)
}

// Meta gets the meta for a node
func (keg *Keg) Meta(ctx context.Context, id Node) ([]byte, error) {
	if keg == nil || keg.Repo == nil {
		return nil, fmt.Errorf("no repository configured")
	}
	b, err := keg.Repo.ReadMeta(ctx, id)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// UpdateMeta updates meta then writes to repo. f is applied to the parsed Meta.
func (keg *Keg) UpdateMeta(ctx context.Context, id Node, f func(*Meta)) error {
	if keg == nil || keg.Repo == nil {
		return fmt.Errorf("no repository configured")
	}

	clock := std.ClockFromContext(ctx)
	now := clock.Now()

	m, err := keg.getMeta(ctx, id)
	if errors.Is(err, ErrNotExist) {
		m = NewMeta(ctx, now)
	}

	// apply mutation
	f(m)

	// write back
	if err := keg.Repo.WriteMeta(ctx, id, []byte(m.ToYAML())); err != nil {
		return NewBackendError(keg.Repo.Name(), "UpdateMeta:WriteMeta", 0, err, false)
	}
	return nil
}

// Touch bumps the access time for a node.
func (keg *Keg) Touch(ctx context.Context, id Node) error {
	return keg.UpdateMeta(ctx, id, func(m *Meta) {
		clock := std.ClockFromContext(ctx)
		now := clock.Now()
		m.accessed = now
	})
}

// Update updates the meta data for a node by re-parsing content and applying
// any discovered properties (title, lead, hash, etc). The dex is also
// incrementally updated
func (keg *Keg) Update(ctx context.Context, id Node) error {
	data, err := keg.getNode(ctx, id)
	if err != nil {
		return fmt.Errorf("unable to update: %w", err)
	}
	if !data.ContentChanged() {
		return nil
	}
	return keg.indexNode(ctx, data)
}

// Index cleans the dex, rebuilds, and then writes to the repo. It uses the
// repository discovered via NewFsRepoFromEnvOrSearch and the Dex helpers to
// build and write indexes.
func (k *Keg) Index(ctx context.Context, _ Node) error {
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
		return fmt.Errorf("unable to re index: %w", err)
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
		errs = append(errs, fmt.Errorf("unable to save dex: %w", err))
	}

	return errors.Join(errs...)
}

// Commit commits a node. For the in-memory and filesystem repo semantics this
// removes any "temporary code" from the NodeID by moving the node directory
// from the code suffix to the canonical numeric id (if needed).
func (keg *Keg) Commit(ctx context.Context, id Node) error {
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

// Dex gets the dex. If it not available attempt to load it from the repo
func (k *Keg) Dex(ctx context.Context) (*Dex, error) {
	if k.dex != nil {
		return k.dex, nil
	}
	dex, err := NewDexFromRepo(ctx, k.Repo)
	k.dex = dex
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

// indexNode adds the data node to the dex and updates the config timestamp
func (keg *Keg) indexNode(ctx context.Context, data *NodeData) error {
	clock := std.ClockFromContext(ctx)
	now := clock.Now()
	dex, err := keg.Dex(ctx)
	if err != nil {
		return err
	}
	dex.Add(ctx, data)
	if err := dex.Write(ctx, keg.Repo); err != nil {
		return err
	}

	if err := keg.UpdateConfig(ctx, func(kc *KegConfig) {
		kc.Updated = now.Format(time.RFC3339)
	}); err != nil {
		return err
	}
	return nil
}
