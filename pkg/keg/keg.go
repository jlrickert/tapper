package keg

import (
	"context"
	"errors"
	"fmt"
	"sync"

	std "github.com/jlrickert/go-std/pkg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

// Keg is a concrete high-level service backed by a KegRepository.
// It implements common node operations by delegating low-level storage to the repo.
type Keg struct {
	Repo KegRepository

	dex    *Dex
	config *KegConfig

	mu sync.Mutex
}

type KegOption func(*Keg)

func NewKegFromTarget(ctx context.Context, target kegurl.Target) (*Keg, error) {
	return nil, nil
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

type KegCreateOptions struct {
	Title string
	Lead  string
	Tags  []string
	Attrs map[string]any
}

// Create creates a new node: allocates an ID, writes content, creates and writes meta.
func (keg *Keg) Create(ctx context.Context, opts KegCreateOptions) (Node, error) {
	if keg == nil || keg.Repo == nil {
		return Node{}, fmt.Errorf("no repository configured")
	}

	// Reserve next ID
	id, err := keg.Repo.Next(ctx)
	if err != nil {
		return Node{}, NewBackendError("keg", "Create:Next", 0, err, false)
	}

	// Build initial meta
	m := NewMeta(ctx)
	if opts.Title != "" {
		m.SetTitle(ctx, opts.Title)
	}
	if len(opts.Tags) > 0 {
		m.SetTags(ctx, opts.Tags)
	}
	// set created/updated times handled by NewMeta

	// Persist empty content if no attrs/content provided (caller may call SetContent separately)
	// We'll write an empty content file so repo implementations that require a content file are happy.
	if err := keg.Repo.WriteContent(ctx, id, []byte(ZeroNodeContent)); err != nil {
		return Node{}, NewBackendError(keg.Repo.Name(), "Create:WriteContent", 0, err, false)
	}

	// ensure hash is set for the content
	h := HasherFromContext(ctx).Hash([]byte(ZeroNodeContent))
	m.SetHash(ctx, h, true)

	// Persist meta
	if err := keg.Repo.WriteMeta(ctx, id, []byte(m.ToYAML())); err != nil {
		return Node{}, NewBackendError(keg.Repo.Name(), "Create:WriteMeta", 0, err, false)
	}

	return id, nil
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

// SetContent sets the content of the node and updates the meta (hash/updated)
func (keg *Keg) SetContent(ctx context.Context, id Node, data []byte) error {
	if keg == nil || keg.Repo == nil {
		return fmt.Errorf("no repository configured")
	}
	// write content
	if err := keg.Repo.WriteContent(ctx, id, data); err != nil {
		return NewBackendError(keg.Repo.Name(), "SetContent:WriteContent", 0, err, false)
	}

	// update meta.hash (and updated time) via UpdateMeta
	hash := HasherFromContext(ctx).Hash(data)
	if err := keg.UpdateMeta(ctx, id, func(m *Meta) {
		m.SetHash(ctx, hash, true)
	}); err != nil {
		return err
	}

	return nil
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

// SetMeta sets the meta in the repo
func (keg *Keg) SetMeta(ctx context.Context, id Node, data []byte) error {
	if keg == nil || keg.Repo == nil {
		return fmt.Errorf("no repository configured")
	}
	if err := keg.Repo.WriteMeta(ctx, id, data); err != nil {
		return NewBackendError(keg.Repo.Name(), "SetMeta:WriteMeta", 0, err, false)
	}
	return nil
}

// UpdateMeta updates meta then writes to repo. f is applied to the parsed Meta.
func (keg *Keg) UpdateMeta(ctx context.Context, id Node, f func(*Meta)) error {
	if keg == nil || keg.Repo == nil {
		return fmt.Errorf("no repository configured")
	}

	// Read existing meta
	raw, err := keg.Repo.ReadMeta(ctx, id)
	if err != nil {
		// If node missing, propagate
		if errors.Is(err, ErrNodeNotFound) {
			return fmt.Errorf("unable to create node: %w", err)
		}
		// If repo returns ErrNotFound for meta, treat as empty meta and proceed
		// (different repos behave differently).
		// For any other backend error, wrap and return.

		// if it's a typed backend error wrap
		return NewBackendError(keg.Repo.Name(), "UpdateMeta:ReadMeta", 0, err, false)
	}

	var m *Meta
	if len(raw) == 0 {
		m = NewMeta(ctx)
	} else {
		pm, perr := ParseMeta(ctx, raw)
		if perr != nil {
			// couldn't parse existing meta, start fresh
			m = NewMeta(ctx)
		} else {
			m = pm
		}
	}

	// apply mutation
	f(m)

	// write back
	if err := keg.Repo.WriteMeta(ctx, id, []byte(m.ToYAML())); err != nil {
		return NewBackendError(keg.Repo.Name(), "UpdateMeta:WriteMeta", 0, err, false)
	}
	return nil
}

// Touch bumps the access time for a node. This convenience function locates a
// repository via NewFsRepoFromEnvOrSearch and applies the access time update.
func (keg *Keg) Touch(ctx context.Context, id Node) error {
	repo, err := NewFsRepoFromEnvOrSearch(ctx)
	if err != nil {
		return err
	}
	k := NewKeg(repo)
	clock := std.ClockFromContext(ctx)
	return k.UpdateMeta(ctx, id, func(m *Meta) {
		m.SetAccessed(ctx, clock.Now())
	})
}

// Update updates the meta data for a node by re-parsing content and applying
// any discovered properties (title, lead, hash). Convenience wrapper that
// locates repository via NewFsRepoFromEnvOrSearch and operates on it.
func (keg *Keg) Update(ctx context.Context, id Node) error {
	// Read content (may be nil)
	data, err := keg.Repo.ReadContent(ctx, id)
	if err != nil {
		return err
	}
	// Parse content (detect format using filename hint)
	var content *Content
	if len(data) > 0 {
		c, perr := ParseContent(ctx, data, MarkdownContentFilename)
		if perr != nil {
			// tolerate parse error by skipping content-driven updates
			content = nil
		} else {
			content = c
		}
	}

	// Apply updates to meta
	if err := keg.UpdateMeta(ctx, id, func(m *Meta) {
		if content != nil {
			if content.Title != "" {
				m.SetTitle(ctx, content.Title)
			}
			if content.Lead != "" {
				// store lead as generic key to preserve it in YAML; Meta doesn't have explicit Lead setter
				_ = m.Set(ctx, "lead", content.Lead)
			}
			// set hash if present
			if content.Hash != "" {
				m.SetHash(ctx, content.Hash, true)
			}
		}
	}); err != nil {
		return err
	}

	return nil
}

// Index cleans the dex, rebuilds, and then writes to the repo. It uses the
// repository discovered via NewFsRepoFromEnvOrSearch and the Dex helpers to
// build and write indexes.
func (k *Keg) Index(ctx context.Context, _ Node) error {
	// Build a fresh in-memory Dex
	if k.dex == nil {
		k.dex = &Dex{}
	}

	// List node ids and populate dex
	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return NewBackendError(k.Repo.Name(), "Index:ListNodes", 0, err, false)
	}

	var errs []error
	for _, id := range ids {
		data, err := k.getNode(ctx, id)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		err = k.dex.Add(ctx, *data)
		if err != nil {
			errs = append(errs, err)
		}
	}
	err = k.dex.Write(ctx, k.Repo)
	if err != nil {
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
	dst := Node{ID: id.ID}
	if err := keg.Repo.MoveNode(ctx, id, dst); err != nil {
		return err
	}
	return nil
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
		ID:       n.Path(),
		Hash:     meta.hash,
		Title:    content.Title,
		Lead:     content.Lead,
		Links:    content.Links,
		Format:   content.Format,
		Updated:  meta.Updated(),
		Created:  meta.Created(),
		Accessed: meta.Accessed(),
		Tags:     meta.Tags(),
		Items:    items,
		Images:   images,
	}, nil
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
