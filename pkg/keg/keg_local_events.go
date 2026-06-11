package keg

import (
	"context"
)

// Watch streams node change events until ctx is canceled. With no ids, all
// nodes are watched. Returns ErrNotSupported when the backend cannot emit
// events.
func (k *LocalKeg) Watch(ctx context.Context, ids ...NodeId) (<-chan NodeEvent, error) {
	events, ok := k.Repo.(RepositoryEvents)
	if !ok {
		return nil, ErrNotSupported
	}
	return events.Watch(ctx, ids...)
}
