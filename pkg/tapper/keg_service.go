package tapper

import (
	"context"
	"fmt"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

type KegService struct {
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
		return s.ResolveKegAlias(ctx, opts.Keg, !opts.NoCache)
	}
	if opts.Keg == "" {
		env := toolkit.EnvFromContext(ctx)
		root, err := env.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
		return s.ResolvePath(ctx, root, !opts.NoCache)
	}
	cache := !opts.NoCache
	cfg := s.ConfigService.Config(ctx, cache)
	alias := cfg.DefaultKeg()
	return s.ResolveKegAlias(ctx, alias, !opts.NoCache)
}

func (s *KegService) ResolvePath(ctx context.Context, path string, cache bool) (*keg.Keg, error) {
	s.init()
	cfg := s.ConfigService.Config(ctx, true)
	kegAlias := cfg.LookupAlias(ctx, path)
	return s.ResolveKegAlias(ctx, kegAlias, cache)
}

func (s *KegService) ResolveKegAlias(ctx context.Context, kegAlias string, cache bool) (*keg.Keg, error) {
	s.init()
	if cache && s.kegCache[kegAlias] != nil {
		return s.kegCache[kegAlias], nil
	}
	target, err := s.ConfigService.ResolveTarget(ctx, kegAlias, cache)
	if err != nil {
		return nil, err
	}
	k, err := keg.NewKegFromTarget(ctx, *target)
	if err != nil {
		return k, err
	}
	if k != nil {
		s.kegCache[kegAlias] = k
	}
	return k, err
}
