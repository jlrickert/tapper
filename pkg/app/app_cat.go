package app

import (
	"context"
	"fmt"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tap"
)

// CatOptions configures behavior for Runner.Cat.
type CatOptions struct {
	// NodeID is the node identifier to read (e.g., "0", "42")
	NodeID string

	// Alias of the keg to read from
	Alias string
}

// Cat reads and displays a node's content with its metadata as frontmatter.
//
// The metadata (meta.yaml) is output as YAML frontmatter above the node's
// primary content (README.md).
func (r *Runner) Cat(ctx context.Context, opts CatOptions) (string, error) {
	proj, err := r.GetTapContext(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to read node: %w", err)
	}

	target, err := proj.ResolveKeg(ctx, &tap.ResolveKegOpts{Alias: opts.Alias})
	if err != nil {
		return "", fmt.Errorf("unable to determine keg: %w", err)
	}

	if target == nil {
		return "", fmt.Errorf("no keg configured: %w", keg.ErrInvalid)
	}

	k, err := keg.NewKegFromTarget(ctx, *target)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}

	// Parse the node ID
	node, err := keg.ParseNode(opts.NodeID)
	if err != nil {
		return "", fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
	}
	if node == nil {
		return "", fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
	}

	// Read metadata
	meta, err := k.Repo.ReadMeta(ctx, *node)
	if err != nil {
		return "", fmt.Errorf("unable to read node metadata: %w", err)
	}

	// Read content
	content, err := k.Repo.ReadContent(ctx, *node)
	if err != nil {
		return "", fmt.Errorf("unable to read node content: %w", err)
	}

	// Format as frontmatter + content
	output := fmt.Sprintf("---\n%s---\n%s", string(meta), string(content))
	return output, nil
}
