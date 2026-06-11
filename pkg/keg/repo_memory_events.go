package keg

import (
	"context"
)

// memoryWatch is the per-Watch subscriber handle for MemoryRepo live events.
// MemoryRepo has no external change source, so events arrive exclusively via
// Emit (access bumps from the repo itself, or test code simulating changes).
type memoryWatch struct {
	ch  chan NodeEvent
	ids map[NodeId]struct{} // empty means all nodes
}

// Watch implements RepositoryEvents for MemoryRepo. Events are delivered on
// the returned channel until ctx is cancelled, at which point the channel is
// closed.
func (r *MemoryRepo) Watch(ctx context.Context, ids ...NodeId) (<-chan NodeEvent, error) {
	idSet := make(map[NodeId]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}
	w := &memoryWatch{ch: make(chan NodeEvent, 16), ids: idSet}
	r.registerWatcher(w)

	// Cleanup: unregister first (Emit holds watchersMu, so no send can race
	// the close), then close the channel.
	go func() {
		<-ctx.Done()
		r.unregisterWatcher(w)
		close(w.ch)
	}()

	return w.ch, nil
}

// Emit implements RepositoryEvents. It broadcasts a programmatic event to
// all active Watch subscribers whose filters match. Test code uses this to
// simulate repository changes.
func (r *MemoryRepo) Emit(ev NodeEvent) {
	r.emitToWatchers(ev)
}

// emit delivers an event to this subscriber. Called by
// MemoryRepo.emitToWatchers under watchersMu; sends are non-blocking so a
// slow consumer cannot deadlock the emitter.
func (w *memoryWatch) emit(ev NodeEvent) {
	_, match := w.ids[ev.NodeID]
	if len(w.ids) == 0 || match {
		select {
		case w.ch <- ev:
		default:
			// Drop event if subscriber is slow.
		}
	}
}

var _ RepositoryEvents = (*MemoryRepo)(nil)
