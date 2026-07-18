package keg

import (
	"context"
	"fmt"
)

// Lock acquires a cross-process advisory lock on a node. Returns
// ErrNotSupported when the backend lacks cross-process locking.
func (k *LocalKeg) Lock(ctx context.Context, id NodeId) (LockInfo, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return LockInfo{}, fmt.Errorf("failed to lock node: %w", err)
	}
	exists, err := k.NodeExists(ctx, id)
	if err != nil {
		return LockInfo{}, err
	}
	if !exists {
		return LockInfo{}, fmt.Errorf("node %s: %w", id.Path(), ErrNotExist)
	}
	locker, ok := k.Repo.(RepositoryLock)
	if !ok {
		return LockInfo{}, ErrNotSupported
	}
	token, err := locker.AcquireLock(ctx, id)
	if err != nil {
		return LockInfo{}, err
	}
	info, err := locker.LockStatus(ctx, id)
	if err != nil {
		return LockInfo{Token: token}, nil
	}
	info.Token = token
	return info, nil
}

// Unlock releases a cross-process lock; the token must match the holder's.
func (k *LocalKeg) Unlock(ctx context.Context, id NodeId, token LockToken) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to unlock node: %w", err)
	}
	locker, ok := k.Repo.(RepositoryLock)
	if !ok {
		return ErrNotSupported
	}
	return locker.ReleaseLock(ctx, id, token)
}

// LockStatus reports the node's current cross-process lock state. A zero
// LockInfo means no live lock is held.
func (k *LocalKeg) LockStatus(ctx context.Context, id NodeId) (LockInfo, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return LockInfo{}, fmt.Errorf("failed to read lock status: %w", err)
	}
	locker, ok := k.Repo.(RepositoryLock)
	if !ok {
		return LockInfo{}, ErrNotSupported
	}
	return locker.LockStatus(ctx, id)
}

// ForceUnlock unconditionally removes a cross-process lock regardless of
// token ownership. Escape hatch for stuck or stale locks.
func (k *LocalKeg) ForceUnlock(ctx context.Context, id NodeId) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to force-unlock node: %w", err)
	}
	locker, ok := k.Repo.(RepositoryLock)
	if !ok {
		return ErrNotSupported
	}
	return locker.ForceReleaseLock(ctx, id)
}
