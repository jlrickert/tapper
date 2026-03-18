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

	srcID, err := parseNodeID(opts.SourceID)
	if err != nil {
		return err
	}

	dstID, err := parseNodeID(opts.DestID)
	if err != nil {
		return err
	}

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
