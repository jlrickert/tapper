package tapper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jlrickert/cli-toolkit/cfgcascade"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

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

	// Cached configs.
	userCache    *Config
	projectCache *Config

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
	s.LoadWarnings = nil
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

// ProjectConfig returns the project-level configuration with optional caching.
// If cache is true and a cached config exists, it returns the cached version.
// Otherwise, it reads the config from the local config root and caches the result.
func (s *ConfigService) ProjectConfig(cache bool) (*Config, error) {
	if cache && s.projectCache != nil {
		return s.projectCache, nil
	}
	cfg, err := ReadConfig(s.Runtime, filepath.Join(s.PathService.LocalConfigRoot, "config.yaml"))
	if err != nil {
		return nil, err
	}
	s.projectCache = cfg
	return cfg, nil
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

// DiscoveredKegAliases returns aliases discovered from configured kegSearchPaths.
func (s *ConfigService) DiscoveredKegAliases(cache bool) ([]string, error) {
	targets, err := s.localRepoKegTargets(cache)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(targets))
	for alias := range targets {
		out = append(out, alias)
	}
	sort.Strings(out)
	return out, nil
}

// ResolveTarget resolves an alias to a keg target.
// Resolution order is: explicit configured alias, discovered local keg alias.
// When alias is empty it uses defaultKeg, then fallbackKeg.
func (s *ConfigService) ResolveTarget(alias string, cache bool) (*kegurl.Target, error) {
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
		return nil, fmt.Errorf("no keg configured (set defaultKeg/fallbackKeg, use --keg, or configure kegs in repo config)")
	}

	// Check for explicit keg in configuration first.
	t, err := cfg.ResolveAlias(requestedAlias)
	if err == nil && t != nil {
		return t, nil
	}

	// Fallback to a discovered local repository keg.
	localTargets, err := s.localRepoKegTargets(cache)
	if err != nil {
		return nil, err
	}
	if path, ok := localTargets[requestedAlias]; ok {
		t := kegurl.NewFile(path)
		return &t, nil
	}

	return nil, fmt.Errorf("keg alias not found: %s (add alias under kegs:, add discovery paths in kegSearchPaths, or create ./kegs/%s)", requestedAlias, requestedAlias)
}

// localRepoKegTargets scans kegSearchPaths and returns alias-to-path mappings.
// Paths listed later in kegSearchPaths take precedence for alias collisions.
func (s *ConfigService) localRepoKegTargets(cache bool) (map[string]string, error) {
	cfg, err := s.Config(cache)
	if err != nil {
		return nil, fmt.Errorf("failed to load config for keg discovery: %w", err)
	}
	searchPaths := cfg.KegSearchPaths()
	if len(searchPaths) == 0 {
		return nil, fmt.Errorf("kegSearchPaths not defined in config (set kegSearchPaths in tap repo config --user)")
	}

	// Later search paths take precedence for alias collisions.
	targets := map[string]string{}
	for _, searchPath := range searchPaths {
		if strings.TrimSpace(searchPath) == "" {
			continue
		}
		repoPath, _ := toolkit.ExpandPath(s.Runtime, searchPath)
		if strings.TrimSpace(repoPath) == "" {
			continue
		}

		pattern := filepath.Join(repoPath, "*", "keg")
		kegPaths, err := s.Runtime.Glob(pattern)
		if err != nil {
			return nil, err
		}
		for _, kegPath := range kegPaths {
			rel, err := filepath.Rel(repoPath, kegPath)
			if err != nil {
				continue
			}
			alias := firstDir(rel)
			if alias == "" {
				continue
			}
			targets[alias] = filepath.Join(repoPath, alias)
		}
	}

	return targets, nil
}
