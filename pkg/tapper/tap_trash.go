package tapper

import (
	"context"
	"fmt"

	"github.com/jlrickert/tapper/pkg/keg"
)

// TrashList returns the keg's removed nodes, newest first.
func (t *Tap) TrashList(ctx context.Context, opts KegTargetOptions) ([]keg.TrashEntry, error) {
	k, err := t.resolveKegForRole(ctx, opts, FlightRoleViewer)
	if err != nil {
		return nil, err
	}
	return k.ListTrash(ctx)
}

type TrashRestoreOptions struct {
	KegTargetOptions
	NodeIDs []string
}

// TrashRestore puts trashed nodes back at their ids, all or nothing.
func (t *Tap) TrashRestore(ctx context.Context, opts TrashRestoreOptions) error {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return err
	}
	ids := make([]keg.NodeId, len(opts.NodeIDs))
	for i, raw := range opts.NodeIDs {
		id, err := keg.ParseNode(raw)
		if err != nil {
			return fmt.Errorf("restore %d node %q: %w", i, raw, err)
		}
		ids[i] = *id
	}
	return k.RestoreTrashed(ctx, ids)
}
