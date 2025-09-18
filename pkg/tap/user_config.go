package tap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	std "github.com/jlrickert/go-std/pkg"
	"gopkg.in/yaml.v3"
)

// UserConfig is the optional higher-level tapper config (~/.config/tapper/config.yaml).
// It may contain multiple mappings that match repositories to keg targets.
type UserConfig struct {
	// defaultKeg is the alias for the default keg
	defaultKeg string `yaml:"defaultKeg,omitempty"`

	// Maps a path to a keg to use
	mappings []Mapping         `yaml:"mappings,omitempty"`
	kegs     map[string]KegUrl `yaml:"aliases,omitempty"`
}

// Mapping is a single mapping entry in the user config.
type Mapping struct {
	PathPrefix string `yaml:"prefix,omitempty"`
	PathRegex  string `yaml:"regex,omitempty"`
	Alias      string `yaml:"alias"`
}

func (cfg *UserConfig) Normalize(ctx context.Context) error {
	if cfg == nil {
		return nil
	}

	// Normalize default keg if present.
	if cfg.defaultKeg != "" {
		cfg.defaultKeg = strings.TrimSpace(strings.ToLower(cfg.defaultKeg))
	}

	// Normalize aliases: lowercase keys and normalize each KegUrl value.
	if cfg.kegs != nil {
		newKegs := make(map[string]KegUrl, len(cfg.kegs))
		for k, v := range cfg.kegs {
			key := strings.TrimSpace(k)
			lkey := strings.ToLower(key)

			// Normalize the KegUrl value.
			if err := (&v).Normalize(ctx); err != nil {
				return fmt.Errorf("normalize alias %q: %w", k, err)
			}

			// If duplicate lowercase keys occur, last one wins (deterministic
			// iteration order is not guaranteed, but this keeps behavior simple).
			newKegs[lkey] = v
		}
		cfg.kegs = newKegs
	}

	// Normalize mappings: clean path prefixes, validate regex, normalize alias.
	for i := range cfg.mappings {
		m := &cfg.mappings[i]
		if m.PathPrefix != "" {
			m.PathPrefix = filepath.Clean(m.PathPrefix)
		}
		if m.PathRegex != "" {
			if _, err := regexp.Compile(m.PathRegex); err != nil {
				return fmt.Errorf("invalid mapping regex %q: %w", m.PathRegex, err)
			}
		}
		m.Alias = strings.TrimSpace(m.Alias)
		m.Alias = strings.ToLower(m.Alias)
	}

	// Optionally expand env in any remaining stringy fields via std helpers.
	// This mirrors behavior in other config normalization helpers.
	env := std.EnvFromContext(ctx)
	if cfg.kegs != nil {
		for k, v := range cfg.kegs {
			// Ensure Value expanded (again) in case ExpandEnv is needed.
			v.Value = std.ExpandEnv(env, v.Value)
			cfg.kegs[k] = v
		}
	}

	return nil
}

// ResolveKegAlias looks up an alias in the user's Tapper configuration and
// returns the resolved KegTarget. Behavior:
//
//   - Prefers an exact key match against cfg.Aliases.
//   - Falls back to a case-insensitive match if no exact key is found.
//   - If no user config is available or the alias cannot be found, a typed
//     AliasNotFoundError is returned.
//
// Note: This function only reads and validates the alias entry. It does not
// perform further resolution (for example preferring local paths or expanding
// other alias tokens); callers that need that behavior should load the full
// UserConfig and use ResolveKegTargetForRepo as appropriate.
func (cfg *UserConfig) ResolveAlias(ctx context.Context, alias string) (*KegUrl, error) {
	if cfg == nil {
		return nil, NewAliasNotFoundError(alias)
	}
	if cfg.kegs == nil {
		return nil, NewAliasNotFoundError(alias)
	}

	if target, ok := cfg.kegs[alias]; ok {
		return &target, nil
	}

	// Case-insensitive fallback.
	if target, ok := cfg.kegs[strings.ToLower(alias)]; ok {
		return &target, nil
	}

	return nil, NewAliasNotFoundError(alias)
}

