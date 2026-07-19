package keg

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// KegOperationLock is the root-level filesystem lock directory used to
// serialize complete operations across FsRepo instances and processes.
const KegOperationLock = ".keg-operation-lock"

type kegBoundaryMode uint8

const (
	kegBoundaryRead kegBoundaryMode = iota + 1
	kegBoundaryWrite
)

type kegBoundaryContextKey struct{}

type kegBoundaryContext struct {
	owner any
	mode  kegBoundaryMode
}

func boundaryMode(ctx context.Context, owner any) kegBoundaryMode {
	state, _ := ctx.Value(kegBoundaryContextKey{}).(kegBoundaryContext)
	if state.owner == owner {
		return state.mode
	}
	return 0
}

func contextWithBoundary(ctx context.Context, owner any, mode kegBoundaryMode) context.Context {
	return context.WithValue(ctx, kegBoundaryContextKey{}, kegBoundaryContext{owner: owner, mode: mode})
}

// kegOperationBoundary is a cancellation-aware RW lock. It belongs to a
// repository, so every LocalKeg sharing that repository shares the same
// operation boundary as well.
type kegOperationBoundary struct {
	mu      sync.Mutex
	readers int
	writer  bool
	waiters chan struct{}
}

func (b *kegOperationBoundary) changed() {
	if b.waiters != nil {
		close(b.waiters)
	}
	b.waiters = make(chan struct{})
}

func (b *kegOperationBoundary) acquire(ctx context.Context, write bool) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrLockTimeout, err)
		}
		b.mu.Lock()
		available := !b.writer && (!write || b.readers == 0)
		if available {
			if write {
				b.writer = true
			} else {
				b.readers++
			}
			b.mu.Unlock()
			return func() {
				b.mu.Lock()
				if write {
					b.writer = false
				} else {
					b.readers--
				}
				b.changed()
				b.mu.Unlock()
			}, nil
		}
		if b.waiters == nil {
			b.waiters = make(chan struct{})
		}
		waiters := b.waiters
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %w", ErrLockTimeout, ctx.Err())
		case <-waiters:
		}
	}
}

func (r *MemoryRepo) WithKegRead(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("fn required")
	}
	if boundaryMode(ctx, r) != 0 {
		return fn(ctx)
	}
	release, err := r.operationBoundary.acquire(ctx, false)
	if err != nil {
		return err
	}
	defer release()
	return fn(contextWithBoundary(ctx, r, kegBoundaryRead))
}

func (r *MemoryRepo) WithKegWrite(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("fn required")
	}
	switch boundaryMode(ctx, r) {
	case kegBoundaryWrite:
		return fn(ctx)
	case kegBoundaryRead:
		return ErrKegLockUpgrade
	}
	release, err := r.operationBoundary.acquire(ctx, true)
	if err != nil {
		return err
	}
	defer release()
	err = fn(contextWithBoundary(ctx, r, kegBoundaryWrite))
	// Advance even on callback failure: low-level repository callers can leave
	// partial state, and conservative cache invalidation is safer than assuming
	// every failed operation restored itself perfectly.
	r.operationGeneration.Add(1)
	return err
}

func (r *MemoryRepo) kegOperationGeneration() uint64 {
	return r.operationGeneration.Load()
}

func (f *FsRepo) WithKegRead(ctx context.Context, fn func(context.Context) error) error {
	return f.withKegOperation(ctx, kegBoundaryRead, fn)
}

func (f *FsRepo) WithKegWrite(ctx context.Context, fn func(context.Context) error) error {
	return f.withKegOperation(ctx, kegBoundaryWrite, fn)
}

// withKegOperation deliberately uses the same exclusive root lock for reads
// and writes. That is the conservative filesystem guarantee: a multi-file
// aggregate read cannot observe half of a concurrent write, including a write
// performed by another process.
func (f *FsRepo) withKegOperation(ctx context.Context, mode kegBoundaryMode, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("fn required")
	}
	switch held := boundaryMode(ctx, f); {
	case held == kegBoundaryWrite:
		return fn(ctx)
	case held == kegBoundaryRead && mode == kegBoundaryRead:
		return fn(ctx)
	case held == kegBoundaryRead && mode == kegBoundaryWrite:
		return ErrKegLockUpgrade
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrLockTimeout, err)
	}
	if err := f.runtime.Mkdir(f.Root, 0o755, true); err != nil {
		return errors.Join(ErrLock, NewBackendError(f.Name(), "WithKegOperation", 0, err, false))
	}
	lockPath := filepath.Join(f.Root, KegOperationLock)
	for {
		err := f.runtime.Mkdir(lockPath, 0o700, false)
		if err == nil {
			f.writeLockMetadata(lockPath)
			break
		}
		if os.IsExist(err) {
			if f.isLockStale(lockMetadataPath(lockPath)) {
				_ = f.runtime.Remove(lockPath, true)
				continue
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("%w: %w", ErrLockTimeout, ctx.Err())
			case <-time.After(25 * time.Millisecond):
			}
			continue
		}
		return errors.Join(ErrLock, NewBackendError(f.Name(), "WithKegOperation", 0, err, false))
	}

	runErr := fn(contextWithBoundary(ctx, f, mode))
	unlockErr := f.runtime.Remove(lockPath, true)
	if unlockErr != nil && !os.IsNotExist(unlockErr) {
		unlockErr = errors.Join(ErrLock, NewBackendError(f.Name(), "WithKegOperationUnlock", 0, unlockErr, false))
	} else {
		unlockErr = nil
	}
	return errors.Join(runErr, unlockErr)
}
