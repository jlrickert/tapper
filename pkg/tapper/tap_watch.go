package tapper

import (
	"context"
	"errors"
	"fmt"

	"github.com/jlrickert/tapper/pkg/keg"
)

type WatchNodeOptions struct {
	NodeID string
	KegTargetOptions
}

// WatchNode subscribes to live change events for a single node. The watch is
// scoped to ctx: events are delivered on the returned channel until ctx is
// cancelled, at which point the channel is closed.
func (t *Tap) WatchNode(ctx context.Context, opts WatchNodeOptions) (<-chan keg.NodeEvent, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}
	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return nil, err
	}
	ch, err := k.Watch(ctx, id)
	if err != nil {
		if errors.Is(err, keg.ErrNotSupported) {
			return nil, fmt.Errorf("repository does not support live events")
		}
		return nil, err
	}
	return ch, nil
}
