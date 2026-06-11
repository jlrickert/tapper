package keg

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// fsWatch is the per-Watch handle for FsRepo live events: one fsnotify
// watcher and one subscriber channel, both scoped to the Watch context.
type fsWatch struct {
	repo         *FsRepo
	watcher      *fsnotify.Watcher
	resolvedRoot string // real filesystem path for fsnotify and classify
	ch           chan NodeEvent
	ids          map[NodeId]struct{} // empty means all nodes
	done         chan struct{}       // closed when loop exits
}

// Watch implements RepositoryEvents for FsRepo using fsnotify. When no IDs
// are given, the entire keg root is watched and events for any node are
// emitted. Events are delivered on the returned channel until ctx is
// cancelled, at which point the channel is closed.
func (f *FsRepo) Watch(ctx context.Context, ids ...NodeId) (<-chan NodeEvent, error) {
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

	// Determine which directories to watch using resolved paths.
	var dirs []string
	if len(ids) == 0 {
		dirs = append(dirs, resolved)
	} else {
		for _, id := range ids {
			dirs = append(dirs, filepath.Join(resolved, id.Path()))
		}
	}
	for _, d := range dirs {
		if err := w.Add(d); err != nil {
			_ = w.Close()
			return nil, err
		}
	}

	idSet := make(map[NodeId]struct{}, len(ids))
	for _, id := range ids {
		idSet[id] = struct{}{}
	}
	fw := &fsWatch{
		repo:         f,
		watcher:      w,
		resolvedRoot: resolved,
		ch:           make(chan NodeEvent, 16),
		ids:          idSet,
		done:         make(chan struct{}),
	}
	f.registerWatcher(fw)

	go fw.loop(ctx, len(ids) == 0)

	// Cleanup: when ctx ends, unregister the subscriber first (Emit holds
	// watchersMu, so no programmatic send can race the close), close the
	// fsnotify watcher (unblocks the loop), wait for the loop to exit, then
	// close the channel.
	go func() {
		<-ctx.Done()
		f.unregisterWatcher(fw)
		_ = w.Close()
		<-fw.done
		close(fw.ch)
	}()

	return fw.ch, nil
}

// Emit implements RepositoryEvents. It broadcasts a programmatic event
// (access bumps, test simulation) to all active Watch subscribers whose
// filters match.
func (f *FsRepo) Emit(ev NodeEvent) {
	f.emitToWatchers(ev)
}

// emit delivers a programmatic event to this subscriber. Called by
// FsRepo.emitToWatchers under watchersMu; sends are non-blocking so a slow
// consumer cannot deadlock the emitter.
func (fw *fsWatch) emit(ev NodeEvent) {
	_, match := fw.ids[ev.NodeID]
	if len(fw.ids) == 0 || match {
		select {
		case fw.ch <- ev:
		default:
		}
	}
}

// loop reads fsnotify events, debounces them, and emits NodeEvents.
// Signals completion by closing fw.done when it returns.
func (fw *fsWatch) loop(ctx context.Context, watchRoot bool) {
	defer close(fw.done)

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
					case fw.ch <- p.event:
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
func (fw *fsWatch) classify(ev fsnotify.Event, watchRoot bool) (NodeEvent, bool) {
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

var _ RepositoryEvents = (*FsRepo)(nil)
