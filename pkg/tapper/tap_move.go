package tapper

import (
	"context"
	"errors"
	"fmt"

	"github.com/jlrickert/tapper/pkg/keg"
)

type MoveOptions struct {
	KegTargetOptions

	SourceID string
	DestID   string
}

func (t *Tap) Move(ctx context.Context, opts MoveOptions) error {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}

	src, err := keg.ParseNode(opts.SourceID)
	if err != nil {
		return fmt.Errorf("invalid source node ID %q: %w", opts.SourceID, err)
	}
	if src == nil {
		return fmt.Errorf("invalid source node ID %q: %w", opts.SourceID, keg.ErrInvalid)
	}

	dst, err := keg.ParseNode(opts.DestID)
	if err != nil {
		return fmt.Errorf("invalid destination node ID %q: %w", opts.DestID, err)
	}
	if dst == nil {
		return fmt.Errorf("invalid destination node ID %q: %w", opts.DestID, keg.ErrInvalid)
	}

	srcID := keg.NodeId{ID: src.ID, Code: src.Code}
	dstID := keg.NodeId{ID: dst.ID, Code: dst.Code}
	if err := k.Move(ctx, srcID, dstID); err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			return fmt.Errorf("node %s not found", srcID.Path())
		}
		if errors.Is(err, keg.ErrDestinationExists) {
			return fmt.Errorf("destination node %s already exists", dstID.Path())
		}
		return fmt.Errorf("unable to move node: %w", err)
	}

	return nil
}
