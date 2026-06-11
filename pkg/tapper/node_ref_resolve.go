package tapper

import (
	"context"
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// RefContext carries the "current keg" so a node reference can resolve a
// keg-local alias against that keg's Links table and imply the hub for a
// qualified reference from the current keg's hub.
type RefContext struct {
	CurrentKeg *keg.LocalKeg
}

// ResolveNodeRef resolves a parsed node reference into the keg that holds it and
// the numeric node id within that keg:
//
//   - RefLocal:     the current keg, with the bare node id.
//   - RefAlias:     the alias is resolved against the current keg's Links table
//     first (so authored links travel with the keg), then the
//     tap-config kegs map.
//   - RefQualified: a (hub, namespace, keg) reference whose hub is implied from
//     the current keg's hub; the reserved @local namespace pins
//     the local hub regardless of the current keg's hub.
func (t *Tap) ResolveNodeRef(ctx context.Context, ref *keg.NodeRef, rc RefContext) (*keg.LocalKeg, keg.NodeId, error) {
	if ref == nil {
		return nil, keg.NodeId{}, fmt.Errorf("nil node ref")
	}
	// The node id within the owning keg never carries the addressing alias.
	node := keg.NodeId{ID: ref.Node.ID, Code: ref.Node.Code}

	switch ref.Form {
	case keg.RefLocal:
		if rc.CurrentKeg == nil {
			return nil, keg.NodeId{}, fmt.Errorf("local node ref %q has no current keg", ref.String())
		}
		return rc.CurrentKeg, node, nil

	case keg.RefAlias:
		target, err := t.resolveRefAlias(ctx, ref.Alias, rc)
		if err != nil {
			return nil, keg.NodeId{}, err
		}
		k, err := t.openTarget(ctx, target)
		if err != nil {
			return nil, keg.NodeId{}, err
		}
		return k, node, nil

	case keg.RefQualified:
		// @local pins the local hub; any other namespace implies the current
		// keg's hub from context.
		hub := ""
		if ref.Namespace != LocalHubName && rc.CurrentKeg != nil && rc.CurrentKeg.Target != nil {
			hub = strings.TrimSpace(rc.CurrentKeg.Target.Hub)
		}
		cfg, err := t.ConfigService.Config(true)
		if err != nil {
			return nil, keg.NodeId{}, err
		}
		target, err := cfg.ResolveRef(t.Runtime, KegRef{Hub: hub, Namespace: ref.Namespace, Name: ref.KegName})
		if err != nil {
			return nil, keg.NodeId{}, fmt.Errorf("resolve %q: %w", ref.String(), err)
		}
		k, err := t.openTarget(ctx, target)
		if err != nil {
			return nil, keg.NodeId{}, err
		}
		return k, node, nil

	default:
		return nil, keg.NodeId{}, fmt.Errorf("unknown node ref form")
	}
}

// resolveNodeArg resolves a raw NODE_ID command argument into the keg that owns
// it and the numeric node id within that keg. It is the single choke point that
// gives every command that accepts a node argument cross-keg reach:
//
//   - "<id>" / "<id>-<code>"        operate on currentKeg (unchanged behavior).
//   - "keg:<alias>/<id>"            redirect to the keg the alias names (the
//     current keg's Links table first, then tap-config kegs).
//   - "keg:@<ns>/<keg>/<id>"        redirect to the fully qualified keg; the hub
//     is implied from currentKeg's hub, @local pins the local hub.
//
// currentKeg is the keg already resolved by the caller (via resolveKeg); it
// supplies the RefLocal target and the context a relative ref resolves against.
// The returned keg is the one the operation must run on, so a redirected ref
// reads/writes the right keg rather than silently acting on currentKeg.
func (t *Tap) resolveNodeArg(ctx context.Context, currentKeg *keg.LocalKeg, raw string) (*keg.LocalKeg, keg.NodeId, error) {
	ref, err := keg.ParseNodeRef(raw)
	if err != nil {
		return nil, keg.NodeId{}, fmt.Errorf("invalid node ID %q: %w", raw, err)
	}
	return t.ResolveNodeRef(ctx, ref, RefContext{CurrentKeg: currentKeg})
}

// resolveRefAlias resolves an alias to a keg target: the current keg's Links
// table first (so authored links travel with the keg), then the tap-config kegs
// map.
func (t *Tap) resolveRefAlias(ctx context.Context, alias string, rc RefContext) (*keg.Target, error) {
	if rc.CurrentKeg != nil {
		if kc, err := rc.CurrentKeg.Config(ctx); err == nil && kc != nil {
			if target, err := kc.ResolveAlias(alias); err == nil {
				return target, nil
			}
		}
	}
	cfg, err := t.ConfigService.Config(true)
	if err != nil {
		return nil, err
	}
	target, err := cfg.ResolveAlias(t.Runtime, alias)
	if err != nil {
		return nil, fmt.Errorf("unknown keg alias %q: %w", alias, keg.ErrNotExist)
	}
	return target, nil
}

// openTarget opens a keg at the resolved target using the shared token resolver.
func (t *Tap) openTarget(ctx context.Context, target *keg.Target) (*keg.LocalKeg, error) {
	return keg.NewKegFromTarget(ctx, *target, t.Runtime, keg.WithTokenResolver(t.KegService.tokenResolver()))
}
