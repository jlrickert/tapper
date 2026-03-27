package tapper

import (
	"strings"
)

// tapEnvVarKeys lists the TAP_* env var suffixes (without prefix) that the
// env provider checks. Each maps to a Config field.
var tapEnvVarKeys = []string{
	"DEFAULT_KEG",
	"FALLBACK_KEG",
	"LOG_FILE",
	"LOG_LEVEL",
	"DEFAULT_REGISTRY",
	"KEG_SEARCH_PATHS",
}

const tapEnvPrefix = "TAP_"

// configFromEnvMap builds a *Config from env var values returned by EnvProvider.
// The map keys are lowercased versions of the env var suffixes (e.g. "default_keg").
func configFromEnvMap(envMap map[string]string) *Config {
	if len(envMap) == 0 {
		return nil
	}

	cfg := &Config{data: &configDTO{}}

	if v, ok := envMap["default_keg"]; ok {
		cfg.data.DefaultKeg = v
	}
	if v, ok := envMap["fallback_keg"]; ok {
		cfg.data.FallbackKeg = v
	}
	if v, ok := envMap["log_file"]; ok {
		cfg.data.LogFile = v
	}
	if v, ok := envMap["log_level"]; ok {
		cfg.data.LogLevel = v
	}
	if v, ok := envMap["default_registry"]; ok {
		cfg.data.DefaultRegistry = v
	}
	if v, ok := envMap["keg_search_paths"]; ok {
		// Colon-separated on Unix.
		paths := strings.Split(v, ":")
		var filtered []string
		for _, p := range paths {
			if strings.TrimSpace(p) != "" {
				filtered = append(filtered, p)
			}
		}
		if len(filtered) > 0 {
			cfg.data.KegSearchPaths = stringList(filtered)
		}
	}

	return cfg
}
