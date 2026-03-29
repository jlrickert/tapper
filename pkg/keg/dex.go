package keg

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

// Dex provides a high-level, in-memory view of the repository's generated
// dex indices: nodes, tags, links, backlinks, and changes. It is a convenience
// wrapper used by index builders and other tooling to read or inspect index
// data without dealing directly with repository I/O. Dex does not perform any
// I/O itself; callers are responsible for providing a Repository when writing
// indices.
type Dex struct {
	// nodes is the list of nodes sorted by node id.
	nodes NodeIndex

	// tags maps a tag to a list of nodes that has a tag
	tags TagIndex

	// links maps a node to nodes that it links too
	links LinkIndex

	// backlinks maps a node to other nodes linking to it
	backlinks BacklinkIndex

	// changes is the reverse-chronological list of all nodes.
	changes ChangesIndex

	// custom holds config-driven query-filtered index builders.
	custom []IndexBuilder

	// queryResolver is an optional callback injected via WithQueryResolver.
	// When non-nil, it is passed to QueryFilteredIndex constructors to enable
	// query terms beyond plain tags (e.g. key=value attribute predicates).
	queryResolver func(term string, data *NodeData) bool

	mu sync.RWMutex
}

// DexOption is a functional option for NewDexFromRepo.
type DexOption func(*Dex) error

// WithConfig builds DexOptions from a keg Config. It iterates cfg.Indexes and
// creates a QueryFilteredIndex for each entry that:
//   - has a non-empty Query (or deprecated Tags) field, and
//   - is not one of the core protected index names.
//
// The short file name used with repo.WriteIndex is derived by stripping any
// leading "dex/" prefix from entry.File.
//
// By default, the index evaluates tag expressions against node tag sets. To
// support richer query terms (e.g. key=value attribute predicates), pass
// WithQueryResolver to inject a custom resolver callback.
func WithConfig(cfg *Config) DexOption {
	return func(d *Dex) error {
		if cfg == nil {
			return nil
		}
		for _, entry := range cfg.Indexes {
			if IsCoreIndex(entry.File) {
				continue
			}
			query := entry.QueryOrTags()
			if query == "" {
				continue
			}
			// Strip the "dex/" prefix to get the short name for repo.WriteIndex.
			shortName := strings.TrimPrefix(entry.File, "dex/")
			sortOrder := QueryFilteredSortOrder(entry.Sort)
			idx, err := NewQueryFilteredIndexWithSort(shortName, query, d.queryResolver, sortOrder)
			if err != nil {
				return fmt.Errorf("dex: config index %q: %w", entry.File, err)
			}
			d.custom = append(d.custom, idx)
		}
		return nil
	}
}

// WithQueryResolver sets a custom query term resolver for config-driven custom
// indexes. When set, each term in a query expression is resolved by calling
// resolve(term, data) for each node, instead of the default tag-only resolver.
// This enables key=value attribute predicates and other term types defined in
// higher-level packages (e.g. pkg/tapper).
func WithQueryResolver(resolve func(term string, data *NodeData) bool) DexOption {
	return func(d *Dex) error {
		d.queryResolver = resolve
		return nil
	}
}

