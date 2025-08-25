package keg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Keg is a concrete high-level service backed by a KegRepository.
// It implements common node operations by delegating low-level storage to the repo.
type Keg struct {
	Repo KegRepository
	Dex  *Dex

	deps   *Deps
	Config *Config

	mu sync.Mutex
}

// NewKeg returns a Keg service backed by the provided repository.
func NewKeg(repo KegRepository, opts ...KegOption) *Keg {
	deps := applyKegOptions(opts...)
	deps.applyDefaults()
	return &Keg{Repo: repo, deps: deps, Config: &Config{}}
}

func (keg *Keg) LoadConfig(ctx context.Context) error {
	cfg, err := keg.Repo.ReadConfig(ctx)
	if err == nil {
		keg.Config = cfg
	}

	// Propagate the error to the caller.
	return err
}

// Initiates a brand new keg
func (k *Keg) Init(ctx context.Context) error {
	// Check to see if keg already exists
	_, err := k.Repo.ReadConfig(ctx)
	if err == nil {
		return ErrKegExists
	}
	if !IsNotFound(err) {
		return fmt.Errorf("unable to init keg: %w", err)
	}

	k.Config = NewConfigWithDeps(k.deps)
	// Create the keg file
	err = k.Repo.WriteConfig(ctx, *k.Config)
	if err != nil {
		return fmt.Errorf("unable to create keg config: %w", err)
	}

	_, err = k.CreateNode(ctx, NodeCreateOptions{Content: []byte(ZeroNodeContent)})
	if err != nil {
		return fmt.Errorf("unable to create zero node: %w", err)
	}

	return nil
}

// NextID allocates or returns next available id via repo.
func (k *Keg) NextID(ctx context.Context) (NodeID, error) {
	return k.Repo.Next(ctx)
}

// CreateNode creates a node with options (content/meta) and returns id.
func (k *Keg) CreateNode(ctx context.Context, opts NodeCreateOptions) (NodeID, error) {
	var id NodeID
	var err error
	if opts.ID == 0 {
		opts.ID, err = k.Repo.Next(ctx)
		if err != nil {
			return NodeID(0), err
		}
	}

	metaData, _ := opts.Meta.ToBytes()

	var errs []error
	err = k.Repo.WriteContent(ctx, opts.ID, opts.Content)
	if err != nil {
		errs = append(errs, err)
	}
	err = k.Repo.WriteMeta(ctx, opts.ID, metaData)
	if err != nil {
		errs = append(errs, err)
	}

	if opts.ID != 0 {
		id = opts.ID
		var nf *NodeNotFoundError
		if errors.As(err, &nf) {
			return NodeID(0), fmt.Errorf("node exists: %w", err)
		}
	} else {
		id, err = k.Repo.Next(ctx)
		if err != nil {
			return 0, fmt.Errorf("next id: %w", err)
		}
	}

	// write meta (ensure timestamps)
	if opts.Meta == nil {
		opts.Meta = NewMetaFromRaw(map[string]any{}, k.deps)
		opts.Meta.Touch()
	}
	opts.Meta.Touch()
	if opts.Meta != nil {
		if err := k.WriteMeta(ctx, id, opts.Meta); err != nil {
			return 0, fmt.Errorf("write meta: %w", err)
		}
	}
	if len(opts.Content) > 0 {
		if err := k.WriteContent(ctx, id, opts.Content); err != nil {
			return 0, fmt.Errorf("write content: %w", err)
		}
	}
	return id, nil
}

// UpdateNode refreshes the metadata and content for the specified [NodeID] by
// reading from the repository, normalizing tags, updating timestamps, and
// persisting changes atomically to maintain index consistency
func (k *Keg) UpdateNode(ctx context.Context, id NodeID) error {
	// Acquire a per-node lock to avoid races with concurrent updates/reads.
	if k.Repo == nil {
		return fmt.Errorf("no repository configured")
	}
	unlock, err := k.Repo.LockNode(ctx, id, time.Millisecond*100)
	if err != nil {
		return fmt.Errorf("unable to lock node: %w", err)
	}
	defer unlock()

	// Read content (if present) and parse it to extract title/lead/links.
	var content *Content
	contentBytes, cerr := k.Repo.ReadContent(ctx, id)
	if cerr != nil && !IsNotFound(cerr) {
		return fmt.Errorf("read content: %w", cerr)
	}
	if len(contentBytes) > 0 {
		c, perr := ParseContent(contentBytes, FormatMarkdown, k.deps)
		if perr != nil {
			return fmt.Errorf("parse content: %w", perr)
		}
		content = c
	}

	// Read meta (if missing, create a new meta).
	meta, merr := k.ReadMeta(ctx, id)
	if merr != nil {
		if IsNotFound(merr) {
			meta = NewMeta(k.deps)
			// ensure created/updated/accessed are set reasonably
			meta.SetCreated(time.Now().UTC())
		} else {
			return fmt.Errorf("read meta: %w", merr)
		}
	}

	// Merge parsed content into meta where appropriate.
	if content != nil {
		// Allow Meta to ingest content-derived fields (title/lead/links).
		meta.LoadContent(content)
	}

	// Persist meta back to the repository.
	if err := k.WriteMeta(ctx, id, meta); err != nil {
		return fmt.Errorf("write meta: %w", err)
	}

	return nil
}

