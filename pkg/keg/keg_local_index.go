package keg

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
)

// IndexNode updates a node's metadata by re-parsing its content and extracting
// properties like title, lead, and content hash. The dex is also updated to reflect
// any changes. If content hasn't changed, this is a no-op.
func (k *LocalKeg) IndexNode(ctx context.Context, id NodeId) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to update node: %w", err)
	}

	var nodeData *NodeData
	err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		updated, changed, err := k.indexNodeLocked(lockCtx, id)
		if err != nil {
			return err
		}
		if changed {
			nodeData = updated
		}
		return nil
	})
	if err != nil {
		return err
	}
	if nodeData == nil {
		return nil
	}
	if err := k.writeNodeToDex(ctx, id, nodeData); err != nil {
		return err
	}
	return k.refreshDirtyIndex(ctx)
}

type IndexOptions struct {
	NoUpdate bool
}

// Index rebuilds all keg indices from scratch.
// Every node is scanned, metadata and stats are refreshed (unless
// NoUpdate is set), and the full dex is regenerated.
func (k *LocalKeg) Index(ctx context.Context, opts IndexOptions) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to re index keg: %w", err)
	}

	k.dexMu.Lock()
	if k.dex == nil {
		k.dex = &Dex{}
		// Apply config-driven options (e.g. tag-filtered indexes) to the new Dex.
		dexOpts, _ := k.dexOptions(ctx)
		for _, opt := range dexOpts {
			_ = opt(k.dex)
		}
	} else {
		// Clear preserves registered custom IndexBuilders while emptying their data.
		k.dex.Clear(ctx)
	}
	dex := k.dex // capture pointer under lock for use after unlock
	k.dexMu.Unlock()

	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	now := k.Runtime.Clock().Now()

	// Process nodes in parallel. Each node's probe, read, refresh, and
	// persist steps are independent. Results are collected and fed to
	// dex.Add sequentially after all workers complete.
	type indexResult struct {
		id   NodeId
		data *NodeData
		errs []error
	}

	workers := runtime.NumCPU()
	if workers > 16 {
		workers = 16
	}
	if workers < 1 {
		workers = 1
	}

	sem := make(chan struct{}, workers)
	results := make([]indexResult, len(ids))

	var wg sync.WaitGroup
	for i, id := range ids {
		// Check for cancellation before spawning each goroutine.
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(idx int, nodeID NodeId) {
			defer wg.Done()
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release

			// Skip work if context was cancelled while waiting for the semaphore.
			if ctx.Err() != nil {
				return
			}
			res := indexResult{id: nodeID}
			res.data, res.errs = k.indexNode(ctx, nodeID, opts, now)
			results[idx] = res
		}(i, id)
	}
	wg.Wait()

	if ctx.Err() != nil {
		return fmt.Errorf("index rebuild cancelled: %w", ctx.Err())
	}

	// Sequentially add results to the dex and collect errors.
	var errs []error
	for _, res := range results {
		errs = append(errs, res.errs...)
		if res.data != nil {
			if err := dex.Add(ctx, res.data); err != nil {
				errs = append(errs, fmt.Errorf("failed to add node %s: %w", res.id, err))
			}
		}
	}

	if err := dex.Write(ctx, k.Repo); err != nil {
		errs = append(errs, fmt.Errorf("failed to save dex: %w", err))
	} else {
		k.dexMu.Lock()
		k.recordDexWrite()
		k.dexMu.Unlock()
	}
	if err := k.touchConfigUpdated(ctx, now); err != nil {
		errs = append(errs, fmt.Errorf("failed to update index timestamp: %w", err))
	}
	if err := k.refreshSnapshotGeneratedIndexes(ctx); err != nil {
		errs = append(errs, fmt.Errorf("failed to refresh snapshot indexes: %w", err))
	}

	return errors.Join(errs...)
}

// indexNode processes a single node for index rebuild: probes for missing
// files, reads content/meta/stats, refreshes if needed, and persists
// changes. It is safe to call concurrently for different nodes.
func (k *LocalKeg) indexNode(ctx context.Context, id NodeId, opts IndexOptions, now time.Time) (*NodeData, []error) {
	// Skip bare directories created by Next() but not yet populated by Create().
	exists, existErr := k.nodeExistsWithContent(ctx, id)
	if existErr != nil {
		return nil, []error{existErr}
	}
	if !exists {
		return nil, nil
	}

	var errs []error

	metaMissing, statsMissing, probeErr := k.nodeFilesMissing(ctx, id)
	if probeErr != nil {
		return nil, []error{probeErr}
	}

	data, nodeErrs := k.getNodeBestEffort(ctx, id)
	if len(nodeErrs) > 0 {
		errs = append(errs, nodeErrs...)
	}

	if data.Meta == nil {
		data.Meta = NewMeta(ctx, time.Time{})
	}
	if data.Stats == nil {
		data.Stats = &NodeStats{}
	}

	needsRefresh := metaMissing ||
		statsMissing ||
		(!opts.NoUpdate && (data.sourceChanged(k.Runtime) || data.Stats.Title() == "" ||
			data.Stats.Hash() == "" ||
			data.Stats.Created().IsZero() ||
			data.Stats.Updated().IsZero()))

	if needsRefresh {
		if err := data.updateMeta(ctx, k.Runtime, &now); err != nil {
			errs = append(errs, err)
			// Still return data so the node is not silently dropped
			// from the index. The best-effort data is better than
			// losing the node entirely.
			return data, errs
		}
	}

	data.Stats.EnsureTimes(now)

	needsPersist := metaMissing || statsMissing || needsRefresh
	if needsPersist {
		err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
			if err := k.Repo.WriteMeta(lockCtx, id, []byte(data.Meta.ToYAML())); err != nil {
				return fmt.Errorf("failed to write node meta %s: %w", id.Path(), err)
			}
			if err := k.Repo.WriteStats(lockCtx, id, data.Stats); err != nil {
				return fmt.Errorf("failed to write node stats %s: %w", id.Path(), err)
			}
			return nil
		})
		if err != nil {
			errs = append(errs, err)
			// Still return data so the node is not silently dropped
			// from the index on persist failure.
			return data, errs
		}
	}

	return data, errs
}

func (k *LocalKeg) indexNodeLocked(ctx context.Context, id NodeId) (*NodeData, bool, error) {
	n := k.Node(id)
	changed, err := n.Changed(ctx)
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return nil, false, nil
	}
	if err := n.Update(ctx); err != nil {
		return nil, false, fmt.Errorf("failed to update node %s: %w", id, err)
	}
	return n.data, true, nil
}
