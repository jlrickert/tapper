package tapper

import (
	"fmt"
	"regexp"

	"github.com/jlrickert/tapper/pkg/keg"
)

// kegAliasPattern restricts keg aliases to the portable Hub route shape.
// Lowercase letters, digits, hyphen, and underscore are accepted; dots,
// slashes, whitespace, and uppercase variants are rejected.
var kegAliasPattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

// ValidateKegAlias returns nil when alias matches the canonical alias shape
// and a wrapped keg.ErrInvalid otherwise. Empty input is rejected explicitly
// so callers can distinguish missing-alias errors from shape errors when
// reading the wrapped chain.
func ValidateKegAlias(alias string) error {
	if alias == "" {
		return fmt.Errorf("keg alias is required: %w", keg.ErrInvalid)
	}
	if !kegAliasPattern.MatchString(alias) {
		return fmt.Errorf("invalid keg alias %q: must match %s: %w",
			alias, kegAliasPattern.String(), keg.ErrInvalid)
	}
	return nil
}

// namespacePattern restricts namespaces to a single Hub route segment:
// lowercase letters, digits, hyphen, and underscore. The leading sigil
// distinguishes a namespace from ordinary aliases in references and Hub routes.
var namespacePattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

// ValidateNamespace returns nil when ns is a legal namespace segment and a
// wrapped keg.ErrInvalid otherwise. Empty input is rejected explicitly. The "@"
// sigil is never part of the stored value; pass the bare namespace.
func ValidateNamespace(ns string) error {
	if ns == "" {
		return fmt.Errorf("namespace is required: %w", keg.ErrInvalid)
	}
	if !namespacePattern.MatchString(ns) {
		return fmt.Errorf("invalid namespace %q: must match %s (no dots or slashes): %w",
			ns, namespacePattern.String(), keg.ErrInvalid)
	}
	return nil
}
