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

	ExpectedHash   string
	ExpectedHashes map[string]string
}

func (t *Tap) Remove(ctx context.Context, opts RemoveOptions) error {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}

	nodes := make([]keg.NodeRemoveOptions, 0, len(opts.NodeIDs))
	for _, nodeID := range opts.NodeIDs {
		// Intentionally NOT routed through resolveNodeArg. Query-derived ids come
		// from the current keg's dex and are bare; mixing them with cross-keg
		// refs that redirect a destructive Remove to another keg would be a
		// surprising, hard-to-audit deletion. Remove stays scoped to the keg the
		// caller selected with --keg.
		id, err := parseNodeID(nodeID)
		if err != nil {
			return err
		}

		expectedHash := opts.ExpectedHash
		if hash := opts.ExpectedHashes[nodeID]; hash != "" {
			expectedHash = hash
		}
		nodes = append(nodes, keg.NodeRemoveOptions{ID: id, ExpectedHash: expectedHash})
	}
	result, err := k.RemoveNodes(ctx, keg.RemoveNodesOptions{Nodes: nodes, Query: strings.TrimSpace(opts.Query)})
	if errors.Is(err, keg.ErrNotExist) {
		return fmt.Errorf("node not found in %s: %w", describeKeg(k), err)
	}
	if err != nil {
		return fmt.Errorf("unable to remove nodes: %w", err)
	}
	if result.Failure != nil {
		failureErr := result.Failure.Err()
		if errors.Is(failureErr, keg.ErrNotExist) {
			return fmt.Errorf("node not found in %s: %w", describeKeg(k), failureErr)
		}
		return fmt.Errorf("unable to remove node %s: %w", result.Failure.NodeID.Path(), failureErr)
	}
	return nil
}
