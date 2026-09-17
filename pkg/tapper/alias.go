package tapper

import (
	"fmt"
	"regexp"

	"github.com/jlrickert/tapper/pkg/keg"
)

// refSegmentPattern is the canonical shape of one segment of an @namespace/keg
// reference. Hub is the source of truth (catalogrepo.ValidAlias): it applies
// one pattern to namespaces and aliases alike and rejects everything else when
// a KEG is created, so a single pattern here is what keeps a reference that
// parses from failing server-side.
//
// The pattern admits a trailing hyphen because Hub admits one. A client
// stricter than the server would reject references the server accepts, which
// is the worse direction to be wrong in.
var refSegmentPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// segmentShape describes refSegmentPattern for humans, matching the wording
// Hub uses when it rejects the same value.
const segmentShape = "must be 1-64 lowercase alphanumeric characters or hyphens, starting with alphanumeric"

// ValidateKegAlias returns nil when alias matches the canonical segment shape
// and a wrapped keg.ErrInvalid otherwise. Empty input is rejected explicitly
// so callers can distinguish missing-alias errors from shape errors when
// reading the wrapped chain.
func ValidateKegAlias(alias string) error {
	if alias == "" {
		return fmt.Errorf("keg alias is required: %w", keg.ErrInvalid)
	}
	if !refSegmentPattern.MatchString(alias) {
		return fmt.Errorf("invalid keg alias %q: %s: %w", alias, segmentShape, keg.ErrInvalid)
	}
	return nil
}

// ValidateNamespace returns nil when ns is a legal namespace segment and a
// wrapped keg.ErrInvalid otherwise. Empty input is rejected explicitly. The "@"
// sigil is never part of the stored value; pass the bare namespace.
func ValidateNamespace(ns string) error {
	if ns == "" {
		return fmt.Errorf("namespace is required: %w", keg.ErrInvalid)
	}
	if !refSegmentPattern.MatchString(ns) {
		return fmt.Errorf("invalid namespace %q: %s (no dots or slashes): %w", ns, segmentShape, keg.ErrInvalid)
	}
	return nil
}
