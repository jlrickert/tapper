package keg

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	tapper_errors "github.com/jlrickert/tapper/pkg/errors"
)

// Dex provides a high-level, in-memory view of the repository's generated
// dex indices: nodes, tags, links, and backlinks. It is a convenience wrapper
// used by index builders and other tooling to read or inspect index data without
// dealing directly with repository I/O. Dex does not perform any I/O itself;
// callers are responsible for providing a KegRepository when writing indices.
type Dex struct {
	// nodes is the list of nodes sorted by node id.
	nodes NodeIndex

	// tags maps a tag to a list of nodes that has a tag
	tags TagIndex

	// links maps a node to nodes that it links too
	links LinkIndex

	// backlinks maps a node to other nodes linking to it
	backlinks BacklinkIndex

	mu sync.RWMutex
}

// NewDexFromRepo loads available index artifacts ("nodes.tsv", "tags", "links",
// "backlinks") from the provided repository and returns a Dex populated with
// parsed indexes. Missing or empty index files are treated as empty datasets
// and do not cause an error.
func NewDexFromRepo(ctx context.Context, repo KegRepository) (*Dex, error) {
	d := &Dex{}

	// nodes.tsv
	if data, err := repo.GetIndex(ctx, "nodes.tsv"); err != nil {
		if errors.Is(err, tapper_errors.ErrNotFound) {
			d.nodes = NodeIndex{}
		} else {
			return nil, fmt.Errorf("unable to read `nodes.tsv` index: %w", err)
		}
	} else {
		ni, err := ParseNodeIndex(ctx, data)
		if err != nil {
			return nil, fmt.Errorf("unable to parse `nodes.tsv` index: %w", err)
		}
		d.nodes = ni
	}

	// tags
	if data, err := repo.GetIndex(ctx, "tags"); err != nil {
		if errors.Is(err, tapper_errors.ErrNotFound) {
			d.tags = TagIndex{}
		} else {
			return nil, fmt.Errorf("unable to read `tags` index: %w", err)
		}
	} else {
		ti, err := ParseTagIndex(ctx, data)
		if err != nil {
			return nil, fmt.Errorf("unable to parse `tags` index: %w", err)
		}
		d.tags = ti
	}

	// links
	if data, err := repo.GetIndex(ctx, "links"); err != nil {
		if errors.Is(err, tapper_errors.ErrNotFound) {
			d.links = LinkIndex{}
		} else {
			return nil, fmt.Errorf("unable to read `links` index: %w", err)
		}
	} else {
		li, err := ParseLinkIndex(ctx, data)
		if err != nil {
			return nil, fmt.Errorf("unable to parse `links` index: %w", err)
		}
		d.links = li
	}

	// backlinks
	if data, err := repo.GetIndex(ctx, "backlinks"); err != nil {
		if errors.Is(err, tapper_errors.ErrNotFound) {
			d.backlinks = BacklinkIndex{}
		} else {
			return nil, fmt.Errorf("unable to read `backlinks` index: %w", err)
		}
	} else {
		bi, err := ParseBacklinksIndex(ctx, data)
		if err != nil {
			return nil, fmt.Errorf("unable to parse `backlinks` index: %w", err)
		}
		if bi != nil {
			d.backlinks = *bi
		} else {
			d.backlinks = BacklinkIndex{}
		}
	}

	return d, nil
}

// Nodes returns a copy of the parsed nodes index (slice of NodeRef).
func (dex *Dex) Nodes(ctx context.Context) []NodeIndexEntry {
	dex.mu.RLock()
	defer dex.mu.RUnlock()
	return dex.nodes.List(ctx)
}

// Tags returns the parsed tags index (map[tag] -> []NodeID).
func (dex *Dex) TagLinks(ctx context.Context, node Node) ([]Node, bool) {
	dex.mu.RLock()
	defer dex.mu.RUnlock()
	list, ok := dex.tags.data[node.Path()]
	return list, ok
}

func (dex *Dex) TagList(ctx context.Context) []string {
	dex.mu.RLock()
	defer dex.mu.RUnlock()
	keys := maps.Keys(dex.tags.data)
	return slices.AppendSeq([]string{}, keys)
}

// Links returns the parsed outgoing links index (map[src] -> []dst).
func (dex *Dex) Links(ctx context.Context, node Node) ([]Node, bool) {
	list, ok := dex.links.data[node.Path()]
	return list, ok
}

// Backlinks returns the parsed backlinks index (map[dst] -> []src).
// NOTE: not intended to be mutated
func (dex *Dex) Backlinks(ctx context.Context, node Node) ([]Node, bool) {
	list, ok := dex.backlinks.data[node.Path()]
	return list, ok
}

// Clear resets all in-memory index data held by the Dex instance.
func (dex *Dex) Clear(ctx context.Context) error {
	dex.mu.Lock()
	dex.nodes = NodeIndex{}
	dex.tags = TagIndex{}
	dex.links = LinkIndex{}
	dex.backlinks = BacklinkIndex{}
	dex.mu.Unlock()
	return nil
}

// Add adds the provided node to all managed indexes. This implements the
// IndexBuilder contract for convenience when using Dex as an aggregated builder.
func (dex *Dex) Add(ctx context.Context, data NodeData) error {
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
	dex.mu.Unlock()
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// Remove removes the node identified by id from all managed indexes. This
// implements the IndexBuilder contract for convenience when using Dex.
func (dex *Dex) Remove(ctx context.Context, node Node) error {
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
	dex.mu.Unlock()
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func (dex *Dex) NextNode(ctx context.Context) Node {
	dex.mu.RLock()
	defer dex.mu.RUnlock()
	return dex.nodes.Next(ctx)
}

// Write serializes the in-memory indexes and writes them atomically to the
// provided repository using WriteIndex. If any write operation fails the error
// chain is returned (errors.Join is used to aggregate multiple errors).
func (dex *Dex) Write(ctx context.Context, repo KegRepository) error {
	dex.mu.Lock()
	defer dex.mu.Unlock()

	var errs []error
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		nodesData, err := dex.nodes.Data(ctx)
		name := "nodes.tsv"
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to create `%s` index: %w", name, err))
		}
		if e := repo.WriteIndex(ctx, name, nodesData); e != nil {
			errs = append(errs, fmt.Errorf("unable to write `%s` index: %w", name, err))
		}
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		data, err := dex.tags.Data(ctx)
		name := "tags"
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to create `%s` index: %w", name, err))
		}
		if err := repo.WriteIndex(ctx, name, data); err != nil {
			errs = append(errs, fmt.Errorf("unable to write `%s` index: %w", name, err))
		}
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		data, err := dex.links.Data(ctx)
		name := "links"
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to create `%s` index: %w", name, err))
		}
		if err := repo.WriteIndex(ctx, name, data); err != nil {
			errs = append(errs, fmt.Errorf("unable to write `%s` index: %w", name, err))
		}
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		data, err := dex.backlinks.Data(ctx)
		name := "backlinks"
		if err != nil {
			errs = append(errs, fmt.Errorf("unable to create `%s` index: %w", name, err))
		}
		if err := repo.WriteIndex(ctx, name, data); err != nil {
			errs = append(errs, fmt.Errorf("unable to write `%s` index: %w", name, err))
		}
		wg.Done()
	}()

	wg.Wait()

	if len(errs) == 0 {
		return nil
	}

	return fmt.Errorf("unable to write dex: %w", errors.Join(errs...))
}

func (dex *Dex) GetRef(ctx context.Context, id Node) *NodeIndexEntry {
	if dex == nil {
		return nil
	}
	return dex.nodes.Get(ctx, id)
}