// ResolveKegMap searches the configured mappings for one that matches the
// provided path and returns the resolved KegUrl for the mapping's alias.
// Matching rules (checked in declaration order):
//   - If Mapping.PathPrefix is set and path has that prefix -> match.
//   - If Mapping.PathRegex is set and the regex matches -> match.
//
// For a matched mapping the function attempts to resolve mapping.Alias via
// ResolveAlias. If the alias is not found the mapping is skipped and search
// continues. If no mapping matches and a defaultKeg is configured, the default
// is returned. If nothing is found ErrKegNotFound is returned.
//
// Note: This function uses the first matching mapping. More advanced tie-
// breaking (priority, specificity) can be added later.
func (cfg *UserConfig) ResolveKegMap(ctx context.Context, path string) (*KegUrl, error) {
	lg := std.LoggerFromContext(ctx)

	if cfg == nil {
		lg.Debug("no user config provided to ResolveKegMap")
		return nil, ErrKegNotFound
	}

	// Normalize incoming path for stable comparisons.
	p := filepath.Clean(path)

	for _, m := range cfg.mappings {
		// PathPrefix matching
		if m.PathPrefix != "" {
			pref := filepath.Clean(m.PathPrefix)
			if strings.HasPrefix(p, pref) {
				if m.Alias == "" {
					lg.Debug("mapping matched by prefix but has no alias", "prefix", m.PathPrefix, "path", p)
					continue
				}
				kurl, err := cfg.ResolveAlias(ctx, m.Alias)
				if err != nil {
					lg.Debug("mapping alias not found, skipping", "alias", m.Alias, "err", err)
					continue
				}
				lg.Info("resolved keg via mapping (prefix)", "prefix", m.PathPrefix, "alias", m.Alias)
				return kurl, nil
			}
		}

		// Regex matching
		if m.PathRegex != "" {
			re, err := regexp.Compile(m.PathRegex)
			if err != nil {
				lg.Error("invalid mapping regex, skipping", "regex", m.PathRegex, "err", err)
				continue
			}
			if re.MatchString(p) {
				if m.Alias == "" {
					lg.Debug("mapping matched by regex but has no alias", "regex", m.PathRegex, "path", p)
					continue
				}
				kurl, err := cfg.ResolveAlias(ctx, m.Alias)
				if err != nil {
					lg.Debug("mapping alias not found, skipping", "alias", m.Alias, "err", err)
					continue
				}
				lg.Info("resolved keg via mapping (regex)", "regex", m.PathRegex, "alias", m.Alias)
				return kurl, nil
			}
		}
	}

	// Fallback to defaultKeg if configured.
	if cfg.defaultKeg != "" {
		lg.Info("using default keg from user config")
		return cfg.ResolveAlias(ctx, cfg.defaultKeg)
	}

	lg.Debug("no mapping matched and no default keg configured", "path", p)
	return nil, ErrKegNotFound
}

func ParseUserConfig(ctx context.Context, data []byte) (*UserConfig, error) {
	var uc UserConfig
	if err := yaml.Unmarshal(data, &uc); err != nil {
		return nil, fmt.Errorf("failed to parse user config: %w", err)
	}
	if err := uc.Normalize(ctx); err != nil {
		return nil, fmt.Errorf("failed to normalize user config: %w", err)
	}
	return &uc, nil
}

func ReadUserConfigFrom(ctx context.Context, path string) (*UserConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var uc UserConfig
	if err := yaml.Unmarshal(b, &uc); err != nil {
		return nil, err
	}
	if err := uc.Normalize(ctx); err != nil {

	}
	return &uc, nil
}

// WriteUserConfig writes the UserConfig to pathName atomically.
//
// Behavior:
//   - Validates receiver is non-nil and pathName is provided.
//   - Ensures the Updated field is set (RFC3339 UTC) if empty.
//   - Creates parent directory (os.MkdirAll) as needed.
//   - Marshals the config to YAML and writes it to a temporary file in the
//     same directory, then renames the temp file into place to provide an
//     atomic replacement on POSIX filesystems.
//   - Returns a wrapped error describing the failure (marshal, write, rename,
//     or directory creation). The temporary file is removed on failure when
//     possible.
//
// Note: This routine does not attempt an fsync of the directory after rename.
// Callers that require stronger durability guarantees should perform explicit
// fsyncs on the target filesystem where supported.
func (uc *UserConfig) WriteUserConfig(ctx context.Context, path string) error {
	lg := std.LoggerFromContext(ctx)
	b, err := yaml.Marshal(uc)
	if err != nil {
		lg.Error("marshal user config", "err", err, "path", path)
		return fmt.Errorf("marshal user config: %w", err)
	}

	if err := std.AtomicWriteFile(path, b, 0o644); err != nil {
		lg.Error("failed to write to user config", "err", err, "path", path)
		return fmt.Errorf("failed to write user config: %w", err)
	}

	return nil
}
