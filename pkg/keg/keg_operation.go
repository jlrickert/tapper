package keg

import "context"

type localKegOperationContextKey struct{}

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
