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

type KegService struct {
	Runtime *toolkit.Runtime

	ConfigService *ConfigService

	// Caches a keg by alias
	kegCache map[string]*keg.Keg
}

type ResolveKegOptions struct {
	Root    string
	Keg     string
	Project bool
	Cwd     bool
	Path    string
	NoCache bool
}

func (s *KegService) init() {
	if s.kegCache == nil {
		s.kegCache = map[string]*keg.Keg{}
	}
}

func (s *KegService) Resolve(ctx context.Context, opts ResolveKegOptions) (*keg.Keg, error) {
	s.init()
	cache := !opts.NoCache

	alias := strings.TrimSpace(opts.Keg)
	explicitPath := strings.TrimSpace(opts.Path)

	if alias != "" && (opts.Project || explicitPath != "") {
		return nil, fmt.Errorf("--keg cannot be used with --project or --path")
	}
	if opts.Cwd && !opts.Project && explicitPath == "" {
		return nil, fmt.Errorf("--cwd can only be used with --project")
	}

	if explicitPath != "" {
		return s.resolveProjectTarget(ctx, explicitPath, cache)
	}
	if opts.Project {
		base := strings.TrimSpace(opts.Root)
		if base == "" || opts.Cwd {
			var err error
			base, err = s.Runtime.Getwd()
			if err != nil {
				return nil, fmt.Errorf("failed to get working directory: %w", err)
			}
		}
		if !opts.Cwd {
			if gitRoot := appCtx.FindGitRoot(ctx, s.Runtime, base); gitRoot != "" {
				base = gitRoot
			}
		}
		return s.resolveProjectTarget(ctx, base, cache)
	}
	if alias != "" {
		return s.resolveKegAlias(ctx, alias, cache)
	}

	root := strings.TrimSpace(opts.Root)
	if root == "" {
		var err error
		root, err = s.Runtime.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
	}
	return s.resolvePath(ctx, root, cache)
}

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
	for _, b := range baseCandidates {
		if b == "" {
			continue
		}
		candidates = append(candidates, b, filepath.Join(b, "docs"))
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

	return nil, fmt.Errorf("project keg not found (checked: %s)", strings.Join(checked, ", "))
}

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

func (s *KegService) resolvePath(ctx context.Context, path string, cache bool) (*keg.Keg, error) {
	s.init()
	cfg := s.ConfigService.Config(true)
	kegAlias := cfg.LookupAlias(s.Runtime, path)
	if kegAlias == "" {
		kegAlias = cfg.DefaultKeg()
	}
	if kegAlias == "" {
		return nil, fmt.Errorf("no keg configured")
	}
	return s.resolveKegAlias(ctx, kegAlias, cache)
}

func (s *KegService) resolveKegAlias(ctx context.Context, kegAlias string, cache bool) (*keg.Keg, error) {
	s.init()
	if kegAlias == "" {
		return nil, fmt.Errorf("no keg configured")
	}
	if cache && s.kegCache[kegAlias] != nil {
		return s.kegCache[kegAlias], nil
	}
	target, err := s.ConfigService.ResolveTarget(kegAlias, cache)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("keg alias not found: %s", kegAlias)
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
