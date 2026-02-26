package tapper

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	appCtx "github.com/jlrickert/cli-toolkit/apppaths"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

// KegService resolves keg targets from config, project paths, and explicit filesystem locations.
type KegService struct {
	// Runtime provides filesystem and environment access used to resolve kegs.
	Runtime *toolkit.Runtime

	// ConfigService resolves configured keg aliases and targets.
	ConfigService *ConfigService

	// kegCache memoizes resolved kegs by alias or file-derived cache key.
	kegCache map[string]*keg.Keg
}

// ResolveKegOptions controls how KegService resolves a keg target.
type ResolveKegOptions struct {
	// Root is the base path used for project and fallback resolution.
	Root string
	// Keg is the explicit keg alias to resolve.
	Keg string
	// Project resolves a keg from project-local locations.
	Project bool
	// Cwd limits project resolution to the current working directory.
	Cwd bool
	// Path resolves a keg from an explicit filesystem path.
	Path string
	// NoCache disables in-memory keg caching for this resolution.
	NoCache bool
}

// ensureCache initializes the in-memory keg cache when needed.
func (s *KegService) ensureCache() {
	if s.kegCache == nil {
		s.kegCache = map[string]*keg.Keg{}
	}
}

// Resolve returns a keg using explicit path, project, alias, or configured fallback resolution.
func (s *KegService) Resolve(ctx context.Context, opts ResolveKegOptions) (*keg.Keg, error) {
	s.ensureCache()
	cache := !opts.NoCache

	alias := strings.TrimSpace(opts.Keg)
	explicitPath := strings.TrimSpace(opts.Path)

	if alias != "" && (opts.Project || explicitPath != "") {
		return nil, fmt.Errorf("--keg cannot be used with --project or --path")
	}
	if opts.Cwd && !opts.Project && explicitPath == "" {
		return nil, fmt.Errorf("--cwd can only be used with --project")
	}

	base := strings.TrimSpace(opts.Root)
	if base == "" {
		var err error
		base, err = s.Runtime.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	if explicitPath != "" {
		return s.resolveProjectTarget(ctx, explicitPath, cache)
	}
	if opts.Project {
		if !opts.Cwd {
			if gitRoot := appCtx.FindGitRoot(ctx, s.Runtime, base); gitRoot != "" {
				base = gitRoot
			}
		}
		return s.resolveProjectTarget(ctx, base, cache)
	}
	if alias != "" {
		return s.resolveKegAlias(ctx, alias, base, cache)
	}

	return s.resolvePath(ctx, base, cache)
}

// resolveProjectTarget resolves a filesystem-backed keg under known project keg locations.
func (s *KegService) resolveProjectTarget(ctx context.Context, base string, cache bool) (*keg.Keg, error) {
	rawBase := filepath.Clean(toolkit.ExpandEnv(s.Runtime, base))
	expandedBase := rawBase
	if p, err := toolkit.ExpandPath(s.Runtime, rawBase); err == nil {
		expandedBase = filepath.Clean(p)
	}

	baseCandidates := []string{rawBase}
	if expandedBase != "" && expandedBase != rawBase {
		baseCandidates = append(baseCandidates, expandedBase)
	}

	var candidates []string
	seen := map[string]struct{}{}
	for _, b := range baseCandidates {
		if b == "" {
			continue
		}
		baseName := filepath.Base(filepath.Clean(b))
		for _, candidate := range []string{
			b,
			filepath.Join(b, "kegs", baseName),
			filepath.Join(b, "kegs", "project"),
			filepath.Join(b, "kegs", "tapper"),
		} {
			candidate = filepath.Clean(candidate)
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			candidates = append(candidates, candidate)
		}

	}

	var checked []string
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		kegFile := filepath.Join(candidate, "keg")
		checked = append(checked, kegFile)
		info, statErr := s.Runtime.Stat(kegFile, false)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		return s.resolveFileKeg(ctx, candidate, cache)
	}

	return nil, newProjectKegNotFoundError(checked)
}

// resolveFileKeg resolves a keg from a filesystem root and caches it by normalized path.
func (s *KegService) resolveFileKeg(ctx context.Context, root string, cache bool) (*keg.Keg, error) {
	key := "file:" + filepath.Clean(root)
	if cache && s.kegCache[key] != nil {
		return s.kegCache[key], nil
	}

	target := kegurl.NewFile(root)
	k, err := keg.NewKegFromTarget(ctx, target, s.Runtime)
	if err != nil {
		return nil, err
	}

	if cache {
		s.kegCache[key] = k
	}
	return k, nil
}

