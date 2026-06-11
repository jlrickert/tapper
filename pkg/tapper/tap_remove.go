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
		matchedEntries, evalErr := k.Query(ctx, keg.QueryOptions{Expr: q})
		if evalErr != nil {
			return fmt.Errorf("invalid query expression: %w", evalErr)
		}
		seen := make(map[string]struct{})
		for _, entry := range matchedEntries {
			n, parseErr := keg.ParseNode(entry.ID)
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
		// Intentionally NOT routed through resolveNodeArg. Query-derived ids come
		// from the current keg's dex and are bare; mixing them with cross-keg
		// refs that redirect a destructive Remove to another keg would be a
		// surprising, hard-to-audit deletion. Remove stays scoped to the keg the
		// caller selected with --keg.
		id, err := parseNodeID(nodeID)
		if err != nil {
			return err
		}

		if _, err := k.Remove(ctx, id); err != nil {
			if errors.Is(err, keg.ErrNotExist) {
				return fmt.Errorf("node %s not found in %s", id.Path(), describeKeg(k))
			}
			return fmt.Errorf("unable to remove node %s: %w", id.Path(), err)
		}
	}

	return nil
}
