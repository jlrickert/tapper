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

	// custom holds config-driven tag-filtered index builders.
	custom []IndexBuilder

	mu sync.RWMutex
}

// DexOption is a functional option for NewDexFromRepo.
type DexOption func(*Dex) error

// WithConfig builds DexOptions from a keg Config. It iterates cfg.Indexes and
// creates a TagFilteredIndex for each entry that:
//   - has a non-empty Tags field, and
//   - is not one of the core protected index names.
//
// The short file name used with repo.WriteIndex is derived by stripping any
// leading "dex/" prefix from entry.File.
func WithConfig(cfg *Config) DexOption {
	return func(d *Dex) error {
		if cfg == nil {
			return nil
		}
		for _, entry := range cfg.Indexes {
			if IsCoreIndex(entry.File) {
				continue
			}
			if entry.Tags == "" {
				continue
			}
			// Strip the "dex/" prefix to get the short name for repo.WriteIndex.
			shortName := strings.TrimPrefix(entry.File, "dex/")
			idx, err := NewTagFilteredIndex(shortName, entry.Tags)
			if err != nil {
				return fmt.Errorf("dex: config index %q: %w", entry.File, err)
			}
			d.custom = append(d.custom, idx)
		}
		return nil
	}
}

// NewDexFromRepo loads available index artifacts ("nodes.tsv", "tags", "links",
// "backlinks", "changes.md") from the provided repository and returns a Dex
// populated with parsed indexes. Missing or empty index files are treated as
// empty datasets and do not cause an error. Additional DexOptions (e.g.
// WithConfig) can be supplied to configure optional behaviour such as
// tag-filtered custom indexes.
func NewDexFromRepo(ctx context.Context, repo Repository, opts ...DexOption) (*Dex, error) {
	d := &Dex{}

	var errs []error

	// nodes.tsv
	if data, err := repo.GetIndex(ctx, "nodes.tsv"); err != nil {
		if errors.Is(err, ErrNotExist) {
			d.nodes = NodeIndex{}
		} else {
			errs = append(errs, fmt.Errorf("unable to read `nodes.tsv` index: %w", err))
		}
	} else {
		ni, err := ParseNodeIndex(ctx, data)
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to parse `nodes.tsv` index: %w", err))
			d.nodes = NodeIndex{}
		} else {
			d.nodes = ni
		}
	}

	// tags
	if data, err := repo.GetIndex(ctx, "tags"); err != nil {
		if errors.Is(err, ErrNotExist) {
			d.tags = TagIndex{}
		} else {
			errs = append(errs, fmt.Errorf("unable to read `tags` index: %w", err))
		}
	} else {
		ti, err := ParseTagIndex(ctx, data)
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to parse `tags` index: %w", err))
			d.tags = TagIndex{}
		} else {
			d.tags = ti
		}
	}

	// links
	if data, err := repo.GetIndex(ctx, "links"); err != nil {
		if errors.Is(err, ErrNotExist) {
			d.links = LinkIndex{}
		} else {
			errs = append(errs, fmt.Errorf("unable to read `links` index: %w", err))
		}
	} else {
		li, err := ParseLinkIndex(ctx, data)
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to parse `links` index: %w", err))
			d.links = LinkIndex{}
		} else {
			d.links = li
		}
	}

	// backlinks
	if data, err := repo.GetIndex(ctx, "backlinks"); err != nil {
		if errors.Is(err, ErrNotExist) {
			d.backlinks = BacklinkIndex{}
		} else {
			errs = append(errs, fmt.Errorf("unable to read `backlinks` index: %w", err))
		}
	} else {
		bi, err := ParseBacklinksIndex(ctx, data)
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to parse `backlinks` index: %w", err))
			d.backlinks = BacklinkIndex{}
		} else {
			if bi != nil {
				d.backlinks = *bi
			} else {
				d.backlinks = BacklinkIndex{}
			}
		}
	}

	// changes.md
	if data, err := repo.GetIndex(ctx, "changes.md"); err != nil {
		if errors.Is(err, ErrNotExist) {
			d.changes = ChangesIndex{}
		} else {
			errs = append(errs, fmt.Errorf("unable to read `changes.md` index: %w", err))
		}
	} else {
		ci, err := ParseChangesIndex(ctx, data)
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to parse `changes.md` index: %w", err))
			d.changes = ChangesIndex{}
		} else {
			d.changes = ci
		}
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
	list, ok := dex.links.data[node.Path()]
	return list, ok
}

// Backlinks returns the parsed backlinks index (map[dst] -> []src).
// NOTE: not intended to be mutated
func (dex *Dex) Backlinks(ctx context.Context, node NodeId) ([]NodeId, bool) {
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
func (dex *Dex) Write(ctx context.Context, repo Repository) error {
	dex.mu.Lock()
	defer dex.mu.Unlock()

	var errs []error
	var wg sync.WaitGroup

	wg.Go(func() {
		nodesData, err := dex.nodes.Data(ctx)
		name := "nodes.tsv"
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to create `%s` index: %w", name, err))
		}
		if e := repo.WriteIndex(ctx, name, nodesData); e != nil {
			errs = append(errs, fmt.Errorf("unable to write `%s` index: %w", name, err))
		}
	})

	wg.Go(func() {
		data, err := dex.tags.Data(ctx)
		name := "tags"
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to create `%s` index: %w", name, err))
		}
		if err := repo.WriteIndex(ctx, name, data); err != nil {
			errs = append(errs, fmt.Errorf("unable to write `%s` index: %w", name, err))
		}
	})

	wg.Go(func() {
		data, err := dex.links.Data(ctx)
		name := "links"
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to create `%s` index: %w", name, err))
		}
		if err := repo.WriteIndex(ctx, name, data); err != nil {
			errs = append(errs, fmt.Errorf("unable to write `%s` index: %w", name, err))
		}
	})

	wg.Go(func() {
		data, err := dex.backlinks.Data(ctx)
		name := "backlinks"
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to create `%s` index: %w", name, err))
		}
		if err := repo.WriteIndex(ctx, name, data); err != nil {
			errs = append(errs, fmt.Errorf("unable to write `%s` index: %w", name, err))
		}
	})

	wg.Go(func() {
		data, err := dex.changes.Data(ctx)
		name := "changes.md"
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to create `%s` index: %w", name, err))
		}
		if err := repo.WriteIndex(ctx, name, data); err != nil {
			errs = append(errs, fmt.Errorf("unable to write `%s` index: %w", name, err))
		}
	})

	for _, c := range dex.custom {
		c := c // capture for goroutine
		wg.Go(func() {
			data, err := c.Data(ctx)
			name := c.Name()
			if err != nil {
				errs = append(errs, fmt.Errorf("unable to create `%s` index: %w", name, err))
			}
			if err := repo.WriteIndex(ctx, name, data); err != nil {
				errs = append(errs, fmt.Errorf("unable to write `%s` index: %w", name, err))
			}
		})
	}

	wg.Wait()

	if len(errs) == 0 {
		return nil
	}

	return fmt.Errorf("unable to write dex: %w", errors.Join(errs...))
}

func (dex *Dex) GetRef(ctx context.Context, id NodeId) *NodeIndexEntry {
	if dex == nil {
		return nil
	}
	return dex.nodes.Get(ctx, id)
}
