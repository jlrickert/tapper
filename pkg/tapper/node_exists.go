package tapper

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// describeKeg renders a keg's identity for error messages: its canonical
// reference (keg:@ns/name, or a file path) and, for a remote keg, the hub URL it
// reads from. It lets an otherwise opaque "node N not found" name the hub,
// namespace, and keg that were actually consulted. Returns a generic phrase when
// the keg has no resolved target (e.g. an in-memory keg in tests).
func describeKeg(k *keg.Keg) string {
	if k == nil || k.Target == nil {
		return "the resolved keg"
	}
	ref := k.Target.String()
	if u := strings.TrimSpace(k.Target.HubURL); u != "" {
		return fmt.Sprintf("%s (hub %s)", ref, u)
	}
	return ref
}

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
