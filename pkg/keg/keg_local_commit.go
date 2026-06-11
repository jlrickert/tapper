package keg

import (
	"context"
	"fmt"
)

// Commit finalizes a temporary node by allocating a permanent ID and moving it
// from its temporary location (with Code suffix) to the canonical numeric ID.
// For nodes without a Code (already permanent), Commit is a no-op.
func (k *LocalKeg) Commit(ctx context.Context, id NodeId) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to commit node: %w", err)
	}

	// only commit when Code is present (temporary id)
	if id.Code == "" {
		return nil
	}
	dst, err := k.Repo.Next(ctx)
	if err != nil {
		return err
	}
	if err := k.Repo.MoveNode(ctx, id, dst); err != nil {
		return err
	}
	return nil
}
