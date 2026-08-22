package keg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type memoryRepoState struct {
	nodes     map[NodeId]*memoryNode
	indexes   map[string][]byte
	schemas   map[string][]byte
	snapshots map[NodeId][]memorySnapshotEntry
	config    *Config
}

func (r *MemoryRepo) WithKegAtomicWrite(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("fn required")
	}
	return r.WithKegWrite(ctx, func(writeCtx context.Context) error {
		state, err := r.cloneState(writeCtx)
		if err != nil {
			return err
		}
		if err := fn(writeCtx); err != nil {
			r.restoreState(state)
			return err
		}
		return nil
	})
}

func (r *MemoryRepo) cloneState(ctx context.Context) (*memoryRepoState, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state := &memoryRepoState{
		nodes: make(map[NodeId]*memoryNode, len(r.nodes)), indexes: make(map[string][]byte, len(r.indexes)),
		schemas: make(map[string][]byte, len(r.schemas)), snapshots: make(map[NodeId][]memorySnapshotEntry, len(r.snapshots)),
	}
	for id, n := range r.nodes {
		copyNode := &memoryNode{content: cloneBytes(n.content), meta: cloneBytes(n.meta), stats: cloneBytes(n.stats), items: map[string][]byte{}, images: map[string][]byte{}}
		for name, data := range n.items {
			copyNode.items[name] = cloneBytes(data)
		}
		for name, data := range n.images {
			copyNode.images[name] = cloneBytes(data)
		}
		state.nodes[id] = copyNode
	}
	for name, data := range r.indexes {
		state.indexes[name] = cloneBytes(data)
	}
	for name, data := range r.schemas {
		state.schemas[name] = cloneBytes(data)
	}
	for id, entries := range r.snapshots {
		cloned := make([]memorySnapshotEntry, len(entries))
		for i, entry := range entries {
			cloned[i] = entry
			cloned[i].content = cloneBytes(entry.content)
			cloned[i].meta = cloneBytes(entry.meta)
			cloned[i].stats = cloneBytes(entry.stats)
		}
		state.snapshots[id] = cloned
	}
	if r.config != nil {
		raw, err := json.Marshal(r.config)
		if err != nil {
			return nil, err
		}
		var cfg Config
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return nil, err
		}
		state.config = &cfg
	}
	_ = ctx
	return state, nil
}

func (r *MemoryRepo) restoreState(state *memoryRepoState) {
	if state == nil {
		return
	}
	r.mu.Lock()
	r.nodes, r.indexes, r.schemas, r.snapshots, r.config = state.nodes, state.indexes, state.schemas, state.snapshots, state.config
	r.mu.Unlock()
}

type fsBackupEntry struct {
	dir  bool
	mode os.FileMode
	data []byte
}

func (f *FsRepo) WithKegAtomicWrite(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("fn required")
	}
	return f.WithKegWrite(ctx, func(writeCtx context.Context) error {
		backup, err := f.captureRoot()
		if err != nil {
			return err
		}
		if err := fn(writeCtx); err != nil {
			return errors.Join(err, f.restoreRoot(backup))
		}
		return nil
	})
}

func (f *FsRepo) captureRoot() (map[string]fsBackupEntry, error) {
	out := map[string]fsBackupEntry{".": {dir: true, mode: 0o755}}
	var walk func(string) error
	walk = func(rel string) error {
		path := f.Root
		if rel != "." {
			path = filepath.Join(f.Root, rel)
		}
		entries, err := f.runtime.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			child := entry.Name()
			if rel != "." {
				child = filepath.Join(rel, child)
			}
			if child == KegOperationLock {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.IsDir() {
				out[child] = fsBackupEntry{dir: true, mode: info.Mode().Perm()}
				if err := walk(child); err != nil {
					return err
				}
				continue
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("atomic KEG write does not support %s", child)
			}
			data, err := f.runtime.ReadFile(filepath.Join(f.Root, child))
			if err != nil {
				return err
			}
			out[child] = fsBackupEntry{mode: info.Mode().Perm(), data: data}
		}
		return nil
	}
	return out, walk(".")
}

func (f *FsRepo) restoreRoot(backup map[string]fsBackupEntry) error {
	current, err := f.captureRoot()
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(current))
	for path := range current {
		if path != "." {
			paths = append(paths, path)
		}
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	var errs []error
	for _, rel := range paths {
		if _, keep := backup[rel]; keep {
			continue
		}
		errs = append(errs, f.runtime.Remove(filepath.Join(f.Root, rel), true))
	}
	paths = paths[:0]
	for path := range backup {
		if path != "." {
			paths = append(paths, path)
		}
	}
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) < len(paths[j]) })
	for _, rel := range paths {
		entry := backup[rel]
		path := filepath.Join(f.Root, rel)
		if entry.dir {
			errs = append(errs, f.runtime.Mkdir(path, entry.mode, true))
			continue
		}
		errs = append(errs, f.runtime.WriteFile(path, entry.data, entry.mode))
	}
	return errors.Join(errs...)
}

var _ RepositoryAtomicWrite = (*MemoryRepo)(nil)
var _ RepositoryAtomicWrite = (*FsRepo)(nil)
