package tapper

import (
	"context"
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

type IndexOptions struct {
	KegTargetOptions

	// Rebuild rebuilds the full index
	Rebuild bool

	// NoUpdate skips updating node meta information
	NoUpdate bool
}

type IndexCatOptions struct {
	KegTargetOptions

	// Name is the index file name to dump, e.g. "changes.md" or "nodes.tsv".
	// A leading "dex/" prefix is stripped automatically.
	Name string
}

// ListIndexes returns the names of available index files for a keg (e.g. "changes.md", "nodes.tsv").
func (t *Tap) ListIndexes(ctx context.Context, opts IndexCatOptions) ([]string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return nil, fmt.Errorf("unable to determine keg: %w", err)
	}
	return k.Repo.ListIndexes(ctx)
}

// IndexCat returns the raw contents of a named dex index file.
// opts.Name may include or omit a leading "dex/" prefix; both are accepted.
func (t *Tap) IndexCat(ctx context.Context, opts IndexCatOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to determine keg: %w", err)
	}

	name := strings.TrimPrefix(opts.Name, "dex/")
	data, err := k.Repo.GetIndex(ctx, name)
	if err != nil {
		return "", fmt.Errorf("index %q not found: %w", opts.Name, err)
	}
	return string(data), nil
}

// Index updates indices for a keg (nodes.tsv, tags, links, backlinks).
// Default behavior is incremental. Set opts.Rebuild to force a full rebuild.
func (t *Tap) Index(ctx context.Context, opts IndexOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to determine keg: %w", err)
	}

	err = k.Index(ctx, keg.IndexOptions{
		Rebuild:  opts.Rebuild,
		NoUpdate: opts.NoUpdate,
	})
	if err != nil {
		return "", fmt.Errorf("unable to rebuild indices: %w", err)
	}

	output := fmt.Sprintf("Indices rebuilt for %s\n", k.Target.Path())
	return output, nil
}