// resolvePath resolves the effective keg alias from config for the given path and returns its keg.
func (s *KegService) resolvePath(ctx context.Context, path string, cache bool) (*keg.Keg, error) {
	s.ensureCache()
	cfg := s.ConfigService.Config(true)
	kegAlias := cfg.DefaultKeg()
	if kegAlias == "" {
		kegAlias = cfg.LookupAlias(s.Runtime, path)
	}
	if kegAlias == "" {
		kegAlias = cfg.FallbackKeg()
	}
	if kegAlias == "" {
		return nil, fmt.Errorf("no keg configured")
	}
	return s.resolveKegAlias(ctx, kegAlias, path, cache)
}

// resolveKegAlias resolves a keg alias from config and optionally falls back to project-local alias resolution.
func (s *KegService) resolveKegAlias(ctx context.Context, kegAlias string, projectRoot string, cache bool) (*keg.Keg, error) {
	s.ensureCache()
	if kegAlias == "" {
		return nil, fmt.Errorf("no keg configured")
	}
	if cache && s.kegCache[kegAlias] != nil {
		return s.kegCache[kegAlias], nil
	}

	cfg := s.ConfigService.Config(cache)
	_, configured := cfg.Kegs()[kegAlias]

	target, err := s.ConfigService.ResolveTarget(kegAlias, cache)
	if err == nil && target != nil {
		k, err := keg.NewKegFromTarget(ctx, *target, s.Runtime)
		if err != nil {
			return k, err
		}
		if k != nil {
			s.kegCache[kegAlias] = k
		}
		return k, nil
	}

	// If alias is not configured, allow project-local alias fallback: <project>/kegs/<alias>.
	// This supports local project kegs without requiring config entries.
	if !configured {
		if projectKeg, found, projectErr := s.resolveProjectAlias(ctx, projectRoot, kegAlias, cache); projectErr != nil {
			return nil, projectErr
		} else if found {
			if cache && projectKeg != nil {
				s.kegCache[kegAlias] = projectKeg
			}
			return projectKeg, nil
		}
	}

	if err != nil {
		return nil, err
	}

	k, err := keg.NewKegFromTarget(ctx, *target, s.Runtime)
	if err != nil {
		return k, err
	}
	if k != nil {
		s.kegCache[kegAlias] = k
	}
	return k, err
}

// resolveProjectAlias resolves a project-local alias at <project>/kegs/<alias>/keg when present.
func (s *KegService) resolveProjectAlias(ctx context.Context, base string, alias string, cache bool) (*keg.Keg, bool, error) {
	base = strings.TrimSpace(base)
	alias = strings.TrimSpace(alias)
	if base == "" || alias == "" {
		return nil, false, nil
	}

	searchBase := base
	if gitRoot := appCtx.FindGitRoot(ctx, s.Runtime, base); gitRoot != "" {
		searchBase = gitRoot
	}

	rawBase := filepath.Clean(toolkit.ExpandEnv(s.Runtime, searchBase))
	expandedBase := rawBase
	if p, err := toolkit.ExpandPath(s.Runtime, rawBase); err == nil {
		expandedBase = filepath.Clean(p)
	}

	baseCandidates := []string{rawBase}
	if expandedBase != "" && expandedBase != rawBase {
		baseCandidates = append(baseCandidates, expandedBase)
	}

	seen := map[string]struct{}{}
	for _, candidateBase := range baseCandidates {
		if candidateBase == "" {
			continue
		}
		projectKegRoot := filepath.Clean(filepath.Join(candidateBase, "kegs", alias))
		if _, ok := seen[projectKegRoot]; ok {
			continue
		}
		seen[projectKegRoot] = struct{}{}

		kegFile := filepath.Join(projectKegRoot, "keg")
		info, statErr := s.Runtime.Stat(kegFile, false)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}

		k, err := s.resolveFileKeg(ctx, projectKegRoot, cache)
		if err != nil {
			return nil, false, err
		}
		return k, true, nil
	}

	return nil, false, nil
}
