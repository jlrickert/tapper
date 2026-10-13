package tapper

import (
	"context"

	"github.com/jlrickert/tapper/pkg/keg"
)

type ReplaceOptions struct {
	KegTargetOptions
	keg.ReplaceTextOptions
}

// Replace finds and replaces text across a keg. A dry run only reads, so it
// needs a viewer; a real one needs an editor.
func (t *Tap) Replace(ctx context.Context, opts ReplaceOptions) (keg.ReplaceTextResult, error) {
	role := FlightRoleEditor
	if opts.DryRun {
		role = FlightRoleViewer
	}
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, role)
	if err != nil {
		return keg.ReplaceTextResult{}, err
	}
	ctx = keg.WithDefaultValidationActor(ctx, keg.ValidationActorHuman)
	return k.ReplaceText(ctx, opts.ReplaceTextOptions)
}
