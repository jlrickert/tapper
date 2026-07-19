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
