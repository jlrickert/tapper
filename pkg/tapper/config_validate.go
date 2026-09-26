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

// RetiredConfigFields reports keys a config file still carries that Tapper no
// longer reads. They survive rewrites as unknown data, so nothing is lost,
// but they do nothing. It needs a config read from one file: a merged config
// keeps no document to inspect.
func RetiredConfigFields(cfg *Config) []ConfigWarning {
	if cfg == nil {
		return nil
	}
	var warnings []ConfigWarning
	// agent and agents configured `tap launch` before it moved to Hub models.
	for _, field := range []string{"agent", "agents"} {
		if _, ok := mappingValue(mappingNode(cfg.doc), field); ok {
			warnings = append(warnings, ConfigWarning{
				Field:   field,
				Message: "no longer used: tap launch starts harnesses on Hub catalog models (tap launch HARNESS --model ID); remove it",
			})
		}
	}
	return warnings
}

// ValidateConfig checks a Config for semantic issues that are valid YAML but
// likely mistakes. It returns warnings, not errors — the config is still usable.
func ValidateConfig(cfg *Config) []ConfigWarning {
	if cfg == nil || cfg.data == nil {
		return nil
	}

	warnings := RetiredConfigFields(cfg)

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
		if entry.retiredOnly() {
			continue
		}
		if entry.PathPrefix == "" && entry.PathRegex == "" {
			warnings = append(warnings, ConfigWarning{
				Field:   fmt.Sprintf("kegMap[%d]", i),
				Message: "kegMap defaults have no pathPrefix or pathRegex",
			})
		}
		if entry.Keg == "" && entry.Hub == "" && entry.Flight == "" {
			warnings = append(warnings, ConfigWarning{
				Field:   fmt.Sprintf("kegMap[%d]", i),
				Message: "kegMap entry has no keg, hub, or flight",
			})
		}
		// Check pathRegex compiles.
		if entry.PathRegex != "" {
			if _, err := regexp.Compile(entry.PathRegex); err != nil {
				warnings = append(warnings, ConfigWarning{
					Field:   fmt.Sprintf("kegMap[%d].pathRegex", i),
					Message: fmt.Sprintf("invalid regex for kegMap defaults: %v", err),
				})
			}
		}
	}

	// Check for duplicate KEG, Hub, and flight defaults for the same pattern.
	type kegMapKey struct {
		keg, hub, flight, prefix, regex string
	}
	seen := make(map[kegMapKey]int)
	for i, entry := range cfg.data.KegMap {
		if entry.retiredOnly() {
			continue
		}
		key := kegMapKey{entry.Keg, entry.Hub, entry.Flight, entry.PathPrefix, entry.PathRegex}
		if prev, ok := seen[key]; ok {
			warnings = append(warnings, ConfigWarning{
				Field:   fmt.Sprintf("kegMap[%d]", i),
				Message: fmt.Sprintf("duplicate kegMap entry (same as index %d): keg=%q hub=%q flight=%q", prev, entry.Keg, entry.Hub, entry.Flight),
			})
		}
		seen[key] = i
	}

	return warnings
}