// NewDexFromRepo loads available index artifacts ("nodes.tsv", "tags", "links",
// "backlinks", "changes.md") from the provided repository and returns a Dex
// populated with parsed indexes. Missing or empty index files are treated as
// empty datasets and do not cause an error. Additional DexOptions (e.g.
// WithConfig) can be supplied to configure optional behaviour such as
// tag-filtered custom indexes.
//
// All 5 index files are read and parsed concurrently for faster loading.
func NewDexFromRepo(ctx context.Context, repo Repository, opts ...DexOption) (*Dex, error) {
	d := &Dex{}

	// Each goroutine writes to its own result slot; no shared mutable state.
	var (
		nodes     NodeIndex
		tags      TagIndex
		links     LinkIndex
		backlinks BacklinkIndex
		changes   ChangesIndex

		nodeErr     error
		tagErr      error
		linkErr     error
		backlinkErr error
		changeErr   error
	)

	var wg sync.WaitGroup

	wg.Go(func() {
		data, err := repo.GetIndex(ctx, "nodes.tsv")
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				nodes = NodeIndex{}
			} else {
				nodeErr = fmt.Errorf("unable to read `nodes.tsv` index: %w", err)
			}
			return
		}
		ni, err := ParseNodeIndex(ctx, data)
		if err != nil {
			nodeErr = fmt.Errorf("unable to parse `nodes.tsv` index: %w", err)
			nodes = NodeIndex{}
		} else {
			nodes = ni
		}
	})

	wg.Go(func() {
		data, err := repo.GetIndex(ctx, "tags")
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				tags = TagIndex{}
			} else {
				tagErr = fmt.Errorf("unable to read `tags` index: %w", err)
			}
			return
		}
		ti, err := ParseTagIndex(ctx, data)
		if err != nil {
			tagErr = fmt.Errorf("unable to parse `tags` index: %w", err)
			tags = TagIndex{}
		} else {
			tags = ti
		}
	})

	wg.Go(func() {
		data, err := repo.GetIndex(ctx, "links")
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				links = LinkIndex{}
			} else {
				linkErr = fmt.Errorf("unable to read `links` index: %w", err)
			}
			return
		}
		li, err := ParseLinkIndex(ctx, data)
		if err != nil {
			linkErr = fmt.Errorf("unable to parse `links` index: %w", err)
			links = LinkIndex{}
		} else {
			links = li
		}
	})

	wg.Go(func() {
		data, err := repo.GetIndex(ctx, "backlinks")
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				backlinks = BacklinkIndex{}
			} else {
				backlinkErr = fmt.Errorf("unable to read `backlinks` index: %w", err)
			}
			return
		}
		bi, err := ParseBacklinksIndex(ctx, data)
		if err != nil {
			backlinkErr = fmt.Errorf("unable to parse `backlinks` index: %w", err)
			backlinks = BacklinkIndex{}
		} else {
			if bi != nil {
				backlinks = *bi
			} else {
				backlinks = BacklinkIndex{}
			}
		}
	})

	wg.Go(func() {
		data, err := repo.GetIndex(ctx, "changes.md")
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				changes = ChangesIndex{}
			} else {
				changeErr = fmt.Errorf("unable to read `changes.md` index: %w", err)
			}
			return
		}
		ci, err := ParseChangesIndex(ctx, data)
		if err != nil {
			changeErr = fmt.Errorf("unable to parse `changes.md` index: %w", err)
			changes = ChangesIndex{}
		} else {
			changes = ci
		}
	})

	wg.Wait()

	// Assign results to dex after all goroutines complete.
	d.nodes = nodes
	d.tags = tags
	d.links = links
	d.backlinks = backlinks
	d.changes = changes

	var errs []error
	if nodeErr != nil {
		errs = append(errs, nodeErr)
	}
	if tagErr != nil {
		errs = append(errs, tagErr)
	}
	if linkErr != nil {
		errs = append(errs, linkErr)
	}
	if backlinkErr != nil {
		errs = append(errs, backlinkErr)
	}
	if changeErr != nil {
		errs = append(errs, changeErr)
	}

	// Apply options (e.g. WithConfig to register custom tag-filtered indexes).
	for _, opt := range opts {
		if err := opt(d); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return d, errors.Join(errs...)
	}

	return d, nil
}

// Nodes returns a copy of the parsed nodes index (slice of NodeRef).
func (dex *Dex) Nodes(ctx context.Context) []NodeIndexEntry {
	dex.mu.RLock()
	defer dex.mu.RUnlock()
	return dex.nodes.List(ctx)
}

// TagLinks Tags returns the parsed tags index (map[tag] -> []NodeID).
func (dex *Dex) TagLinks(ctx context.Context, node NodeId) ([]NodeId, bool) {
	return dex.TagNodes(ctx, node.Path())
}

// TagNodes returns the parsed tags index entry for tag (map[tag] -> []NodeID).
func (dex *Dex) TagNodes(ctx context.Context, tag string) ([]NodeId, bool) {
	dex.mu.RLock()
	defer dex.mu.RUnlock()
	tag = NormalizeTag(tag)
	if tag == "" {
		return nil, false
	}
	list, ok := dex.tags.data[tag]
	return list, ok
}

func (dex *Dex) TagList(ctx context.Context) []string {
	dex.mu.RLock()
	defer dex.mu.RUnlock()
	keys := maps.Keys(dex.tags.data)
	return slices.AppendSeq([]string{}, keys)
}

// Links returns the parsed outgoing links index (map[src] -> []dst).
func (dex *Dex) Links(ctx context.Context, node NodeId) ([]NodeId, bool) {
	dex.mu.RLock()
	defer dex.mu.RUnlock()
	list, ok := dex.links.data[node.Path()]
	return list, ok
}

// Backlinks returns the parsed backlinks index (map[dst] -> []src).
// NOTE: not intended to be mutated
func (dex *Dex) Backlinks(ctx context.Context, node NodeId) ([]NodeId, bool) {
	dex.mu.RLock()
	defer dex.mu.RUnlock()
	list, ok := dex.backlinks.data[node.Path()]
	return list, ok
}

// Clear resets all in-memory index data held by the Dex instance.
func (dex *Dex) Clear(ctx context.Context) {
	dex.mu.Lock()
	dex.nodes = NodeIndex{}
	dex.tags = TagIndex{}
	dex.links = LinkIndex{}
	dex.backlinks = BacklinkIndex{}
	_ = dex.changes.Clear(ctx)
	for _, c := range dex.custom {
		_ = c.Clear(ctx)
	}
	dex.mu.Unlock()
}

