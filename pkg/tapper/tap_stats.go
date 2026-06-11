package tapper

import (
	"context"
	"fmt"
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

	k, node, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return "", err
	}

	exists, err := t.nodeExistsWithContent(ctx, k, node)
	if err != nil {
		return "", fmt.Errorf("unable to inspect node: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("node %s not found in %s", node.Path(), describeKeg(k))
	}

	stats, err := k.GetStats(ctx, node)
	if err != nil {
		return "", fmt.Errorf("unable to read node stats: %w", err)
	}

	return formatStatsOnlyYAML(ctx, stats), nil
}
