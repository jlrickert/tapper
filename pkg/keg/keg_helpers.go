package keg

import (
	"context"
	"fmt"
	"time"
)

// UpdateConfig applies f to the keg's configuration via a read-then-set over
// the Keg interface. Unlike LocalKeg.UpdateConfig this is not atomic — a
// concurrent writer between the read and the set is lost — which is an
// accepted trade-off for rare admin operations over remote kegs.
func UpdateConfig(ctx context.Context, k Keg, f func(*Config)) error {
	cfg, err := k.Config(ctx)
	if err != nil {
		return fmt.Errorf("unable to read keg config: %w", err)
	}
	f(cfg)
	return k.SetConfig(ctx, []byte(cfg.String()))
}

// UpdateMeta applies f to a node's metadata via read-then-set over the Keg
// interface. Not atomic across concurrent writers; see UpdateConfig.
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