// ReadContent reads node content and touches the meta data
func (k *Keg) ReadContent(ctx context.Context, id NodeID) ([]byte, error) {
	// Ensure repo is present
	if k.Repo == nil {
		return nil, fmt.Errorf("no repository configured")
	}

	// Read the content from the repository.
	b, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		return nil, err
	}

	// Attempt to read and update the node's meta (touch accessed/updated timestamps).
	rawMeta, merr := k.Repo.ReadMeta(ctx, id)
	if merr != nil {
		// If meta is missing, nothing to touch; return content.
		if IsNotFound(merr) {
			return b, nil
		}
		// For other meta read errors, return the content and surface the meta read error.
		return b, fmt.Errorf("read meta: %w", merr)
	}

	meta, perr := ParseMeta(rawMeta, k.deps)
	if perr != nil {
		// If meta cannot be parsed, return content and report parse error.
		return b, fmt.Errorf("parse meta: %w", perr)
	}

	// Touch the meta to update accessed timestamp (Meta.Touch will use injected deps/clock when available).
	meta.Touch()

	// Persist updated meta back to repo. Use k.WriteMeta to serialize and write atomically.
	if werr := k.WriteMeta(ctx, id, meta); werr != nil {
		// Return content but surface the write error.
		return b, fmt.Errorf("update meta: %w", werr)
	}

	return b, nil
}

// ReadMeta reads and parse
func (k *Keg) ReadMeta(ctx context.Context, id NodeID) (*Meta, error) {
	if k.Repo == nil {
		return nil, fmt.Errorf("no repository configured")
	}

	data, err := k.Repo.ReadMeta(ctx, id)
	if err != nil {
		if IsNotFound(err) {
			return nil, err
		}
		return nil, fmt.Errorf("read meta: %w", err)
	}

	meta, perr := ParseMeta(data, k.deps)
	if perr != nil {
		return NewMeta(k.deps), fmt.Errorf("parse meta: %w", perr)
	}
	return meta, nil
}

// ResolveLink resolves a token like "repo", "keg:owner/123", or "keg:alias" to
// a concrete URL. If a LinkResolver was injected at construction time it will
// be used. Otherwise a minimal resolver based on the repository Config.Links
// is used.
//
// Notes:
//   - The minimal resolver looks up aliases from the Config.Links slice
//     (case-insensitive).
//   - For tokens of the form "keg:owner/<nodeid>" it will attempt to find an
//     alias whose alias matches the owner and, if found, return baseURL +
//     "/docs/<nodeid>". This is a heuristic fallback and callers that need
//     precise behavior should inject a LinkResolver that implements the
//     desired mapping rules.
func (k *Keg) ResolveLink(ctx context.Context, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("empty token")
	}

	if k.Config == nil {
		return "", fmt.Errorf("keg not initated")
	}

	// Load repo config if available. Treat missing config as empty config.
	var cfg Config
	if k.Config != nil && k.Repo != nil {
		cfgPtr, err := k.Repo.ReadConfig(ctx)
		if err != nil && !IsNotFound(err) {
			return "", fmt.Errorf("read config: %w", err)
		}
		if cfgPtr != nil {
			cfg = *cfgPtr
		}
	}

	// Choose resolver: injected one wins, otherwise use the basic resolver.
	resolved, err := k.deps.Resolver.Resolve(cfg, token)
	if err != nil {
		return "", fmt.Errorf("unable to resolve token: %s", token)
	}
	return resolved, nil
}

func (k *Keg) GetNodeData(ctx context.Context, id NodeID) ([]byte, error) {
	b, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		return nil, err
	}

	var errs []error

	metaData, err := k.Repo.ReadMeta(ctx, id)
	if err != nil {
		errs = append(errs, err)
	}
	meta, err := ParseMeta(metaData, k.deps)
	if err != nil {
		errs = append(errs, err)
	}

	if meta == nil {
		meta = NewMeta(k.deps)
	}
	meta.Touch()

	metaData, err = meta.ToBytes()
	if err != nil {
		errs = append(errs, err)
	}
	err = k.Repo.WriteContent(ctx, id, metaData)

	return b, err
}

