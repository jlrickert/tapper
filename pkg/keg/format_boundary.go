package keg

import "context"

// WithReadBoundary runs fn inside a single keg read boundary when k has one.
//
// A local keg's read boundary is an exclusive lock, and every per-node read
// takes it. Batches of reads must therefore share one boundary rather than
// acquiring it per call: the boundary is re-entrant through the context, so
// nested reads inside fn short-circuit instead of relocking. Without this, a
// listing that reads metadata for N nodes performs 2N exclusive lock cycles and
// blocks every other process on the keg for the duration.
//
// A remote keg has no local boundary to hold, so fn runs directly.
func WithReadBoundary(ctx context.Context, k Keg, fn func(context.Context) error) error {
	local, ok := k.(*LocalKeg)
	if !ok || local == nil || local.Repo == nil {
		return fn(ctx)
	}
	return local.Repo.WithKegRead(ctx, fn)
}
