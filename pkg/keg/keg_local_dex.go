package keg

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// Dex returns the keg's index with always-fresh semantics: the cached dex is
// reused only while it is provably current (see dexStale), otherwise it is
// reloaded from the repository. Config-driven query-filtered indexes are
// applied automatically via WithConfig. Safe for both short-lived CLI
// invocations and long-lived processes (serve handlers, MCP servers) where
// another process may update the index between calls.
func (k *LocalKeg) Dex(ctx context.Context) (*Dex, error) {
	return withKegReadValue(ctx, k, k.readDex)
}

func (k *LocalKeg) readDex(ctx context.Context) (*Dex, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve dex: %w", err)
	}
	return k.ensureDexFresh(ctx)
}

// dexOptions reads the keg config and returns DexOptions to apply when
// constructing or initialising a Dex. If the config is absent or cannot be
// read, an empty (nil) slice is returned so callers can proceed without error.
func (k *LocalKeg) dexOptions(ctx context.Context) ([]DexOption, error) {
	cfg, err := k.Repo.ReadConfig(ctx)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return []DexOption{WithConfig(cfg)}, nil
}

// -- private utility functions

// indexFileMtime returns the ModTime of dex/nodes.tsv for FsRepo backends.
// For non-filesystem repos (e.g. MemoryRepo) it returns time.Time{} (zero).
func (k *LocalKeg) indexFileMtime() time.Time {
	fsRepo, ok := k.Repo.(*FsRepo)
	if !ok {
		return time.Time{}
	}
	idxPath := filepath.Join(fsRepo.Root, "dex", "nodes.tsv")
	info, err := fsRepo.runtime.Stat(idxPath, false)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// dexStale reports whether the cached dex is out of date. For MemoryRepo
// (single-process) this always returns false because there are no external
// writers. For FsRepo it compares the current mtime of dex/nodes.tsv against
// the mtime recorded when the dex was last loaded. Other repository
// implementations are treated as external and always stale.
//
// Caller must hold k.dexMu.
func (k *LocalKeg) dexStale() bool {
	if generation, ok := k.Repo.(interface{ kegOperationGeneration() uint64 }); ok {
		return generation.kegOperationGeneration() != k.dexLoadGeneration
	}
	if _, ok := k.Repo.(*FsRepo); !ok {
		return true
	}
	current := k.indexFileMtime()
	if current.IsZero() {
		// File doesn't exist — treat as stale so we rebuild.
		return true
	}
	return !current.Equal(k.dexLoadMtime)
}

// ensureDexFresh returns the cached dex if it is still current, otherwise
// reloads it from disk. This replaces the pattern of InvalidateDex() +
// Dex(ctx) which unconditionally discarded the cache.
//
// ensureDexFresh acquires k.dexMu internally; callers must NOT hold it.
func (k *LocalKeg) ensureDexFresh(ctx context.Context) (*Dex, error) {
	k.dexMu.Lock()
	defer k.dexMu.Unlock()

	if k.dex != nil && !k.dexStale() {
		return k.dex, nil
	}

	opts, _ := k.dexOptions(ctx)
	dex, err := NewDexFromRepo(ctx, k.Repo, opts...)
	k.dex = dex
	k.dexLoadMtime = k.indexFileMtime()
	if generation, ok := k.Repo.(interface{ kegOperationGeneration() uint64 }); ok {
		k.dexLoadGeneration = generation.kegOperationGeneration()
	}
	return dex, err
}

// recordDexWrite updates the mtime cache and generation counter after a
// successful Dex.Write. Caller must hold k.dexMu.
func (k *LocalKeg) recordDexWrite() {
	k.dexLoadMtime = k.indexFileMtime()
	if generation, ok := k.Repo.(interface{ kegOperationGeneration() uint64 }); ok {
		k.dexLoadGeneration = generation.kegOperationGeneration()
	}
	k.dexWriteGen++
}

// writeNodeToDex adds or updates a node in the dex, persists dex artifacts,
// records the write, and touches the keg config updated timestamp. When
// updatedAt is zero, the runtime clock is used for the config timestamp.
func (k *LocalKeg) writeNodeToDex(ctx context.Context, data *NodeData, updatedAt time.Time) error {
	id := data.ID
	dex, err := k.ensureDexFresh(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve dex for node %s: %w", id, err)
	}
	if err := dex.Add(ctx, data); err != nil {
		return fmt.Errorf("failed to add node %s to dex: %w", id, err)
	}
	if err := dex.Write(ctx, k.Repo); err != nil {
		return fmt.Errorf("failed to write dex for node %s: %w", id, err)
	}

	k.dexMu.Lock()
	k.recordDexWrite()
	k.dexMu.Unlock()

	if updatedAt.IsZero() {
		updatedAt = k.Runtime.Clock().Now()
	}
	if err := k.touchConfigUpdated(ctx, updatedAt); err != nil {
		return fmt.Errorf("failed to touch keg config after dex write for node %s: %w", id, err)
	}
	return nil
}

// InvalidateDex clears the cached dex so the next Dex() call reloads from
// the repository. This is useful when external processes may have modified
// the index files.
func (k *LocalKeg) InvalidateDex() {
	k.dexMu.Lock()
	k.dex = nil
	k.dexMu.Unlock()
}
