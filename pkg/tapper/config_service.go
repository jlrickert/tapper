package tapper

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/jlrickert/cli-toolkit/toolkit"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

type ConfigService struct {
	Runtime *toolkit.Runtime

	PathService *PathService

	// ConfigPath is the path to the config file.
	ConfigPath string

	// Cached configs.
	userCache    *Config
	projectCache *Config

	mergedCache *Config
}

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

func (s *ConfigService) ResetCache() {
	s.mergedCache = nil
	s.userCache = nil
	s.projectCache = nil
}

// UserConfig returns the global user configuration
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
// Otherwise, it retrieves both configs, merges them, caches the result, and returns it.
func (s *ConfigService) Config(cache bool) *Config {
	if cache && s.mergedCache != nil {
		return s.mergedCache
	}

	if s.ConfigPath != "" {
		// FIXME: propagate this error up. Thus function is missing error type
		cfg, _ := ReadConfig(s.Runtime, s.ConfigPath)
		if cfg == nil {
			cfg = &Config{}
		}
		s.mergedCache = cfg
		return cfg
	}

	user, _ := s.UserConfig(cache)
	project, _ := s.ProjectConfig(cache)
	s.mergedCache = MergeConfig(user, project)
	return s.mergedCache
}

//// ListKegs returns a list of unique keg directory names found in the user repository
//// and configured kegs, with optional caching of the underlying configuration.
//func (s *ConfigService) ListKegs(ctx context.Context, cache bool) ([]string, error) {
//	cfg := s.Config(ctx, cache)
//	userRepo, _ := toolkit.ExpandPath(ctx, cfg.UserRepoPath())
//
//	// Find files
//	var results []string
//	pattern := filepath.Join(userRepo, "*", "keg")
//	if kegPaths, err := toolkit.Glob(ctx, pattern); err == nil {
//		for _, kegPath := range kegPaths {
//			path, err := filepath.Rel(userRepo, kegPath)
//			if err == nil {
//				results = append(results, path)
//			}
//		}
//	}
//
//	results = append(results, cfg.ListKegs()...)
//
//	// Extract unique directories containing keg files
//	kegDirs := make([]string, 0, len(results))
//	seenDirs := make(map[string]bool)
//	for _, result := range results {
//		dir := firstDir(result)
//		if !seenDirs[dir] {
//			kegDirs = append(kegDirs, dir)
//			seenDirs[dir] = true
//		}
//	}
//
//	return kegDirs, nil
//}

func (s *ConfigService) LocalRepoKegs(cache bool) ([]string, error) {
	cfg := s.Config(cache)
	repoPath, _ := toolkit.ExpandPath(s.Runtime, cfg.UserRepoPath())

	if repoPath == "" {
		return nil, fmt.Errorf("userRepoPath not defined in user config")
	}

	// Find files
	var results []string
	pattern := filepath.Join(repoPath, "*", "keg")

	kegPaths, err := s.Runtime.Glob(pattern)
	if err != nil {
		return nil, err
	}
	for _, kegPath := range kegPaths {
		path, err := filepath.Rel(repoPath, kegPath)
		if err == nil {
			results = append(results, path)
		}
	}

	// Extract unique directories containing keg files
	kegDirs := make([]string, 0, len(results))
	seenDirs := make(map[string]bool)
	for _, result := range results {
		dir := firstDir(result)
		if !seenDirs[dir] {
			kegDirs = append(kegDirs, dir)
			seenDirs[dir] = true
		}
	}

	return kegDirs, nil
}

func (s *ConfigService) ResolveTarget(alias string, cache bool) (*kegurl.Target, error) {
	cfg := s.Config(cache)
	requestedAlias := alias
	if requestedAlias == "" {
		requestedAlias = cfg.DefaultKeg()
	}

	// Check for explicit keg in configuration first.
	t, err := cfg.ResolveAlias(requestedAlias)
	if err == nil && t != nil {
		return t, nil
	}

	// Fallback to a discovered local repository keg.
	localKegs, err := s.LocalRepoKegs(cache)
	if err != nil {
		return nil, err
	}
	if cfg.UserRepoPath() != "" && slices.Contains(localKegs, requestedAlias) {
		path := filepath.Join(cfg.UserRepoPath(), requestedAlias)
		t := kegurl.NewFile(path)
		return &t, nil
	}

	return nil, fmt.Errorf("keg alias not found: %s", requestedAlias)
}
