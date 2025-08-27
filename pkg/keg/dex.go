package keg

import (
	"context"
	"errors"
	"fmt"
)

// Dex provides a high-level, in-memory view of the repository's generated
// dex indices: nodes, tags, links, and backlinks. It is a convenience wrapper
// used by index builders and other tooling to read or inspect index data without
// dealing directly with repository I/O. Dex does not perform any I/O itself;
// callers are responsible for providing a KegRepository when writing indices.
type Dex struct {
	nodes     *NodesIndex
	tags      *TagsIndex
	links     *LinksIndex
	backlinks *BacklinksIndex
}

// NewDexFromRepo loads available index artifacts ("nodes.tsv", "tags", "links",
// "backlinks") from the provided repository and returns a Dex populated with
// parsed indexes. Missing or empty index files are treated as empty datasets
// and do not cause an error.
func NewDexFromRepo(ctx context.Context, repo KegRepository) (*Dex, error) {
	nodesIndex, err := NewNodesIndexFromRepo(ctx, repo)
	if err != nil {
		return nil, nil
	}
	tagsIndex, err := NewTagsIndexFromRepo(ctx, repo)
	if err != nil {
		return nil, nil
	}
	linksIndex, err := NewLinksIndexFromRepo(ctx, repo)
	if err != nil {
		return nil, nil
	}
	backlinksIndex, err := NewBacklinksIndexFromRepo(ctx, repo)
	if err != nil {
		return nil, nil
	}
	d := &Dex{
		nodes:     nodesIndex,
		tags:      tagsIndex,
		links:     linksIndex,
		backlinks: backlinksIndex,
	}
	return d, nil
}

// Nodes returns the parsed nodes index (slice of NodeRef).
func (dex *Dex) Nodes() []NodeRef {
	return dex.nodes.Nodes
}

// Tags returns the parsed tags index (map[tag] -> []NodeID).
func (dex *Dex) Tags() map[string][]NodeID {
	return dex.tags.Tags
}

// Links returns the parsed outgoing links index (map[src] -> []dst).
func (dex *Dex) Links() map[NodeID][]NodeID {
	return dex.links.Links
}

// Backlinks returns the parsed backlinks index (map[dst] -> []src).
func (dex *Dex) Backlinks() map[NodeID][]NodeID {
	return dex.backlinks.Backlinks
}

// Clear resets all in-memory index data held by the Dex instance.
func (dex *Dex) Clear(ctx context.Context) error {
	dex.nodes.Clear(ctx)
	dex.tags.Clear(ctx)
	dex.links.Clear(ctx)
	dex.backlinks.Clear(ctx)
	return nil
}

// Add adds the provided node to all managed indexes. This implements the
// IndexBuilder contract for convenience when using Dex as an aggregated builder.
func (dex *Dex) Add(ctx context.Context, node Node) error {
	dex.nodes.Add(ctx, node)
	dex.tags.Add(ctx, node)
	dex.links.Add(ctx, node)
	dex.backlinks.Add(ctx, node)
	return nil
}

// Remove removes the node identified by id from all managed indexes. This
// implements the IndexBuilder contract for convenience when using Dex.
func (dex *Dex) Remove(ctx context.Context, node NodeID) error {
	dex.nodes.Remove(ctx, node)
	dex.tags.Remove(ctx, node)
	dex.links.Remove(ctx, node)
	dex.backlinks.Remove(ctx, node)
	return nil
}

// Rebuild clears current in-memory indexes and repopulates them from the
// provided Keg instance. This is a convenience that combines Clear + Update.
func (dex *Dex) Rebuild(ctx context.Context, keg *Keg) error {
	err := dex.Clear(ctx)
	if err != nil {
		return err
	}
	return dex.Update(ctx, keg)
}

// Update scans the Keg repository and updates the in-memory indexes. It queries
// the repository's Next() to determine the id space to attempt. For each id it
// loads the node and either adds it to the indexes or removes any existing
// state if the node is not found.
func (dex *Dex) Update(ctx context.Context, keg *Keg) error {
	nextId, err := keg.Repo.Next(ctx)
	if err != nil {
		return fmt.Errorf("unable to update dex: %w", err)
	}

	for i := range nextId - 1 {
		node, err := keg.GetNode(ctx, i)
		if IsNotFound(err) {
			dex.Remove(ctx, i)
			continue
		} else if err != nil {
			return fmt.Errorf("unable to update dex: %w", err)
		}

		dex.Add(ctx, node)
	}

	return nil
}

// Write serializes the in-memory indexes and writes them atomically to the
// provided repository using WriteIndex. If any write operation fails the error
// chain is returned (errors.Join is used to aggregate multiple errors).
func (dex *Dex) Write(ctx context.Context, repo KegRepository) error {
	nodesData, err := dex.nodes.Data(ctx)
	if err != nil {
		return fmt.Errorf("unable to write dex: %w", err)
	}

	tagsData, err := dex.tags.Data(ctx)
	if err != nil {
		return fmt.Errorf("unable to write dex: %w", err)
	}

	linksData, err := dex.links.Data(ctx)
	if err != nil {
		return fmt.Errorf("unable to write dex: %w", err)
	}

	backlinksData, err := dex.backlinks.Data(ctx)
	if err != nil {
		return fmt.Errorf("unable to write dex: %w", err)
	}

	err = nil
	var errs []error
	if e := repo.WriteIndex(ctx, dex.nodes.Name(), nodesData); e != nil {
		errs = append(errs, e)
	}
	if e := repo.WriteIndex(ctx, dex.tags.Name(), tagsData); e != nil {
		errs = append(errs, e)
	}
	if e := repo.WriteIndex(ctx, dex.links.Name(), linksData); e != nil {
		errs = append(errs, e)
	}
	if e := repo.WriteIndex(ctx, dex.backlinks.Name(), backlinksData); e != nil {
		errs = append(errs, e)
	}
	err = errors.Join(errs...)
	if err != nil {
		return fmt.Errorf("unable to write dex: %w", err)
	}

	return nil
}

func (dex *Dex) GetNode(id NodeID) *NodeRef {
	if dex == nil || dex.nodes == nil {
		return nil
	}
	for i := range dex.nodes.Nodes {
		if dex.nodes.Nodes[i].ID == id {
			return &dex.nodes.Nodes[i]
		}
	}
	return nil
}

// NextID returns the NextID value from the nodes index. This represents the
// next available numeric node id known to the in-memory nodes index.
func (dex *Dex) NextID() NodeID {
	return dex.nodes.NextID
}
