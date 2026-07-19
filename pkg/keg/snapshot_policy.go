package keg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"
)

// SnapshotPolicyResult summarizes one automatic snapshot-policy scan.
type SnapshotPolicyResult struct {
	Mode      string
	IdleAfter time.Duration
	Scanned   int
	Created   []Snapshot
}

// CreatedCount returns the number of snapshots appended during the scan.
func (r SnapshotPolicyResult) CreatedCount() int {
	return len(r.Created)
}

// AutoSnapshotMessage returns the deterministic message used for policy
// snapshots at a given idle window.
func AutoSnapshotMessage(idleAfter time.Duration) string {
	return fmt.Sprintf("auto snapshot after %s idle", formatSnapshotDuration(idleAfter))
}

// RunSnapshotPolicy scans all nodes in deterministic order and appends
// automatic snapshots for nodes whose live content has drifted from the latest
// snapshot after the configured idle window.
func (k *LocalKeg) RunSnapshotPolicy(ctx context.Context) (SnapshotPolicyResult, error) {
	return withKegWriteValue(ctx, k, k.runSnapshotPolicy)
}

func (k *LocalKeg) runSnapshotPolicy(ctx context.Context) (SnapshotPolicyResult, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return SnapshotPolicyResult{}, fmt.Errorf("failed to run snapshot policy: %w", err)
	}

	cfg, err := k.Repo.ReadConfig(ctx)
	if err != nil {
		return SnapshotPolicyResult{}, fmt.Errorf("read snapshot policy config: %w", err)
	}
	mode, idleAfter, err := cfg.SnapshotPolicy()
	if err != nil {
		return SnapshotPolicyResult{}, err
	}
	result := SnapshotPolicyResult{Mode: mode, IdleAfter: idleAfter}
	if mode == SnapshotModeOff {
		return result, nil
	}
	if _, ok := repoSnapshots(k.Repo); !ok {
		return result, ErrNotSupported
	}

	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return result, fmt.Errorf("list nodes for snapshot policy: %w", err)
	}
	slices.SortFunc(ids, func(a, b NodeId) int { return a.Compare(b) })
	result.Scanned = len(ids)

	msg := AutoSnapshotMessage(idleAfter)
	for _, id := range ids {
		snap, created, err := k.appendPolicySnapshotIfEligible(ctx, id, idleAfter, msg)
		if err != nil {
			if errors.Is(err, ErrNotExist) {
				continue
			}
			return result, fmt.Errorf("snapshot policy node %s: %w", id.Path(), err)
		}
		if created {
			result.Created = append(result.Created, snap)
		}
	}
	if err := k.refreshSnapshotIndexesAfterPolicy(ctx, len(result.Created)); err != nil {
		return result, err
	}
	return result, nil
}

func (k *LocalKeg) appendPolicySnapshotIfEligible(ctx context.Context, id NodeId, idleAfter time.Duration, msg string) (Snapshot, bool, error) {
	var out Snapshot
	created := false
	err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		eligible, err := k.policySnapshotEligibleLocked(lockCtx, id, idleAfter)
		if err != nil {
			return err
		}
		if !eligible {
			return nil
		}
		snap, err := k.appendSnapshotLocked(lockCtx, id, msg)
		if err != nil {
			return err
		}
		out = snap
		created = true
		return nil
	})
	return out, created, err
}

func (k *LocalKeg) policySnapshotEligibleLocked(ctx context.Context, id NodeId, idleAfter time.Duration) (bool, error) {
	content, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		return false, err
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return false, nil
	}

	stats, err := k.Repo.ReadStats(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrNotExist) {
			return false, err
		}
		stats = &NodeStats{}
	}
	if stats != nil {
		updated := stats.Updated()
		if !updated.IsZero() && k.Runtime.Clock().Now().Sub(updated) < idleAfter {
			return false, nil
		}
	}

	snapshots, ok := repoSnapshots(k.Repo)
	if !ok {
		return false, ErrNotSupported
	}
	existing, err := snapshots.ListSnapshots(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrNotExist) {
			return false, err
		}
		existing = nil
	}
	if len(existing) == 0 {
		return true, nil
	}
	latest := existing[len(existing)-1]
	currentContentHash := hashSnapshotBytes(k.Runtime, content)
	if latest.ContentHash != "" && latest.ContentHash == currentContentHash {
		return false, nil
	}

	latestContent, err := snapshots.ReadContentAt(ctx, id, latest.ID)
	if err != nil {
		return false, fmt.Errorf("read latest snapshot content rev %d: %w", latest.ID, err)
	}
	return !bytes.Equal(latestContent, content), nil
}
