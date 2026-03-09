package keg

import (
	"context"
	"sync"
)

// MemoryRepoWatcher implements RepositoryEvents for MemoryRepo, enabling
// event-driven testing without filesystem dependencies. The Emit method
// allows test code to simulate repository changes.
type MemoryRepoWatcher struct {
	repo *MemoryRepo

	mu     sync.Mutex
	closed bool
	subs   []*memorySub
}

type memorySub struct {
	ch     chan NodeEvent
	ids    map[NodeId]struct{} // empty means all nodes
	cancel context.CancelFunc
}

// WatchEvents returns a RepositoryEvents implementation for the MemoryRepo.
func (r *MemoryRepo) WatchEvents() *MemoryRepoWatcher {
	return &MemoryRepoWatcher{repo: r}
}

// Emit sends a NodeEvent to all active subscribers whose filters match.
// This is intended to be called from test code to simulate repository changes.
func (w *MemoryRepoWatcher) Emit(ev NodeEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	for _, s := range w.subs {
		_, match := s.ids[ev.NodeID]
		if len(s.ids) == 0 || match {
			select {
			case s.ch <- ev:
			default:
				// Drop event if subscriber is slow.
			}
		}
	}
}

// Watch begins observing changes for the specified node IDs (or all nodes
// when no IDs are given).
func (w *MemoryRepoWatcher) Watch(ctx context.Context, ids ...NodeId) (<-chan NodeEvent, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil, ErrNotSupported
	}

	watchCtx, cancel := context.WithCancel(ctx)
	ch := make(chan NodeEvent, 16)

	idSet := make(map[NodeId]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}

	sub := &memorySub{ch: ch, ids: idSet, cancel: cancel}
	w.subs = append(w.subs, sub)

	// Clean up when context is done.
	go func() {
		<-watchCtx.Done()
		w.removeSub(sub)
		close(ch)
	}()

	return ch, nil
}

// Close releases all watcher resources and closes subscriber channels.
func (w *MemoryRepoWatcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	for _, s := range w.subs {
		s.cancel()
	}
	w.subs = nil
	return nil
}

func (w *MemoryRepoWatcher) removeSub(sub *memorySub) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i, s := range w.subs {
		if s == sub {
			w.subs = append(w.subs[:i], w.subs[i+1:]...)
			return
		}
	}
}

var _ RepositoryEvents = (*MemoryRepoWatcher)(nil)
