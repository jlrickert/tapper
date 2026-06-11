package tapper

import (
	"context"
	"fmt"

	"github.com/jlrickert/tapper/pkg/keg"
)

type WatchNodeOptions struct {
	NodeID string
	KegTargetOptions
}

func (t *Tap) WatchNode(ctx context.Context, opts WatchNodeOptions) (<-chan keg.NodeEvent, func() error, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to open keg: %w", err)
	}
	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return nil, nil, err
	}
	w, err := repositoryEvents(k.Repo)
	if err != nil {
		return nil, nil, err
	}
	ch, err := w.Watch(ctx, id)
	if err != nil {
		_ = w.Close()
		return nil, nil, err
	}
	return ch, w.Close, nil
}
