package keg

import (
	"context"
	"fmt"
	"time"
)

// UpdateSettings applies f to the keg's configuration via a read-then-set over
// the Keg interface. Unlike LocalKeg.UpdateSettings this is not atomic — a
// concurrent writer between the read and the set is lost — which is an
// accepted trade-off for rare admin operations over remote kegs.
func UpdateSettings(ctx context.Context, k Keg, f func(*Settings)) error {
	cfg, err := k.Settings(ctx)
	if err != nil {
		return fmt.Errorf("unable to read keg settings: %w", err)
	}
	f(cfg)
	return k.SetSettings(ctx, []byte(cfg.String()), SettingsWriteOptions{ExpectedHash: cfg.Hash()})
}

// UpdateMeta applies f to a node's metadata via read-then-set over the Keg
// interface. Not atomic across concurrent writers; see UpdateSettings.
func UpdateMeta(ctx context.Context, k Keg, id NodeId, f func(*NodeMeta)) error {
	meta, err := k.GetMeta(ctx, id)
	if err != nil {
		return err
	}
	if meta == nil {
		meta = NewMeta(ctx, time.Time{})
	}
	f(meta)
	return k.SetMeta(ctx, id, meta)
}
