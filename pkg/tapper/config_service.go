package tapper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jlrickert/cli-toolkit/cfgcascade"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

// ErrNotBootstrapped is returned by hub/namespace-dependent operations on the
// full `tap` surface when no user config exists yet — i.e. `tap bootstrap` has
// not been run. Explicit filesystem destinations (--path/--project/--cwd) and
// the pruned `keg` binary are exempt, and callers that resolve a keg by an
// explicit filesystem path are too.
var ErrNotBootstrapped = errors.New("tapper is not set up on this machine; run `tap bootstrap` to get started")

// ConfigLoadWarning represents a non-fatal issue encountered while loading config.
type ConfigLoadWarning struct {
	Source  string // "user config" or "project config"
	Path    string // file path that caused the issue
	Message string // human-readable description
	Err     error  // underlying error
}

// ConfigService loads, merges, and resolves tapper configuration state.
type ConfigService struct {
	Runtime *toolkit.Runtime

	PathService *PathService

	// ConfigPath is the path to the config file.
	ConfigPath string

	// LoadWarnings accumulates non-fatal issues from the last Config() call.
	// Missing config files are not warnings (graceful degradation). Corrupt
	// YAML, permission errors, etc. are recorded here.
	LoadWarnings []ConfigLoadWarning

	// ResolvedSources lists provider names that contributed to the merged config,
	// most-specific first. Populated after Config() runs the cascade.
	ResolvedSources []string

	// Cached configs.
	userCache    *Config
	projectCache *Config

	// projectWarnings holds load/trust-boundary warnings produced by the most
	// recent project-config walk, surfaced through LoadWarnings by Config().
	projectWarnings []ConfigLoadWarning

	mergedCache *Config
}

// NewConfigService builds a ConfigService rooted at root.
func NewConfigService(root string, rt *toolkit.Runtime) (*ConfigService, error) {
	pathService, err := NewPathService(rt, root)
	if err != nil {
		return nil, err
	}
	return &ConfigService{
		Runtime:     rt,
		PathService: pathService,
	}, nil
}

// ResetCache clears cached user, project, and merged configs.
func (s *ConfigService) ResetCache() {
	s.mergedCache = nil
	s.userCache = nil
	s.projectCache = nil
	s.projectWarnings = nil
	s.LoadWarnings = nil
	s.ResolvedSources = nil
}

// UserConfigExists reports whether a user config file is present — the signal
// that `tap bootstrap` has been run. When an explicit config path is set
// (--config) it checks that file; otherwise the standard user config path. It
// inspects the filesystem only; it never parses, so a corrupt-but-present config
// still counts as bootstrapped.
func (s *ConfigService) UserConfigExists() bool {
	path := s.ConfigPath
	if path == "" {
		path = filepath.Join(s.PathService.ConfigRoot, "config.yaml")
	}
	if _, err := s.Runtime.Stat(path, false); err == nil {
		return true
	}
	return false
}

// UserConfig returns the global user configuration.
func (s *ConfigService) UserConfig(cache bool) (*Config, error) {
	if cache && s.userCache != nil {
		return s.userCache, nil
	}
	path := filepath.Join(s.PathService.ConfigRoot, "config.yaml")
	cfg, err := ReadConfig(s.Runtime, path)
	if err != nil {
		return nil, err
	}
	s.userCache = cfg
	return cfg, nil
}

