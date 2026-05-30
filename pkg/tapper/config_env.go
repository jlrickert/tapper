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
	"DEFAULT_HUB",
	"FALLBACK_HUB",
	"DEFAULT_NAMESPACE",
	"FALLBACK_NAMESPACE",
	"DISABLE_DEFAULT_HUB",
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
	if v, ok := envMap["default_hub"]; ok {
		cfg.data.DefaultHub = v
	}
	if v, ok := envMap["fallback_hub"]; ok {
		cfg.data.FallbackHub = v
	}
	if v, ok := envMap["default_namespace"]; ok {
		cfg.data.DefaultNamespace = v
	}
	if v, ok := envMap["fallback_namespace"]; ok {
		cfg.data.FallbackNamespace = v
	}
	if v, ok := envMap["disable_default_hub"]; ok {
		cfg.data.DisableDefaultHub = parseEnvBool(v)
	}

	return cfg
}

// parseEnvBool maps the conventional shell truthy values to true. Anything
// else, including the empty string, is false. Kept private so the truthy
// vocabulary is centralized — adding a new value is one edit, not a sweep
// across every TAP_*_BOOL env var.
func parseEnvBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
