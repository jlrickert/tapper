package tapper

import (
	"context"
	"errors"
	"fmt"

	"github.com/jlrickert/tapper/pkg/keg"
)

type RemoveOptions struct {
	KegTargetOptions

	NodeIDs []string
}

func (t *Tap) Remove(ctx context.Context, opts RemoveOptions) error {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}

	if len(opts.NodeIDs) == 0 {
		return fmt.Errorf("at least one node ID is required")
	}

	for _, nodeID := range opts.NodeIDs {
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
