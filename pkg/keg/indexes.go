package keg

import "context"

// IndexBuilder is an interface for constructing a single index artifact
// (for example: nodes.tsv, tags, links, backlinks). Implementations maintain
// in-memory state via Add / Remove / Clear and produce the serialized bytes to
// write via Data.
type IndexBuilder interface {
	// Name returns the canonical index filename (for example "dex/tags").
	Name() string

	// Add incorporates information from a node into the index's in-memory state.
	Add(ctx context.Context, node NodeData) error

	// Remove deletes node-related state from the index.
	Remove(ctx context.Context, node Node) error

	// Clear resets the index to an empty state.
	Clear(ctx context.Context) error

	// Data returns the serialized index bytes to be written to storage.
	Data(ctx context.Context) ([]byte, error)
}
