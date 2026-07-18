package keg

import (
	"context"
	"fmt"
	"time"
)

// memoryLockEntry holds process-local advisory lock state for a single node.
// It does not survive a process restart or coordinate separate processes;
// production backends must document their own deployment scope. The
// waiters channel is closed when the entry is released (or force-released),
// so blocked AcquireLock callers wake deterministically without having to
// poll on a wall-clock ticker.
type memoryLockEntry struct {
	info    LockInfo
	waiters chan struct{}
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
			// If we are taking over a stale entry, wake any waiters that
			// were parked on its channel before overwriting the slot.
			if held && entry != nil && entry.waiters != nil {
				close(entry.waiters)
			}
			r.crossLocks[key] = &memoryLockEntry{
				info:    info,
				waiters: make(chan struct{}),
			}
			r.mu.Unlock()
			return token, nil
		}
		// Capture the waiters channel under the lock so we cannot miss
		// the close that will be performed by a concurrent release.
		waiters := entry.waiters
		expiresIn := entry.info.expiresAt().Sub(r.runtime.Clock().Now())
		r.mu.Unlock()

		if waiters == nil {
			// Defensive fallback: entry was constructed without a waiter
			// channel. Yield briefly via context to avoid a tight loop.
			select {
			case <-ctx.Done():
				return "", fmt.Errorf("%w: %w", ErrLockTimeout, ctx.Err())
			default:
			}
			continue
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%w: %w", ErrLockTimeout, ctx.Err())
		case <-waiters:
			// Holder released; retry acquisition.
		case <-r.runtime.SchedulingClock().After(expiresIn):
			// The advisory lease reached its TTL; retry and replace it if stale.
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
	if entry.waiters != nil {
		close(entry.waiters)
	}
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
	if entry, held := r.crossLocks[key]; held {
		delete(r.crossLocks, key)
		if entry.waiters != nil {
			close(entry.waiters)
		}
	}
	return nil
}

var _ RepositoryLock = (*MemoryRepo)(nil)