// GetNode composes meta, content and ancillary lists into a Node. Doesn't
// update stamps
func (k *Keg) GetNode(ctx context.Context, id NodeID) (Node, error) {
	nodeExists := true
	var errs []error
	unlock, err := k.Repo.LockNode(ctx, id, time.Millisecond*100)
	if err != nil {
		return Node{}, fmt.Errorf("unable to lock node: %w", err)
	}
	defer unlock()

	contentBytes, err := k.Repo.ReadContent(ctx, id)

	var content *Content
	if nodeExists && errors.Is(err, ErrNodeNotFound) {
		errs = append(errs, err)
		nodeExists = false
	} else if len(contentBytes) > 0 {
		content, err = ParseContent(contentBytes, FormatMarkdown, k.deps)
		if err != nil {
			errs = append(errs, fmt.Errorf("parse content: %w", err))
		}
	}

	metaBytes, err := k.Repo.ReadMeta(ctx, id)
	var meta *Meta
	if nodeExists && errors.Is(err, ErrNodeNotFound) {
		errs = append(errs, err)
		nodeExists = false
	}
	meta, err = ParseMeta(metaBytes, k.deps)
	if err != nil {
		errs = append(errs, fmt.Errorf("unable to parse meta: %w", err))
	}

	items, _ := k.Repo.ListItems(ctx, id)   // wrap errors if desired
	images, _ := k.Repo.ListImages(ctx, id) // wrap errors if desired

	var title string
	titleData, ok := meta.Get("title")
	if !ok {
		title = ""
	} else {
		title = titleData.(string)
	}

	return Node{
		ID:      id,
		Meta:    meta,
		Content: content,
		Items:   items,
		Images:  images,
		Ref: NodeRef{
			ID:      id,
			Title:   title,
			Updated: meta.GetUpdated(),
		},
	}, nil
}

// // ReadContent returns parsed Content for a node (may be nil).
//
//	func (k *Keg) GetContent(ctx context.Context, id NodeID) (*Content, error) {
//		b, err := k.Repo.ReadContent(ctx, id)
//		if err != nil && !(errors.Is(err, ErrContentNotFound) || errors.Is(err, ErrNotFound)) {
//			return nil, fmt.Errorf("unable to read content form node %b: %w", id, err)
//		}
//
//		var errs []error
//		var content *Content
//		if len(b) > 0 {
//			content, err = ParseContent(b, MarkdownContentFilename)
//			if err != nil {
//				errs = append(errs, err)
//			}
//		} else {
//			content = &Content{}
//		}
//
//		meta, err := k.ReadMeta(ctx, id)
//
//		return content, err
//	}
//
// // ReadMeta returns parsed Meta for a node.
//
//	func (k *Keg) ReadMeta(ctx context.Context, id NodeID) (*Meta, error) {
//		b, err := k.Repo.ReadMeta(ctx, id)
//		if errors.Is(err, ErrNodeNotFound) {
//			return nil, err
//		}
//		if err != nil {
//			return nil, err
//		}
//		return ParseMeta(b)
//	}
//
// WriteContent writes README content and (optionally) updates meta.updated.
func (k *Keg) WriteContent(ctx context.Context, id NodeID, data []byte) error {
	// TODO: acquire lock, validate, backup .new behaviour
	if err := k.Repo.WriteContent(ctx, id, data); err != nil {
		return fmt.Errorf("write content: %w", err)
	}
	// bump updated timestamp in meta if present
	meta, err := k.ReadMeta(ctx, id)
	if err == nil {
		meta.SetUpdated(time.Now().UTC())
		out, err := meta.ToBytes()
		if err == nil {
			_ = k.Repo.WriteMeta(ctx, id, out)
		}
	}
	return nil
}

// WriteMeta writes meta bytes (atomic at repo level).
func (k *Keg) WriteMeta(ctx context.Context, id NodeID, meta *Meta) error {
	out, err := meta.ToBytes()
	if err != nil {
		return fmt.Errorf("serialize meta: %w", err)
	}
	if err := k.Repo.WriteMeta(ctx, id, out); err != nil {
		return fmt.Errorf("write meta: %w", err)
	}
	return nil
}

// UpdateMeta reads meta, runs a mutator, serializes & writes meta back.
func (k *Keg) UpdateMeta(ctx context.Context, id NodeID, mut func(*Meta) error) error {
	raw, err := k.Repo.ReadMeta(ctx, id)
	if err != nil {
		return fmt.Errorf("read meta: %w", err)
	}
	meta, err := ParseMeta(raw, k.deps)
	if err != nil {
		return fmt.Errorf("parse meta: %w", err)
	}
	if err := mut(meta); err != nil {
		return fmt.Errorf("mutate meta: %w", err)
	}
	out, err := meta.ToBytes()
	if err != nil {
		return fmt.Errorf("serialize meta: %w", err)
	}
	if err := k.Repo.WriteMeta(ctx, id, out); err != nil {
		return fmt.Errorf("write meta: %w", err)
	}
	return nil
}

// DeleteNode removes a node and associated artifacts.
func (k *Keg) DeleteNode(ctx context.Context, id NodeID) error {
	// TODO: acquire repo lock, prevent accidental deletes, add dry-run
	if err := k.Repo.DeleteNode(ctx, id); err != nil {
		return fmt.Errorf("delete node %d: %w", id, err)
	}
	return nil
}
