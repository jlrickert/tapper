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
	Alias   string
	NoCache bool
}

func (s *KegService) init() {
	if s.kegCache == nil {
		s.kegCache = map[string]*keg.Keg{}
	}
}

func (s *KegService) Resolve(ctx context.Context, opts ResolveKegOptions) (*keg.Keg, error) {
	s.init()
	if opts.Alias != "" {
		return s.ResolveKegAlias(ctx, opts.Alias, !opts.NoCache)
	}
	if opts.Alias == "" {
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
	alias := cfg.LookupAlias(ctx, path)
	return s.ResolveKegAlias(ctx, alias, cache)
}

func (s *KegService) ResolveKegAlias(ctx context.Context, alias string, cache bool) (*keg.Keg, error) {
	s.init()
	if cache && s.kegCache[alias] != nil {
		return s.kegCache[alias], nil
	}
	target, err := s.ConfigService.ResolveTarget(ctx, alias, cache)
	if err != nil {
		return nil, err
	}
	k, err := keg.NewKegFromTarget(ctx, *target)
	if err != nil {
		return k, err
	}
	if k != nil {
		s.kegCache[alias] = k
	}
	return k, err
}
