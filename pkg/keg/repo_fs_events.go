package keg

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// fsRepoWatcher implements RepositoryEvents for FsRepo using fsnotify.
type fsRepoWatcher struct {
	repo         *FsRepo
	watcher      *fsnotify.Watcher
	resolvedRoot string // real filesystem path for fsnotify and classify

	mu      sync.Mutex
	closed  bool
	cancels []context.CancelFunc
	subs    []*fsSub // active subscribers for Emit broadcasts
	wg      sync.WaitGroup
}

// fsSub tracks a single Watch subscriber for Emit delivery.
type fsSub struct {
	ch   chan NodeEvent
	ids  map[NodeId]struct{} // empty means all nodes
	done chan struct{}        // closed when loop exits
}

// WatchEvents returns a RepositoryEvents implementation for the FsRepo.
// Each call creates a fresh watcher; callers must call Close when done.
func (f *FsRepo) WatchEvents() (RepositoryEvents, error) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	// Resolve the root path through the runtime so that jailed/sandboxed
	// paths are expanded to real filesystem paths for fsnotify.
	resolved := f.Root
	if f.runtime != nil {
		if r, resolveErr := f.runtime.ResolvePath(f.Root, false); resolveErr == nil {
			resolved = r
		}
		// Apply jail prefix for sandboxed environments.
		if jail := strings.TrimSpace(f.runtime.GetJail()); jail != "" {
			trimmed := strings.TrimPrefix(resolved, string(filepath.Separator))
			resolved = filepath.Join(jail, trimmed)
		}
	}
	fw := &fsRepoWatcher{repo: f, watcher: w, resolvedRoot: resolved}
	f.registerWatcher(fw)
	return fw, nil
}

// Watch begins observing changes for the specified node IDs. When no IDs are
// given, the entire keg root is watched and events for any node are emitted.
// Events are delivered on the returned channel until ctx is cancelled or
// Close is called.
func (fw *fsRepoWatcher) Watch(ctx context.Context, ids ...NodeId) (<-chan NodeEvent, error) {
	fw.mu.Lock()
	if fw.closed {
		fw.mu.Unlock()
		return nil, ErrNotSupported
	}
	fw.mu.Unlock()

	// Determine which directories to watch using resolved paths.
	var dirs []string
	if len(ids) == 0 {
		dirs = append(dirs, fw.resolvedRoot)
	} else {
		for _, id := range ids {
			dirs = append(dirs, filepath.Join(fw.resolvedRoot, id.Path()))
		}
	}

	for _, d := range dirs {
		if err := fw.watcher.Add(d); err != nil {
			return nil, err
		}
	}

	watchCtx, cancel := context.WithCancel(ctx)

	ch := make(chan NodeEvent, 16)
	idSet := make(map[NodeId]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}
	sub := &fsSub{ch: ch, ids: idSet, done: make(chan struct{})}

	fw.mu.Lock()
	fw.cancels = append(fw.cancels, cancel)
	fw.subs = append(fw.subs, sub)
	fw.mu.Unlock()

	// Cleanup goroutine: wait for context cancellation, remove the subscriber
	// (preventing further Emit sends), wait for loop to exit, then close the
	// channel. This ordering ensures no sends race with the close.
	fw.wg.Add(1)
	go func() {
		defer fw.wg.Done()
		<-watchCtx.Done()
		fw.removeSub(sub)
		<-sub.done
		close(ch)
	}()

	go fw.loop(watchCtx, ch, sub, len(ids) == 0)
	return ch, nil
}

// Emit sends a NodeEvent to all active subscribers whose filters match.
// Lock ordering: FsRepo.watchersMu -> fsRepoWatcher.mu. Sends are
// non-blocking to avoid deadlock when the channel buffer is full.
func (fw *fsRepoWatcher) Emit(ev NodeEvent) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	if fw.closed {
		return
	}
	for _, s := range fw.subs {
		_, match := s.ids[ev.NodeID]
		if len(s.ids) == 0 || match {
			select {
			case s.ch <- ev:
			default:
			}
		}
	}
}

