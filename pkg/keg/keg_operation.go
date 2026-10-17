package keg

import (
	"context"
	"errors"
	"time"
)

type localKegOperationContextKey struct{}

type lockWaitContextKey struct{}

// touchLockWait bounds how long a read that records an access waits for the
// keg write lock. The access is bookkeeping, so a read behind a long writer
// gives it up and serves the content rather than queue for the lock.
const touchLockWait = 2 * time.Second

// WithLockWait returns ctx carrying a bound on how long a repository waits to
// acquire the keg write lock. A repository that honors it fails with
// ErrLockTimeout once d passes without the lock; the bound covers acquiring
// the lock, not the work done under it. A d of zero or less leaves the wait
// unbounded, which is the default.
//
// The bound is advisory. It lives only in ctx and is not part of the Hub API,
// so it never crosses the wire: it applies only where the Repository runs in
// the same process as the caller and reads it with LockWaitFrom. A Repository
// that does not, the in-memory test repository included, waits as it always
// has, and a touched read there waits for the lock instead of skipping the
// touch. Callers must not rely on the bound for correctness, only to keep a
// read from queueing behind a long writer where it is honored.
func WithLockWait(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, lockWaitContextKey{}, d)
}

// LockWaitFrom returns the keg lock wait bound carried by ctx, or zero when
// the wait is unbounded. Repository implementations call it when they acquire
// the keg write lock; see WithLockWait for which ones do.
func LockWaitFrom(ctx context.Context) time.Duration {
	d, _ := ctx.Value(lockWaitContextKey{}).(time.Duration)
	if d < 0 {
		return 0
	}
	return d
}

// touchLockContext bounds the keg lock wait of a touched read, keeping any
// bound the caller already set.
func touchLockContext(ctx context.Context) context.Context {
	if LockWaitFrom(ctx) > 0 {
		return ctx
	}
	return WithLockWait(ctx, touchLockWait)
}

// touchLockBusy reports whether a touched read failed only because the keg
// lock stayed busy, so the read can be served without the touch. A canceled
// or expired ctx also surfaces as ErrLockTimeout; that read fails as it is.
func touchLockBusy(ctx context.Context, err error) bool {
	return errors.Is(err, ErrLockTimeout) && ctx.Err() == nil
}

func localKegOperationActive(ctx context.Context, k *LocalKeg) bool {
	held, _ := ctx.Value(localKegOperationContextKey{}).(*LocalKeg)
	return held == k
}

func (k *LocalKeg) enterLocalKegOperation(ctx context.Context, invalidate bool, fn func(context.Context) error) error {
	if localKegOperationActive(ctx, k) {
		return fn(ctx)
	}
	// The repository boundary serializes writers across LocalKeg instances,
	// but each instance owns its own in-memory Dex cache. Reload at the outer
	// operation boundary so an instance cannot publish a generation based on a
	// cache populated before another instance's completed operation.
	if invalidate {
		k.InvalidateDex()
	}
	return fn(context.WithValue(ctx, localKegOperationContextKey{}, k))
}

func (k *LocalKeg) withKegRead(ctx context.Context, fn func(context.Context) error) error {
	return k.Repo.WithKegRead(ctx, func(opCtx context.Context) error {
		return k.enterLocalKegOperation(opCtx, false, fn)
	})
}

func (k *LocalKeg) withKegWrite(ctx context.Context, fn func(context.Context) error) error {
	return k.Repo.WithKegWrite(ctx, func(opCtx context.Context) error {
		return k.enterLocalKegOperation(opCtx, true, fn)
	})
}

func (k *LocalKeg) withKegAtomicWrite(ctx context.Context, fn func(context.Context) error) error {
	var err error
	if atomic, ok := k.Repo.(RepositoryAtomicWrite); ok {
		err = atomic.WithKegAtomicWrite(ctx, func(opCtx context.Context) error {
			return k.enterLocalKegOperation(opCtx, true, fn)
		})
	} else {
		// Transactional backends such as PgRepo make their ordinary write
		// boundary atomic, so the fallback preserves their native transaction.
		err = k.withKegWrite(ctx, fn)
	}
	if err != nil {
		// The repository rolled canonical and generated files back. Discard any
		// in-memory dex generation that the failed callback may have built.
		k.InvalidateDex()
	}
	return err
}

func withKegAtomicWriteValue[T any](ctx context.Context, k *LocalKeg, fn func(context.Context) (T, error)) (T, error) {
	var out T
	err := k.withKegAtomicWrite(ctx, func(opCtx context.Context) error {
		var err error
		out, err = fn(opCtx)
		return err
	})
	return out, err
}

func withKegReadValue[T any](ctx context.Context, k *LocalKeg, fn func(context.Context) (T, error)) (T, error) {
	var out T
	err := k.withKegRead(ctx, func(opCtx context.Context) error {
		var err error
		out, err = fn(opCtx)
		return err
	})
	return out, err
}

func withKegWriteValue[T any](ctx context.Context, k *LocalKeg, fn func(context.Context) (T, error)) (T, error) {
	var out T
	err := k.withKegWrite(ctx, func(opCtx context.Context) error {
		var err error
		out, err = fn(opCtx)
		return err
	})
	return out, err
}
