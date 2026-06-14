package tapper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

type ConfigOptions struct {
	// Project indicates whether to display project config
	Project bool

	// User indicates whether to display user config
	User bool

	// ConfigPath directly selects a config file to display.
	ConfigPath string
}

// Config displays the merged or project configuration.
func (t *Tap) Config(opts ConfigOptions) (string, error) {
	if err := validateConfigSelection(opts.ConfigPath, opts.Project, opts.User); err != nil {
		return "", err
	}

	var cfg *Config
	if opts.ConfigPath != "" {
		raw, err := t.Runtime.ReadFile(opts.ConfigPath)
		if err != nil {
			return "", err
		}
		if _, err := ParseConfig(raw); err != nil {
			return "", err
		}
		return string(raw), nil
	} else if opts.Project {
		lCfg, err := t.ConfigService.ProjectConfig(false)
		if err != nil {
			return "", err
		}
		cfg = lCfg
	} else if opts.User {
		uCfg, err := t.ConfigService.UserConfig(false)
		if err != nil {
			return "", err
		}
		cfg = uCfg
	} else {
		var err error
		cfg, err = t.ConfigService.Config(true)
		if err != nil {
			return "", err
		}
	}

	data, err := cfg.ToYAML()
	if err != nil {
		return "", fmt.Errorf("unable to serialize config: %w", err)
	}

	return string(data), nil
}

// ConfigEditOptions configures behavior for Tap.ConfigEdit.
type ConfigEditOptions struct {
	// Project indicates whether to edit local config instead of user config
	Project bool

	User bool

	ConfigPath string

	Stream *toolkit.Stream
}

type ConfigTemplateOptions struct {
	Project bool
}

func validateConfigSelection(configPath string, project, user bool) error {
	switch {
	case project && user:
		return fmt.Errorf("--user and --project cannot be combined")
	case configPath != "" && (project || user):
		return fmt.Errorf("--config cannot be combined with --user or --project")
	default:
		return nil
	}
}

// ConfigTemplate returns starter YAML for either user or project config.
func (t *Tap) ConfigTemplate(opts ConfigTemplateOptions) (string, error) {
	var cfg *Config
	if opts.Project {
		cfg = DefaultProjectConfig("project", "kegs")
	} else {
		cfg = DefaultUserConfig("pub", defaultTemplateKegRoot(t.Runtime))
	}
	data, err := cfg.ToYAML()
	return string(data), err
}

