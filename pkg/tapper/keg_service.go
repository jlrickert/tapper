package tapper

import (
	"context"
	"fmt"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
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
	NoCache bool
}

func (s *KegService) init() {
	if s.kegCache == nil {
		s.kegCache = map[string]*keg.Keg{}
	}
}

func (s *KegService) Resolve(ctx context.Context, opts ResolveKegOptions) (*keg.Keg, error) {
	s.init()
	if opts.Keg != "" {
		return s.resolveKegAlias(ctx, opts.Keg, !opts.NoCache)
	}
	if opts.Keg == "" {
		root, err := s.Runtime.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
		return s.resolvePath(ctx, root, !opts.NoCache)
	}
	cache := !opts.NoCache
	cfg := s.ConfigService.Config(cache)
	alias := cfg.DefaultKeg()
	return s.resolveKegAlias(ctx, alias, !opts.NoCache)
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
