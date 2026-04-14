package tapper

import (
	"context"
	"errors"

	"github.com/jlrickert/tapper/pkg/keg"
)

// nodeExistsWithContent reports whether the given node is a real node with
// content (README.md present), as opposed to a bare shadow-reservation
// directory left behind by FsRepo.Next() or FsRepo.WithNodeLock().
//
// Repository.HasNode returns true for any existing directory, which is
// unsuitable as a load-bearing existence gate at the Tap layer: an empty
// directory produced as a lock or allocation artifact would pass. Callers
// that need to authenticate against a fully-written node must use this
// helper instead.
//
// The check is performed by attempting to read the node's content file; if
// the repository reports ErrNotExist (either because the directory is
// missing or because README.md has not been written), the node is reported
// as absent and no error is returned.
//
// nodeExistsWithContent does NOT hold any node lock — it is safe to call
// from pre-lock gates. The authoritative under-lock check lives in
// pkg/keg.Keg and runs again inside operations that mutate the node.
//
// Promoted from pkg/keg.Keg.nodeExistsWithContent to resolve F2/F3/F11 of
// report node 842.
func (t *Tap) nodeExistsWithContent(ctx context.Context, k *keg.Keg, id keg.NodeId) (bool, error) {
	_, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
