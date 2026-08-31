package tapper

import (
	"context"
	"errors"
	"fmt"

	"github.com/jlrickert/tapper/pkg/keg"
)

type MoveOptions struct {
	KegTargetOptions

	SourceID     string
	DestID       string
	ExpectedHash string
}

// NodeHash performs the read half of an explicit CLI read-before-write flow.
// Mutation methods never call it implicitly.
func (t *Tap) NodeHash(ctx context.Context, opts KegTargetOptions, rawID string) (string, error) {
	k, err := t.resolveKegForRole(ctx, opts, FlightRoleViewer)
	if err != nil {
		return "", err
	}
	k, id, err := t.resolveNodeArg(ctx, k, rawID)
	if err != nil {
		return "", err
	}
	view, err := k.ReadNode(ctx, id)
	if err != nil {
		return "", err
	}
	return view.Hash(), nil
}

func (t *Tap) Move(ctx context.Context, opts MoveOptions) error {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}

	// Move is an intra-keg rename: k.Move relocates a node from one id to
	// another within a single keg. A cross-keg redirect has no meaning here
	// (source and destination must share a keg), so both ids stay scoped to the
	// resolved current keg via parseNodeID.
	srcID, err := parseNodeID(opts.SourceID)
	if err != nil {
		return err
	}

	dstID, err := parseNodeID(opts.DestID)
	if err != nil {
		return err
	}

	if _, err := k.Move(ctx, keg.NodeMoveOptions{Source: srcID, Destination: dstID, ExpectedHash: opts.ExpectedHash}); err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			return fmt.Errorf("node %s not found in %s", srcID.Path(), describeKeg(k))
		}
		if errors.Is(err, keg.ErrDestinationExists) {
			return fmt.Errorf("destination node %s already exists", dstID.Path())
		}
		return fmt.Errorf("unable to move node: %w", err)
	}

	return nil
}