func (fw *fsRepoWatcher) removeSub(sub *fsSub) {
	fw.mu.Lock()
	defer fw.mu.Unlock()
	for i, s := range fw.subs {
		if s == sub {
			fw.subs = append(fw.subs[:i], fw.subs[i+1:]...)
			return
		}
	}
}

// Close releases all watcher resources and cancels active Watch goroutines.
// Blocks until all loop and cleanup goroutines have exited.
func (fw *fsRepoWatcher) Close() error {
	fw.mu.Lock()
	if fw.closed {
		fw.mu.Unlock()
		return nil
	}
	fw.closed = true
	for _, cancel := range fw.cancels {
		cancel()
	}
	fw.cancels = nil
	fw.subs = nil
	err := fw.watcher.Close()
	fw.mu.Unlock()

	// Unregister after releasing fw.mu to preserve lock ordering
	// (FsRepo.watchersMu -> fsRepoWatcher.mu). The closed flag is already
	// set, so Emit calls will no-op even before unregistration completes.
	fw.repo.unregisterWatcher(fw)

	// Wait for all loop and cleanup goroutines to exit before returning.
	fw.wg.Wait()
	return err
}

// loop reads fsnotify events, debounces them, and emits NodeEvents.
// Signals completion by closing sub.done when it returns.
func (fw *fsRepoWatcher) loop(ctx context.Context, ch chan<- NodeEvent, sub *fsSub, watchRoot bool) {
	defer close(sub.done)

	// pending tracks debounce state per file path.
	type pendingEvent struct {
		event NodeEvent
		first time.Time
	}
	pending := make(map[string]*pendingEvent)

	const debounce = 150 * time.Millisecond
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case fsEvent, ok := <-fw.watcher.Events:
			if !ok {
				return
			}
			ev, valid := fw.classify(fsEvent, watchRoot)
			if !valid {
				continue
			}
			pending[fsEvent.Name] = &pendingEvent{event: ev, first: time.Now()}

		case _, ok := <-fw.watcher.Errors:
			if !ok {
				return
			}
			// Swallow watcher errors; they don't map to node events.

		case <-ticker.C:
			now := time.Now()
			for path, p := range pending {
				if now.Sub(p.first) >= debounce {
					select {
					case ch <- p.event:
					case <-ctx.Done():
						return
					}
					delete(pending, path)
				}
			}
		}
	}
}

// classify maps an fsnotify.Event to a NodeEvent, returning false when the
// event does not correspond to a recognized node file.
func (fw *fsRepoWatcher) classify(ev fsnotify.Event, watchRoot bool) (NodeEvent, bool) {
	// Determine which file changed and derive node ID + field.
	abs := ev.Name
	rel, err := filepath.Rel(fw.resolvedRoot, abs)
	if err != nil {
		return NodeEvent{}, false
	}

	parts := strings.SplitN(filepath.ToSlash(rel), "/", 3)
	if len(parts) < 1 {
		return NodeEvent{}, false
	}

	// Parse the first path component as a node ID.
	nodeDir := parts[0]
	nodeNum, parseErr := strconv.Atoi(nodeDir)
	if parseErr != nil {
		return NodeEvent{}, false
	}
	id := NodeId{ID: nodeNum}

	// Determine the field from the filename.
	var field string
	if len(parts) >= 2 {
		base := parts[len(parts)-1]
		switch base {
		case fw.repo.ContentFilename:
			field = "content"
		case fw.repo.MetaFilename:
			field = "meta"
		case fw.repo.StatsFilename:
			field = "stats"
		default:
			// Ignore changes to other files (images, assets, lock files).
			return NodeEvent{}, false
		}
	}

	// Map fsnotify op to NodeEventKind.
	var kind NodeEventKind
	switch {
	case ev.Op&fsnotify.Create != 0:
		kind = NodeEventCreated
	case ev.Op&fsnotify.Remove != 0:
		kind = NodeEventDeleted
	case ev.Op&(fsnotify.Write|fsnotify.Rename|fsnotify.Chmod) != 0:
		kind = NodeEventModified
	default:
		return NodeEvent{}, false
	}

	return NodeEvent{Kind: kind, NodeID: id, Field: field}, true
}

var _ RepositoryEvents = (*fsRepoWatcher)(nil)
