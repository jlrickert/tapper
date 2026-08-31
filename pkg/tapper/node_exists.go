package tapper

import (
	"context"
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// describeKeg renders a keg's identity for error messages: its canonical
// reference (keg:@ns/name) and the hub URL it
// reads from. It lets an otherwise opaque "node N not found" name the hub,
// namespace, and keg that were actually consulted. Returns a generic phrase when
// the keg has no resolved target (e.g. an in-memory keg in tests).
func describeKeg(k keg.Keg) string {
	if k == nil || k.Target() == nil {
		return "the resolved keg"
	}
	ref := k.Target().String()
	if u := strings.TrimSpace(k.Target().HubURL); u != "" {
		return fmt.Sprintf("%s (hub %s)", ref, u)
	}
	return ref
}

// nodeExistsWithContent reports whether the Hub exposes a complete node.
//
// nodeExistsWithContent does NOT hold any node lock — it is safe to call
// from pre-lock gates. The authoritative under-lock check lives in
// pkg/keg.Keg and runs again inside operations that mutate the node.
func (t *Tap) nodeExistsWithContent(ctx context.Context, k keg.Keg, id keg.NodeId) (bool, error) {
	return k.NodeExists(ctx, id)
}