// Add adds the provided node to all managed indexes. This implements the
// IndexBuilder contract for convenience when using Dex as an aggregated builder.
func (dex *Dex) Add(ctx context.Context, data *NodeData) error {
	dex.mu.Lock()

	var errs []error
	if err := dex.nodes.Add(ctx, data); err != nil {
		errs = append(errs, err)
	}
	if err := dex.tags.Add(ctx, data); err != nil {
		errs = append(errs, err)
	}
	if err := dex.links.Add(ctx, data); err != nil {
		errs = append(errs, err)
	}
	if err := dex.backlinks.Add(ctx, data); err != nil {
		errs = append(errs, err)
	}
	if err := dex.changes.Add(ctx, data); err != nil {
		errs = append(errs, err)
	}
	for _, c := range dex.custom {
		if err := c.Add(ctx, data); err != nil {
			errs = append(errs, err)
		}
	}
	dex.mu.Unlock()
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// Remove removes the node identified by id from all managed indexes. This
// implements the IndexBuilder contract for convenience when using Dex.
func (dex *Dex) Remove(ctx context.Context, node NodeId) error {
	dex.mu.Lock()

	var errs []error
	if err := dex.nodes.Rm(ctx, node); err != nil {
		errs = append(errs, err)
	}
	if err := dex.tags.Rm(ctx, node); err != nil {
		errs = append(errs, err)
	}
	if err := dex.links.Rm(ctx, node); err != nil {
		errs = append(errs, err)
	}
	if err := dex.backlinks.Rm(ctx, node); err != nil {
		errs = append(errs, err)
	}
	if err := dex.changes.Rm(ctx, node); err != nil {
		errs = append(errs, err)
	}
	for _, c := range dex.custom {
		if err := c.Remove(ctx, node); err != nil {
			errs = append(errs, err)
		}
	}
	dex.mu.Unlock()
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func (dex *Dex) NextNode(ctx context.Context) NodeId {
	dex.mu.RLock()
	defer dex.mu.RUnlock()
	return dex.nodes.Next(ctx)
}

// Write serializes the in-memory indexes and writes them atomically to the
// provided repository using WriteIndex. If any write operation fails the error
// chain is returned (errors.Join is used to aggregate multiple errors).
//
// Serialization is performed under a read lock so concurrent readers are not
// blocked. The actual file writes happen after the lock is released, since
// they operate on independent byte buffers and repository WriteIndex calls
// are self-synchronizing (atomic file writes).
func (dex *Dex) Write(ctx context.Context, repo Repository) error {
	// Phase 1: Serialize all index data to byte buffers under read lock.
	type indexPayload struct {
		name string
		data []byte
	}

	dex.mu.RLock()
	var serializeErrs []error

	nodesData, err := dex.nodes.Data(ctx)
	if err != nil {
		serializeErrs = append(serializeErrs, fmt.Errorf("unable to create `nodes.tsv` index: %w", err))
	}
	tagsData, err := dex.tags.Data(ctx)
	if err != nil {
		serializeErrs = append(serializeErrs, fmt.Errorf("unable to create `tags` index: %w", err))
	}
	linksData, err := dex.links.Data(ctx)
	if err != nil {
		serializeErrs = append(serializeErrs, fmt.Errorf("unable to create `links` index: %w", err))
	}
	backlinksData, err := dex.backlinks.Data(ctx)
	if err != nil {
		serializeErrs = append(serializeErrs, fmt.Errorf("unable to create `backlinks` index: %w", err))
	}
	changesData, err := dex.changes.Data(ctx)
	if err != nil {
		serializeErrs = append(serializeErrs, fmt.Errorf("unable to create `changes.md` index: %w", err))
	}

	payloads := []indexPayload{
		{"nodes.tsv", nodesData},
		{"tags", tagsData},
		{"links", linksData},
		{"backlinks", backlinksData},
		{"changes.md", changesData},
	}

	for _, c := range dex.custom {
		data, cErr := c.Data(ctx)
		if cErr != nil {
			serializeErrs = append(serializeErrs, fmt.Errorf("unable to create `%s` index: %w", c.Name(), cErr))
		}
		payloads = append(payloads, indexPayload{c.Name(), data})
	}

	dex.mu.RUnlock()

	// Phase 2: Write all serialized buffers to the repository in parallel.
	// No dex lock needed — we're writing independent byte buffers and
	// repository WriteIndex calls use atomic file replacement.
	var writeErrs []error
	var errsMu sync.Mutex
	var wg sync.WaitGroup

	for _, p := range payloads {
		p := p // capture
		wg.Go(func() {
			if err := repo.WriteIndex(ctx, p.name, p.data); err != nil {
				errsMu.Lock()
				writeErrs = append(writeErrs, fmt.Errorf("unable to write `%s` index: %w", p.name, err))
				errsMu.Unlock()
			}
		})
	}
	wg.Wait()

	allErrs := append(serializeErrs, writeErrs...)
	if len(allErrs) == 0 {
		return nil
	}

	return fmt.Errorf("unable to write dex: %w", errors.Join(allErrs...))
}

func (dex *Dex) GetRef(ctx context.Context, id NodeId) *NodeIndexEntry {
	if dex == nil {
		return nil
	}
	dex.mu.RLock()
	defer dex.mu.RUnlock()
	return dex.nodes.Get(ctx, id)
}
