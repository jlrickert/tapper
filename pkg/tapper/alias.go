package tapper

import (
	"fmt"
	"regexp"

	"github.com/jlrickert/tapper/pkg/keg"
)

// kegAliasPattern restricts keg aliases to a portable, filesystem-safe shape.
// Lowercase letters, digits, hyphen, and underscore — no dots, slashes,
// whitespace, or case variants that differ across platforms (HFS+, FAT32).
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
