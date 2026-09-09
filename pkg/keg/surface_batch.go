package keg

import (
	"context"
	"fmt"
	"net/http"
)

// MoveItem is one guarded relocation; occupied destinations are never swaps.
type MoveItem struct {
	Source       int    `json:"source"`
	Destination  int    `json:"destination"`
	ExpectedHash string `json:"expected_hash"`
}

// RemoveItem identifies a node and the version the caller intends to remove.
type RemoveItem struct {
	ID           int    `json:"id"`
	ExpectedHash string `json:"expected_hash"`
}

// RestoreItem selects a revision and guards the current live node version.
type RestoreItem struct {
	ID           int        `json:"id"`
	Revision     RevisionID `json:"rev"`
	ExpectedHash string     `json:"expected_hash"`
}

// MutationResult records the final version and affected link rewrites.
type MutationResult struct {
	ID        int      `json:"id"`
	Hash      string   `json:"hash,omitempty"`
	Rewritten []NodeId `json:"rewritten,omitempty"`
}

func (k *LocalKeg) preflightMutation(ctx context.Context, ids []int, hashes []string) error {
	if err := validateMutationBatchSize(len(ids)); err != nil {
		return err
	}
	seen := map[int]bool{}
	for i, id := range ids {
		if id <= 0 || seen[id] {
			return fmt.Errorf("duplicate or invalid node %d: %w", id, ErrInvalid)
		}
		seen[id] = true
		node, err := k.ReadNode(ctx, NodeId{ID: id})
		if err != nil {
			return err
		}
		if err := checkExpectedHash(fmt.Sprintf("node %d", id), hashes[i], node.Hash(), nodeRecoveryContent(node)); err != nil {
			return err
		}
	}
	return nil
}

// MoveBatch applies the complete relocation batch in one atomic operation.
func (k *LocalKeg) MoveBatch(ctx context.Context, items []MoveItem) ([]MutationResult, error) {
	return withKegAtomicWriteValue(ctx, k, func(ctx context.Context) ([]MutationResult, error) {
		ids, hashes := make([]int, len(items)), make([]string, len(items))
		destinations := map[int]bool{}
		for i, item := range items {
			ids[i], hashes[i] = item.Source, item.ExpectedHash
			if item.Destination <= 0 || destinations[item.Destination] {
				return nil, ErrInvalid
			}
			destinations[item.Destination] = true
		}
		if err := k.preflightMutation(ctx, ids, hashes); err != nil {
			return nil, err
		}
		// Check all destinations before any move so an earlier move cannot vacate a
		// destination and change the established conflict semantics.
		for _, item := range items {
			if item.Source != item.Destination {
				exists, err := k.nodeExistsWithContent(ctx, NodeId{ID: item.Destination})
				if err != nil {
					return nil, err
				}
				if exists {
					return nil, ErrDestinationExists
				}
			}
		}
		out := make([]MutationResult, 0, len(items))
		for _, item := range items {
			// Earlier link rewrites can change another source's hash. All caller hashes
			// were checked at the atomic boundary; the inner operation uses live state.
			node, err := k.ReadNode(ctx, NodeId{ID: item.Source})
			if err != nil {
				return nil, err
			}
			rewritten, err := k.move(ctx, NodeMoveOptions{Source: NodeId{ID: item.Source}, Destination: NodeId{ID: item.Destination}, ExpectedHash: node.Hash()})
			if err != nil {
				return nil, err
			}
			out = append(out, MutationResult{ID: item.Destination, Rewritten: rewritten})
		}
		for i := range out {
			node, err := k.ReadNode(ctx, NodeId{ID: out[i].ID})
			if err != nil {
				return nil, err
			}
			out[i].Hash = node.Hash()
		}
		return out, nil
	})
}

