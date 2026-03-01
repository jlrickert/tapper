package tapper

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

type RemoveOptions struct {
	KegTargetOptions

	// NodeIDs lists explicit node IDs to remove.
	NodeIDs []string

	// Query is an optional boolean expression (tags and/or key=value attr
	// predicates) that selects additional nodes to remove.
	Query string
}

func (t *Tap) Remove(ctx context.Context, opts RemoveOptions) error {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}

	nodeIDs := opts.NodeIDs

	if q := strings.TrimSpace(opts.Query); q != "" {
		dex, dexErr := k.Dex(ctx)
		if dexErr != nil {
			return fmt.Errorf("unable to read dex: %w", dexErr)
		}
		entries := dex.Nodes(ctx)
		matchedPaths, evalErr := evalQueryExpr(ctx, k, dex, entries, q)
		if evalErr != nil {
			return fmt.Errorf("invalid query expression: %w", evalErr)
		}
		seen := make(map[string]struct{})
		for path := range matchedPaths {
			n, parseErr := keg.ParseNode(path)
			if parseErr != nil || n == nil {
				continue
			}
			if _, dup := seen[n.Path()]; dup {
				continue
			}
			seen[n.Path()] = struct{}{}
			nodeIDs = append(nodeIDs, n.Path())
		}
	}

	if len(nodeIDs) == 0 {
		return fmt.Errorf("at least one node ID is required")
	}

	for _, nodeID := range nodeIDs {
		node, err := keg.ParseNode(nodeID)
		if err != nil {
			return fmt.Errorf("invalid node ID %q: %w", nodeID, err)
		}
		if node == nil {
			return fmt.Errorf("invalid node ID %q: %w", nodeID, keg.ErrInvalid)
		}

		id := keg.NodeId{ID: node.ID, Code: node.Code}
		if err := k.Remove(ctx, id); err != nil {
			if errors.Is(err, keg.ErrNotExist) {
				return fmt.Errorf("node %s not found", id.Path())
			}
			return fmt.Errorf("unable to remove node %s: %w", id.Path(), err)
		}
	}

	return nil
}
