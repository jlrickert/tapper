package tapper

import (
	"context"
	"fmt"

	"github.com/jlrickert/tapper/pkg/keg"
)

type IndexOptions struct {
	KegTargetOptions

	// Rebuild rebuilds the full index
	Rebuild bool

	// NoUpdate skips updating node meta information
	NoUpdate bool
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