// WalkConfigsUp returns the absolute paths of every existing rel file found by
// walking from start up to the filesystem root, ordered DEEPEST-FIRST (nearest
// to start first). Missing candidates are skipped. The user-global config is
// not included; callers layer it underneath the returned project configs.
func WalkConfigsUp(rt *toolkit.Runtime, start, rel string) []string {
	var out []string
	seen := map[string]bool{}
	p := filepath.Clean(start)
	for {
		candidate := filepath.Join(p, rel)
		if _, err := rt.Stat(candidate, false); err == nil && !seen[candidate] {
			out = append(out, candidate)
			seen[candidate] = true
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return out
}

// ProjectConfig returns the merged project-level configuration with optional
// caching. It walks from the workspace root up to the filesystem root,
// collecting every .tapper/config.yaml, and merges them so a deeper directory
// overrides a shallower one. Hub definitions and credentials are stripped from
// project layers — only the user config may define them — and each strip is
// recorded as a load warning surfaced by Config(). Returns keg.ErrNotExist when
// no project config exists.
func (s *ConfigService) ProjectConfig(cache bool) (*Config, error) {
	if cache && s.projectCache != nil {
		return s.projectCache, nil
	}

	relDir := filepath.Base(s.PathService.LocalConfigRoot) // ".tapper"
	rel := filepath.Join(relDir, "config.yaml")
	paths := WalkConfigsUp(s.Runtime, s.PathService.Root, rel)

	var (
		merged   *Config
		warnings []ConfigLoadWarning
	)
	// paths is deepest-first; merge shallowest→deepest so a deeper directory's
	// values win.
	for i := len(paths) - 1; i >= 0; i-- {
		p := paths[i]
		cfg, err := ReadConfig(s.Runtime, p)
		if err != nil {
			if errors.Is(err, keg.ErrNotExist) {
				continue
			}
			warnings = append(warnings, ConfigLoadWarning{
				Source:  "project config",
				Path:    p,
				Message: fmt.Sprintf("failed to load project config at %s: %v", p, err),
				Err:     err,
			})
			continue
		}
		for _, field := range stripUntrustedFields(cfg) {
			warnings = append(warnings, ConfigLoadWarning{
				Source:  "project config",
				Path:    p,
				Message: fmt.Sprintf("ignored %s in project config at %s (hubs and credentials may only be set in the user config)", field, p),
			})
		}
		if merged == nil {
			merged = cfg
		} else {
			merged = MergeConfig(merged, cfg)
		}
	}

	s.projectWarnings = warnings
	if merged == nil {
		s.projectCache = nil
		return nil, keg.ErrNotExist
	}
	s.projectCache = merged
	return merged, nil
}

// Config returns the merged user and project configuration with optional caching.
// If cache is true and a merged config exists, it returns the cached version.
// Otherwise, it uses a cfgcascade.Cascade to resolve configuration from three
// providers in rank order: user config file, project config file, TAP_* env vars.
// When ConfigPath is set, it directly reads that file and bypasses the cascade.
func (s *ConfigService) Config(cache bool) (*Config, error) {
	if cache && s.mergedCache != nil {
		return s.mergedCache, nil
	}

	if s.ConfigPath != "" {
		cfg, err := ReadConfig(s.Runtime, s.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config at %s: %w", s.ConfigPath, err)
		}
		if cfg == nil {
			cfg = &Config{}
		}
		s.mergedCache = cfg
		return cfg, nil
	}

	s.LoadWarnings = nil

	userPath := filepath.Join(s.PathService.ConfigRoot, "config.yaml")
	projectPath := filepath.Join(s.PathService.LocalConfigRoot, "config.yaml")

	cascade := &cfgcascade.Cascade[*Config]{
		Layers: []cfgcascade.Layer[*Config]{
			{
				Rank: 1,
				Provider: &cfgcascade.FuncProvider[*Config]{
					ProviderName: "user config",
					Fn: func(_ func(string) string) (*Config, error) {
						cfg, err := s.UserConfig(cache)
						if err != nil {
							if errors.Is(err, keg.ErrNotExist) {
								return nil, os.ErrNotExist
							}
							return nil, err
						}
						return cfg, nil
					},
				},
			},
			{
				Rank: 2,
				Provider: &cfgcascade.FuncProvider[*Config]{
					ProviderName: "project config",
					Fn: func(_ func(string) string) (*Config, error) {
						cfg, err := s.ProjectConfig(cache)
						if err != nil {
							if errors.Is(err, keg.ErrNotExist) {
								return nil, os.ErrNotExist
							}
							return nil, err
						}
						return cfg, nil
					},
				},
			},
			{
				Rank: 3,
				Provider: &cfgcascade.FuncProvider[*Config]{
					ProviderName: "env vars",
					Fn: func(getenv func(string) string) (*Config, error) {
						envProvider := &cfgcascade.EnvProvider{
							ProviderName: "env vars",
							Prefix:       tapEnvPrefix,
							Keys:         tapEnvVarKeys,
						}
						envMap, err := envProvider.Load(getenv)
						if err != nil {
							return nil, err
						}
						cfg := configFromEnvMap(envMap)
						if cfg == nil {
							return nil, os.ErrNotExist
						}
						return cfg, nil
					},
				},
			},
		},
		MergeFn: func(base, overlay *Config) *Config {
			return MergeConfig(base, overlay)
		},
	}

	rv := cascade.Resolve(s.Runtime.Env().Get)
	s.ResolvedSources = rv.Sources

	// Surface trust-boundary / per-layer warnings accumulated by the project
	// config walk (the cascade only sees the merged result).
	s.LoadWarnings = append(s.LoadWarnings, s.projectWarnings...)

	// Map cascade provider errors to LoadWarnings.
	for _, pe := range rv.Errors {
		var path string
		switch pe.Name {
		case "user config":
			path = userPath
		case "project config":
			path = projectPath
		}
		s.LoadWarnings = append(s.LoadWarnings, ConfigLoadWarning{
			Source:  pe.Name,
			Path:    path,
			Message: fmt.Sprintf("failed to load %s at %s: %v", pe.Name, path, pe.Err),
			Err:     pe.Err,
		})
	}

	merged := rv.Value
	if merged == nil {
		merged = &Config{data: &configDTO{}}
	}

	s.mergedCache = merged
	return s.mergedCache, nil
}

// ResolveTarget resolves a keg selector to a keg target. When the selector is
// empty it uses defaultKeg, then fallbackKeg. The selector is parsed as a keg
// reference and turned into a concrete target by Config.ResolveAlias (the
// namespace-centric ResolveRef chain and per-hub-kind backend mapping).
func (s *ConfigService) ResolveTarget(alias, nsOverride, hubOverride string, cache bool) (*keg.Target, error) {
	cfg, err := s.Config(cache)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve target: %w", err)
	}
	requestedAlias := alias
	if requestedAlias == "" {
		requestedAlias = cfg.DefaultKeg()
	}
	if requestedAlias == "" {
		requestedAlias = cfg.FallbackKeg()
	}
	if requestedAlias == "" {
		return nil, fmt.Errorf("no keg configured (set defaultKeg/fallbackKeg or use --keg)")
	}

	// Apply the --namespace / --hub overrides onto the parsed reference.
	ref, err := applyRefOverrides(parseKegRef(requestedAlias), nsOverride, hubOverride, requestedAlias)
	if err != nil {
		return nil, err
	}
	return cfg.ResolveRef(s.Runtime, ref)
}
