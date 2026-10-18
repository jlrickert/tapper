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

// DefaultNodeLockWait is how long a node lock acquisition waits for a held
// lock when the caller names no bound. MaxNodeLockWait caps any requested
// bound, so a caller never hangs for the lock's whole TTL.
const (
	DefaultNodeLockWait = 10 * time.Second
	MaxNodeLockWait     = 60 * time.Second
)

// ClampNodeLockWait returns the node lock wait bound for a requested d: the
// default when d is zero or less, and at most MaxNodeLockWait.
func ClampNodeLockWait(d time.Duration) time.Duration {
	switch {
	case d <= 0:
		return DefaultNodeLockWait
	case d > MaxNodeLockWait:
		return MaxNodeLockWait
	default:
		return d
	}
}

type nodeLockWaitContextKey struct{}

// WithNodeLockWait returns ctx carrying a bound on how long Keg.Lock waits for
// a node lock another holder has. RemoteKeg sends it to the Hub as
// ?wait=<seconds>; LocalKeg gives up acquiring after d. Either way a lock still
// held when the bound passes fails with ErrLockTimeout. A d of zero or less
// leaves the bound to the backend.
func WithNodeLockWait(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, nodeLockWaitContextKey{}, d)
}

// NodeLockWaitFrom returns the node lock wait bound carried by ctx, or zero
// when the caller set none.
func NodeLockWaitFrom(ctx context.Context) time.Duration {
	d, _ := ctx.Value(nodeLockWaitContextKey{}).(time.Duration)
	if d < 0 {
		return 0
	}
	return d
}

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
