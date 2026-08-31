package keg

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Dex returns the keg's current index. Repository-backed indexes are reloaded
// for every aggregate read so a long-lived hub process does not serve a stale
// view after another process or replica updates the repository.
func (k *LocalKeg) Dex(ctx context.Context) (*Dex, error) {
	return withKegReadValue(ctx, k, k.readDex)
}

func (k *LocalKeg) readDex(ctx context.Context) (*Dex, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve dex: %w", err)
	}
	return k.ensureDexFresh(ctx)
}

// dexOptions reads the keg settings and returns DexOptions to apply when
// constructing or initialising a Dex. If the settings is absent or cannot be
// read, an empty (nil) slice is returned so callers can proceed without error.
func (k *LocalKeg) dexOptions(ctx context.Context) ([]DexOption, error) {
	cfg, err := k.Repo.ReadSettings(ctx)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return []DexOption{WithSettings(cfg)}, nil
}

// ensureDexFresh reloads repository index artifacts under the dex mutex.
func (k *LocalKeg) ensureDexFresh(ctx context.Context) (*Dex, error) {
	k.dexMu.Lock()
	defer k.dexMu.Unlock()

	opts, _ := k.dexOptions(ctx)
	dex, err := NewDexFromRepo(ctx, k.Repo, opts...)
	k.dex = dex
	return dex, err
}

// recordDexWrite updates the generation counter after a successful Dex.Write.
// Caller must hold k.dexMu.
func (k *LocalKeg) recordDexWrite() {
	k.dexWriteGen++
}

// writeNodeToDex adds or updates a node in the dex, persists dex artifacts,
// records the write, and touches the keg settings updated timestamp. When
// updatedAt is zero, the runtime clock is used for the settings timestamp.
func (k *LocalKeg) writeNodeToDex(ctx context.Context, data *NodeData, updatedAt time.Time) error {
	return k.writeNodesToDex(ctx, []*NodeData{data}, updatedAt)
}

// writeNodesToDex updates several nodes in one in-memory dex generation and
// persists the generated indexes once. Existing entries are retained, which
// is important for repositories whose dex may contain normalized stats that
// have not yet been split into distinct stats records.
func (k *LocalKeg) writeNodesToDex(ctx context.Context, nodes []*NodeData, updatedAt time.Time) error {
	if len(nodes) == 0 {
		return nil
	}
	firstID := nodes[0].ID
	dex, err := k.ensureDexFresh(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve dex for node %s: %w", firstID, err)
	}
	for _, data := range nodes {
		if err := dex.Add(ctx, data); err != nil {
			return fmt.Errorf("failed to add node %s to dex: %w", data.ID, err)
		}
	}
	if err := dex.Write(ctx, k.Repo); err != nil {
		return fmt.Errorf("failed to write dex for node %s: %w", firstID, err)
	}

	k.dexMu.Lock()
	k.recordDexWrite()
	k.dexMu.Unlock()

	if updatedAt.IsZero() {
		updatedAt = k.Runtime.Clock().Now()
	}
	if err := k.touchSettingsUpdated(ctx, updatedAt); err != nil {
		return fmt.Errorf("failed to touch keg settings after dex write for node %s: %w", firstID, err)
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
