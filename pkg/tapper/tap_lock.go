package tapper

import (
	"context"
	"fmt"

	"github.com/jlrickert/tapper/pkg/keg"
)

// LockOptions configures behavior for Tap.Lock.
type LockOptions struct {
	NodeID string
	KegTargetOptions
}

// UnlockOptions configures behavior for Tap.Unlock.
type UnlockOptions struct {
	NodeID string
	Token  string
	KegTargetOptions
}

// LockStatusOptions configures behavior for Tap.LockStatus.
type LockStatusOptions struct {
	NodeID string
	KegTargetOptions
}

// ForceUnlockOptions configures behavior for Tap.ForceUnlock.
type ForceUnlockOptions struct {
	NodeID string
	KegTargetOptions
}

// Lock acquires a cross-process lock on a node and returns the token.
func (t *Tap) Lock(ctx context.Context, opts LockOptions) (keg.LockToken, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}

	locker, ok := k.Repo.(keg.RepositoryLock)
	if !ok {
		return "", fmt.Errorf("repository does not support cross-process locking")
	}

	node, err := keg.ParseNode(opts.NodeID)
	if err != nil {
		return "", fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
	}
	if node == nil {
		return "", fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
	}

	id := keg.NodeId{ID: node.ID, Code: node.Code}
	exists, err := k.Repo.HasNode(ctx, id)
	if err != nil {
		return "", fmt.Errorf("unable to inspect node: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("node %s not found", id.Path())
	}

	token, err := locker.AcquireLock(ctx, id)
	if err != nil {
		return "", fmt.Errorf("unable to acquire lock: %w", err)
	}
	return token, nil
}

// Unlock releases a cross-process lock on a node.
func (t *Tap) Unlock(ctx context.Context, opts UnlockOptions) error {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}

	locker, ok := k.Repo.(keg.RepositoryLock)
	if !ok {
		return fmt.Errorf("repository does not support cross-process locking")
	}

	node, err := keg.ParseNode(opts.NodeID)
	if err != nil {
		return fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
	}
	if node == nil {
		return fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
	}

	id := keg.NodeId{ID: node.ID, Code: node.Code}
	return locker.ReleaseLock(ctx, id, keg.LockToken(opts.Token))
}

// LockStatus returns the lock state for a node.
func (t *Tap) LockStatus(ctx context.Context, opts LockStatusOptions) (keg.LockInfo, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return keg.LockInfo{}, fmt.Errorf("unable to open keg: %w", err)
	}

	locker, ok := k.Repo.(keg.RepositoryLock)
	if !ok {
		return keg.LockInfo{}, fmt.Errorf("repository does not support cross-process locking")
	}

	node, err := keg.ParseNode(opts.NodeID)
	if err != nil {
		return keg.LockInfo{}, fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
	}
	if node == nil {
		return keg.LockInfo{}, fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
	}

	id := keg.NodeId{ID: node.ID, Code: node.Code}
	return locker.LockStatus(ctx, id)
}

// ForceUnlock unconditionally removes a cross-process lock on a node.
func (t *Tap) ForceUnlock(ctx context.Context, opts ForceUnlockOptions) error {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}

	locker, ok := k.Repo.(keg.RepositoryLock)
	if !ok {
		return fmt.Errorf("repository does not support cross-process locking")
	}

	node, err := keg.ParseNode(opts.NodeID)
	if err != nil {
		return fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
	}
	if node == nil {
		return fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
	}

	id := keg.NodeId{ID: node.ID, Code: node.Code}
	return locker.ForceReleaseLock(ctx, id)
}

// validateLockToken checks a lock token against any held cross-process lock.
// Rules:
//   - If the repo does not support RepositoryLock, the token is ignored.
//   - If no lock is held, the command proceeds regardless of token.
//   - If a lock is held and the token matches, the command proceeds.
//   - If a lock is held and the token is empty or mismatched, an error is returned.
func validateLockToken(ctx context.Context, repo keg.Repository, id keg.NodeId, lockToken string) error {
	locker, ok := repo.(keg.RepositoryLock)
	if !ok {
		return nil
	}
	info, err := locker.LockStatus(ctx, id)
	if err != nil {
		return fmt.Errorf("unable to check lock status: %w", err)
	}
	if info.Token == "" {
		return nil
	}
	if info.Token != keg.LockToken(lockToken) {
		return fmt.Errorf(
			"%w: node locked by %q since %s",
			keg.ErrLockTokenMismatch,
			info.Holder,
			info.AcquiredAt.Format("2006-01-02 15:04:05"),
		)
	}
	return nil
}
