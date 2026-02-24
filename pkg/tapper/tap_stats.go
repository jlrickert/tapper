package tapper

import (
	"context"
	"errors"
	"fmt"

	"github.com/jlrickert/tapper/pkg/keg"
)

type StatsOptions struct {
	// NodeID is the node identifier to inspect (e.g., "0", "42")
	NodeID string

	KegTargetOptions
}

func (t *Tap) Stats(ctx context.Context, opts StatsOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}

	node, err := keg.ParseNode(opts.NodeID)
	if err != nil {
		return "", fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
	}
	if node == nil {
		return "", fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
	}

	exists, err := k.Repo.HasNode(ctx, *node)
	if err != nil {
		return "", fmt.Errorf("unable to inspect node: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("node %s not found", node.Path())
	}

	stats, err := k.Repo.ReadStats(ctx, *node)
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			stats = &keg.NodeStats{}
		} else {
			return "", fmt.Errorf("unable to read node stats: %w", err)
		}
	}

	return formatStatsOnlyYAML(ctx, stats), nil
}
