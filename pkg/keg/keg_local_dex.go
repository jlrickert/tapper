package keg

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// SetExtraDexOpts stores additional DexOptions that will be included whenever
// the dex is loaded or refreshed. These options are prepended before
// WithConfig so that injected resolvers (e.g. WithQueryResolver) are available
// when WithConfig creates QueryFilteredIndex instances.
//
// This is the injection point for higher-level packages (e.g. pkg/tapper) to
// provide capabilities that pkg/keg cannot import directly.
func (k *LocalKeg) SetExtraDexOpts(opts ...DexOption) {
	k.dexMu.Lock()
	defer k.dexMu.Unlock()
	k.extraDexOpts = opts
	// Invalidate the cached dex so the next access rebuilds with the new options.
	k.dex = nil
}

// Dex returns the keg's index, loading it from the repository on first access.
// The dex is lazily loaded and cached in memory for efficient access.
// Config-driven query-filtered indexes are applied automatically via WithConfig.
//
// Dex is the cache-only fast path: it returns whatever is already in memory
// and does not check whether another process has updated the on-disk index.
// This is correct for short-lived CLI invocations, which read the dex once
// and exit. Long-lived processes that hold a *LocalKeg across calls (the MCP
// server, tap site serve, anything sharing an FsRepo with external writers)
// MUST use DexFresh instead, which reloads when the repository may have been
// updated externally. Calling Dex from a long-lived process risks serving stale
// results until the process restarts.
func (k *LocalKeg) Dex(ctx context.Context) (*Dex, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve dex: %w", err)
	}

	k.dexMu.Lock()
	defer k.dexMu.Unlock()

	if k.dex != nil {
		return k.dex, nil
	}
	opts, _ := k.dexOptions(ctx)
	dex, err := NewDexFromRepo(ctx, k.Repo, opts...)
	k.dex = dex
	k.dexLoadMtime = k.indexFileMtime()
	return dex, err
}

// DexFresh returns the keg's index, reloading if the repository may have
// changed since the last load. This is the correct method for long-lived
// processes (serve handlers, MCP servers) where another process may update the
// dex between calls. For FsRepo backends, it compares the mtime of
// dex/nodes.tsv; for MemoryRepo (single-process) it behaves identically to Dex.
// Other repository implementations are treated as external and reloaded.
func (k *LocalKeg) DexFresh(ctx context.Context) (*Dex, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve dex: %w", err)
	}
	return k.ensureDexFresh(ctx)
}

// dexOptions reads the keg config and returns DexOptions to apply when
// constructing or initialising a Dex. If the config is absent or cannot be
// read, an empty (nil) slice is returned so callers can proceed without error.
//
// Extra options injected via SetExtraDexOpts are prepended before WithConfig
// so that resolvers (e.g. WithQueryResolver) are installed on the Dex before
// WithConfig creates QueryFilteredIndex instances that reference them.
func (k *LocalKeg) dexOptions(ctx context.Context) ([]DexOption, error) {
	cfg, err := k.Repo.ReadConfig(ctx)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	// Prepend extraDexOpts so resolvers are set before WithConfig runs.
	opts := make([]DexOption, 0, len(k.extraDexOpts)+1)
	opts = append(opts, k.extraDexOpts...)
	opts = append(opts, WithConfig(cfg))
	return opts, nil
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
	if _, ok := k.Repo.(*MemoryRepo); ok {
		return false
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
	return dex, err
}

// recordDexWrite updates the mtime cache and generation counter after a
// successful Dex.Write. Caller must hold k.dexMu.
func (k *LocalKeg) recordDexWrite() {
	k.dexLoadMtime = k.indexFileMtime()
	k.dexWriteGen++
}

func (k *LocalKeg) writeNodeToDex(ctx context.Context, id NodeId, data *NodeData) error {
	dex, err := k.ensureDexFresh(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve dex: %w", err)
	}
	if err := dex.Add(ctx, data); err != nil {
		return fmt.Errorf("failed to add node %s to dex: %w", id, err)
	}
	if err := dex.Write(ctx, k.Repo); err != nil {
		return fmt.Errorf("failed to write dex: %w", err)
	}

	k.dexMu.Lock()
	k.recordDexWrite()
	k.dexMu.Unlock()

	return k.touchConfigUpdated(ctx, k.Runtime.Clock().Now())
}

// InvalidateDex clears the cached dex so the next Dex() call reloads from
// the repository. This is useful when external processes may have modified
// the index files.
func (k *LocalKeg) InvalidateDex() {
	k.dexMu.Lock()
	k.dex = nil
	k.dexMu.Unlock()
}

// addNodeToDex adds a node to the dex, writes dex changes to the repository,
// and updates the keg's Updated timestamp to the provided time (or now if not specified).
func (k *LocalKeg) addNodeToDex(ctx context.Context, data *NodeData, now *time.Time) error {
	dex, err := k.ensureDexFresh(ctx)
	if err != nil {
		return err
	}

	if err := dex.Add(ctx, data); err != nil {
		return err
	}

	if now != nil {
		if err := dex.Write(ctx, k.Repo); err != nil {
			return err
		}

		k.dexMu.Lock()
		k.recordDexWrite()
		k.dexMu.Unlock()

		if err := k.touchConfigUpdated(ctx, *now); err != nil {
			return err
		}
	}
	return nil
}