// RemoveBatch removes every requested node or rolls back the whole operation.
func (k *LocalKeg) RemoveBatch(ctx context.Context, items []RemoveItem) ([]MutationResult, error) {
	return withKegAtomicWriteValue(ctx, k, func(ctx context.Context) ([]MutationResult, error) {
		ids, hashes := make([]int, len(items)), make([]string, len(items))
		for i, item := range items {
			ids[i], hashes[i] = item.ID, item.ExpectedHash
		}
		if err := k.preflightMutation(ctx, ids, hashes); err != nil {
			return nil, err
		}
		out := make([]MutationResult, 0, len(items))
		for _, item := range items {
			node, err := k.ReadNode(ctx, NodeId{ID: item.ID})
			if err != nil {
				return nil, err
			}
			rewritten, err := k.remove(ctx, NodeRemoveOptions{ID: NodeId{ID: item.ID}, ExpectedHash: node.Hash()})
			if err != nil {
				return nil, err
			}
			out = append(out, MutationResult{ID: item.ID, Rewritten: rewritten})
		}
		return out, nil
	})
}

// RestoreBatch restores guarded snapshots atomically.
func (k *LocalKeg) RestoreBatch(ctx context.Context, items []RestoreItem) ([]MutationResult, error) {
	return withKegAtomicWriteValue(ctx, k, func(ctx context.Context) ([]MutationResult, error) {
		ids, hashes := make([]int, len(items)), make([]string, len(items))
		for i, item := range items {
			ids[i], hashes[i] = item.ID, item.ExpectedHash
		}
		if err := k.preflightMutation(ctx, ids, hashes); err != nil {
			return nil, err
		}
		out := make([]MutationResult, 0, len(items))
		for _, item := range items {
			if err := k.RestoreSnapshot(ctx, NodeId{ID: item.ID}, item.Revision); err != nil {
				return nil, err
			}
			node, err := k.ReadNode(ctx, NodeId{ID: item.ID})
			if err != nil {
				return nil, err
			}
			out = append(out, MutationResult{ID: item.ID, Hash: node.Hash()})
		}
		return out, nil
	})
}

// RepositoryLockRenewal extends a live lease without replacing its token.
type RepositoryLockRenewal interface {
	// RenewLock requires the current unexpired lease token.
	RenewLock(context.Context, NodeId, LockToken) (LockInfo, error)
}

// RenewLock extends an existing lease using its current token.
func (k *LocalKeg) RenewLock(ctx context.Context, id NodeId, token LockToken) (LockInfo, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return LockInfo{}, err
	}
	if locker, ok := k.Repo.(RepositoryLockRenewal); ok {
		return locker.RenewLock(ctx, id, token)
	}
	return LockInfo{}, ErrNotSupported
}

func (k *RemoteKeg) MoveBatch(ctx context.Context, items []MoveItem) ([]MutationResult, error) {
	var out []MutationResult
	err := k.postJSON(ctx, "/nodes/move", "MoveBatch", map[string]any{"nodes": items}, &out, http.StatusOK)
	return out, err
}
func (k *RemoteKeg) RemoveBatch(ctx context.Context, items []RemoveItem) ([]MutationResult, error) {
	var out []MutationResult
	err := k.postJSON(ctx, "/nodes/remove", "RemoveBatch", map[string]any{"nodes": items}, &out, http.StatusOK)
	return out, err
}
func (k *RemoteKeg) RestoreBatch(ctx context.Context, items []RestoreItem) ([]MutationResult, error) {
	var out []MutationResult
	err := k.postJSON(ctx, "/nodes/snapshots/restore", "RestoreBatch", map[string]any{"nodes": items}, &out, http.StatusOK)
	return out, err
}
func (k *RemoteKeg) RenewLock(ctx context.Context, id NodeId, token LockToken) (LockInfo, error) {
	var out remoteLockInfo
	err := k.postJSON(ctx, fmt.Sprintf("/nodes/%d/lock/renew", id.ID), "RenewLock", map[string]string{"token": string(token)}, &out, http.StatusOK)
	return out.toLockInfo(), err
}
