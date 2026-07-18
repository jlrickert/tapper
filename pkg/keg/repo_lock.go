package keg

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"
)

// DefaultLockTTL is the default time-to-live for a cross-process lock.
const DefaultLockTTL = 5 * time.Minute

// LockToken is an opaque string identifying lock ownership.
type LockToken string

// LockInfo describes the current state of a cross-process node lock.
type LockInfo struct {
	Token      LockToken `json:"token"`
	AcquiredAt time.Time `json:"acquired_at"`
	TTLSeconds int       `json:"ttl_seconds"`
	Holder     string    `json:"holder"`
}

// IsStale reports whether the lock has expired based on its TTL.
func (li LockInfo) IsStale(now time.Time) bool {
	if li.Token == "" {
		return true
	}
	return !now.Before(li.AcquiredAt.Add(time.Duration(li.TTLSeconds) * time.Second))
}

func (li LockInfo) expiresAt() time.Time {
	return li.AcquiredAt.Add(time.Duration(li.TTLSeconds) * time.Second)
}

// RepositoryLock provides cross-process token-based node locking. This is an
// optional interface (like RepositoryFiles, RepositoryImages, RepositorySnapshots).
// Not all repository implementations need cross-process locking.
//
// Cross-process locks are separate from the process-scoped WithNodeLock on the
// core Repository interface. WithNodeLock serializes concurrent goroutines
// within a single process; RepositoryLock coordinates across separate CLI
// invocations or MCP server sessions.
type RepositoryLock interface {
	// AcquireLock acquires a cross-process lock on a node. Returns a token
	// that proves ownership. If the node is already locked by a non-stale
	// lock, blocks until the lock is released or ctx is canceled.
	AcquireLock(ctx context.Context, id NodeId) (LockToken, error)

	// ReleaseLock releases a cross-process lock. The token must match the
	// token returned by AcquireLock. Returns an error if the token does not
	// match or no lock is held.
	ReleaseLock(ctx context.Context, id NodeId, token LockToken) error

	// LockStatus returns the current lock state for a node. If no lock is
	// held (or the lock is stale), returns a zero LockInfo with no error.
	LockStatus(ctx context.Context, id NodeId) (LockInfo, error)

	// ForceReleaseLock unconditionally removes a lock regardless of token
	// ownership. Use as an escape hatch for stuck or stale locks.
	ForceReleaseLock(ctx context.Context, id NodeId) error
}

// ErrLockTokenMismatch indicates the provided token does not match the held lock.
var ErrLockTokenMismatch = errors.New("lock token mismatch")

// ErrNotLocked indicates no lock is held on the node.
var ErrNotLocked = errors.New("node is not locked")

// generateLockToken returns a new random UUID-v4 lock token.
func generateLockToken() LockToken {
	var uuid [16]byte
	_, _ = rand.Read(uuid[:])
	uuid[6] = (uuid[6] & 0x0f) | 0x40 // version 4
	uuid[8] = (uuid[8] & 0x3f) | 0x80 // variant 2
	return LockToken(fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16],
	))
}
