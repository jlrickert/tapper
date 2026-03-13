package keg

import (
	"context"
	"fmt"
	"time"
)

// memoryLockEntry holds cross-process lock state for a single node.
type memoryLockEntry struct {
	info LockInfo
}

// AcquireLock implements RepositoryLock.
func (r *MemoryRepo) AcquireLock(ctx context.Context, id NodeId) (LockToken, error) {
	key := lockNodeKey(id)
	for {
		r.mu.Lock()
		entry, held := r.crossLocks[key]
		if !held || entry.info.IsStale(r.runtime.Clock().Now()) {
			token := generateLockToken()
			info := LockInfo{
				Token:      token,
				AcquiredAt: r.runtime.Clock().Now(),
				TTLSeconds: int(DefaultLockTTL / time.Second),
				Holder:     "memory-repo",
			}
			if r.crossLocks == nil {
				r.crossLocks = make(map[NodeId]*memoryLockEntry)
			}
			r.crossLocks[key] = &memoryLockEntry{info: info}
			r.mu.Unlock()
			return token, nil
		}
		r.mu.Unlock()

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%w: %w", ErrLockTimeout, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// ReleaseLock implements RepositoryLock.
func (r *MemoryRepo) ReleaseLock(ctx context.Context, id NodeId, token LockToken) error {
	key := lockNodeKey(id)
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, held := r.crossLocks[key]
	if !held {
		return ErrNotLocked
	}
	if entry.info.Token != token {
		return fmt.Errorf("%w: lock held by %q", ErrLockTokenMismatch, entry.info.Holder)
	}
	delete(r.crossLocks, key)
	return nil
}

// LockStatus implements RepositoryLock.
func (r *MemoryRepo) LockStatus(ctx context.Context, id NodeId) (LockInfo, error) {
	key := lockNodeKey(id)
	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, held := r.crossLocks[key]
	if !held {
		return LockInfo{}, nil
	}
	if entry.info.IsStale(r.runtime.Clock().Now()) {
		return LockInfo{}, nil
	}
	return entry.info, nil
}

// ForceReleaseLock implements RepositoryLock.
func (r *MemoryRepo) ForceReleaseLock(ctx context.Context, id NodeId) error {
	key := lockNodeKey(id)
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.crossLocks, key)
	return nil
}

var _ RepositoryLock = (*MemoryRepo)(nil)
