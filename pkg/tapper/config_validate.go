package tapper

import (
	"fmt"
	"regexp"
	"strings"
)

// ConfigWarning represents a semantic issue found during config validation.
type ConfigWarning struct {
	Field   string // config field name (e.g., "kegMap[0]", "logLevel")
	Message string // human-readable description
}

// validLogLevels lists accepted log level strings.
var validLogLevels = map[string]struct{}{
	"debug": {},
	"info":  {},
	"warn":  {},
	"error": {},
}

// ValidateConfig checks a Config for semantic issues that are valid YAML but
// likely mistakes. It returns warnings, not errors — the config is still usable.
func ValidateConfig(cfg *Config) []ConfigWarning {
	if cfg == nil || cfg.data == nil {
		return nil
	}

	var warnings []ConfigWarning

	// Check logLevel is a recognized value.
	if lvl := cfg.data.LogLevel; lvl != "" {
		if _, ok := validLogLevels[strings.ToLower(lvl)]; !ok {
			warnings = append(warnings, ConfigWarning{
				Field:   "logLevel",
				Message: fmt.Sprintf("unrecognized log level %q (expected debug, info, warn, or error)", lvl),
			})
		}
	}

	// Check kegMap entries have at least one pattern.
	for i, entry := range cfg.data.KegMap {
		if entry.PathPrefix == "" && entry.PathRegex == "" {
			warnings = append(warnings, ConfigWarning{
				Field:   fmt.Sprintf("kegMap[%d]", i),
				Message: fmt.Sprintf("kegMap entry for alias %q has no pathPrefix or pathRegex", entry.Alias),
			})
		}
		if entry.Alias == "" {
			warnings = append(warnings, ConfigWarning{
				Field:   fmt.Sprintf("kegMap[%d]", i),
				Message: "kegMap entry has no alias",
			})
		}
		// Check pathRegex compiles.
		if entry.PathRegex != "" {
			if _, err := regexp.Compile(entry.PathRegex); err != nil {
				warnings = append(warnings, ConfigWarning{
					Field:   fmt.Sprintf("kegMap[%d].pathRegex", i),
					Message: fmt.Sprintf("invalid regex for alias %q: %v", entry.Alias, err),
				})
			}
		}
	}

	// Check for duplicate aliases in kegMap pointing to the same pattern.
	type kegMapKey struct {
		alias, prefix, regex string
	}
	seen := make(map[kegMapKey]int)
	for i, entry := range cfg.data.KegMap {
		key := kegMapKey{entry.Alias, entry.PathPrefix, entry.PathRegex}
		if prev, ok := seen[key]; ok {
			warnings = append(warnings, ConfigWarning{
				Field:   fmt.Sprintf("kegMap[%d]", i),
				Message: fmt.Sprintf("duplicate kegMap entry (same as index %d): alias=%q", prev, entry.Alias),
			})
		}
		seen[key] = i
	}

	return warnings
}