// ConfigEdit edits the selected tap config file.
//
// If stdin is piped with non-empty content, the piped YAML is validated and
// written directly without opening an editor. Otherwise the file is opened in
// the configured editor.
func (t *Tap) ConfigEdit(ctx context.Context, opts ConfigEditOptions) error {
	if err := validateConfigSelection(opts.ConfigPath, opts.Project, opts.User); err != nil {
		return err
	}

	var configPath string
	if opts.ConfigPath != "" {
		configPath = opts.ConfigPath
	} else if opts.User {
		configPath = t.PathService.UserConfig()
	} else {
		// Default (and explicit --project) edit the project config.
		configPath = t.PathService.ProjectConfig()
	}

	resolvedPath, err := t.Runtime.ResolvePath(configPath, false)
	if err != nil {
		return fmt.Errorf("unable to resolve config path: %w", err)
	}

	// If config doesn't exist, create a default one.
	if _, err := t.Runtime.ReadFile(resolvedPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("unable to inspect config file: %w", err)
		}
		if opts.User {
			// User config is the base layer — seed it with real onboarding
			// defaults (hubs, local namespace, etc.).
			cfg := DefaultUserConfig("public", defaultTemplateKegRoot(t.Runtime))
			if err := cfg.Write(t.Runtime, resolvedPath); err != nil {
				return fmt.Errorf("unable to create default config: %w", err)
			}
		} else {
			// Project config: seed a fully commented template so an abandoned
			// edit leaves an inert file rather than authoritative default* slots
			// that would silently override user-level resolution.
			tmpl, tmplErr := projectConfigTemplate()
			if tmplErr != nil {
				return tmplErr
			}
			if err := t.Runtime.AtomicWriteFile(resolvedPath, tmpl, 0o644); err != nil {
				return fmt.Errorf("unable to create default config: %w", err)
			}
		}
	}

	originalRaw, err := t.Runtime.ReadFile(resolvedPath)
	if err != nil {
		return fmt.Errorf("unable to read config file: %w", err)
	}

	saveConfig := func(data []byte) error {
		if err := t.Runtime.AtomicWriteFile(resolvedPath, data, 0o644); err != nil {
			return fmt.Errorf("unable to save edited config: %w", err)
		}
		return nil
	}

	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			if bytes.Equal(pipedRaw, originalRaw) {
				return nil
			}
			if _, parseErr := ParseConfig(pipedRaw); parseErr != nil {
				return fmt.Errorf("tap config from stdin is invalid: %w", parseErr)
			}
			return saveConfig(pipedRaw)
		}
	}

	if err := editWithLiveSaves(ctx, t.Runtime, resolvedPath, nil, func(editedRaw []byte) error {
		if _, err := ParseConfig(editedRaw); err != nil {
			return fmt.Errorf("tap config is invalid after editing: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("unable to edit tap config: %w", err)
	}
	return nil
}

// ConfigExplainResult describes the provenance of a single config field.
type ConfigExplainResult struct {
	Field  string // field name (e.g. "defaultKeg")
	Value  string // resolved value in the merged config
	Source string // which provider set this value ("user config", "project config", "env vars", "default")
}

// ConfigExplainOptions configures behavior for Tap.ConfigExplain.
type ConfigExplainOptions struct {
	// Field limits the result to a single field. Empty means all fields.
	Field string
}

// ConfigExplainFields lists the scalar config fields eligible for explain.
var ConfigExplainFields = []string{
	"defaultKeg",
	"fallbackKeg",
	"logFile",
	"logLevel",
	"defaultHub",
	"fallbackHub",
	"defaultNamespace",
	"fallbackNamespace",
	"disableDefaultHub",
}

// configFieldGetter returns the string value of a named field from a Config.
// Boolean fields render as "true" when set and "" when zero so the env-var
// cascade (which uses non-empty as the "set by this tier" signal) works
// without a parallel "is-set" predicate.
func configFieldGetter(cfg *Config, field string) string {
	if cfg == nil {
		return ""
	}
	switch field {
	case "defaultKeg":
		return cfg.DefaultKeg()
	case "fallbackKeg":
		return cfg.FallbackKeg()
	case "logFile":
		return cfg.LogFile()
	case "logLevel":
		return cfg.LogLevel()
	case "defaultHub":
		return cfg.DefaultHub()
	case "fallbackHub":
		return cfg.FallbackHub()
	case "defaultNamespace":
		return cfg.DefaultNamespace()
	case "fallbackNamespace":
		return cfg.FallbackNamespace()
	case "disableDefaultHub":
		if cfg.DisableDefaultHub() {
			return "true"
		}
		return ""
	default:
		return ""
	}
}

// ConfigExplain returns provenance for config fields, showing which source set
// each value. It loads each tier individually and walks from most-specific to
// least-specific to determine the effective source.
func (t *Tap) ConfigExplain(ctx context.Context, opts ConfigExplainOptions) ([]ConfigExplainResult, error) {
	// Load the merged config to get final values.
	merged, err := t.ConfigService.Config(true)
	if err != nil {
		return nil, fmt.Errorf("unable to load merged config: %w", err)
	}

	// Load each tier individually. Missing configs are nil (not errors).
	userCfg, _ := t.ConfigService.UserConfig(true)
	projectCfg, _ := t.ConfigService.ProjectConfig(true)

	// Build env config by checking TAP_* env vars.
	envCfg := t.loadEnvConfig()

	fields := ConfigExplainFields
	if opts.Field != "" {
		found := false
		for _, f := range ConfigExplainFields {
			if f == opts.Field {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("unknown config field %q; valid fields: %s", opts.Field, strings.Join(ConfigExplainFields, ", "))
		}
		fields = []string{opts.Field}
	}

	var results []ConfigExplainResult
	for _, field := range fields {
		mergedVal := configFieldGetter(merged, field)

		// Walk from most-specific to least-specific to find which source set this value.
		source := "default"
		if envVal := configFieldGetter(envCfg, field); envVal != "" {
			source = "env vars"
		} else if projVal := configFieldGetter(projectCfg, field); projVal != "" {
			source = "project config"
		} else if userVal := configFieldGetter(userCfg, field); userVal != "" {
			source = "user config"
		}

		results = append(results, ConfigExplainResult{
			Field:  field,
			Value:  mergedVal,
			Source: source,
		})
	}

	return results, nil
}

// loadEnvConfig builds a Config from TAP_* env vars, or returns nil if none are set.
func (t *Tap) loadEnvConfig() *Config {
	getenv := t.Runtime.Env().Get
	envMap := make(map[string]string)
	for _, key := range tapEnvVarKeys {
		val := getenv(tapEnvPrefix + key)
		if val != "" {
			envMap[strings.ToLower(key)] = val
		}
	}
	return configFromEnvMap(envMap)
}

// defaultTemplateKegRoot returns the user-visible default basePath written into
// a starter config's local hub (~/Documents/kegs). It is distinct from
// defaultUserKegRoot, the platform data-dir fallback used at resolve time.
func defaultTemplateKegRoot(rt *toolkit.Runtime) string {
	switch runtime.GOOS {
	case "darwin", "linux":
		return "~/Documents/kegs"
	default:
		if rt != nil {
			if home, err := rt.GetHome(); err == nil && strings.TrimSpace(home) != "" {
				return filepath.Join(home, "Documents", "kegs")
			}
		}
		return "~/Documents/kegs"
	}
}
