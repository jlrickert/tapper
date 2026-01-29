package tapper

import (
	"context"
	"fmt"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

type KegService struct {
	ConfigService *ConfigService

	kegCache map[string]*keg.Keg
}

type ResolveKegOptions struct {
	Root    string
	Alias   string
	NoCache bool
}

func (s *KegService) Resolve(ctx context.Context, opts ResolveKegOptions) (*keg.Keg, error) {
	if s.kegCache != nil {
		s.kegCache = map[string]*keg.Keg{}
	}
	if opts.Alias != "" {
		return s.ResolveKegAlias(ctx, opts.Alias)
	}
	if opts.Alias == "" && opts.Root == "" {
		env := toolkit.EnvFromContext(ctx)
		root, err := env.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
		return s.ResolvePath(ctx, root)
	}
	cache := !opts.NoCache
	cfg := s.ConfigService.Config(ctx, cache)
	alias := cfg.DefaultKeg()
	return s.ResolveKegAlias(ctx, alias)
}

func (s *KegService) ResolvePath(ctx context.Context, path string) (*keg.Keg, error) {
	cfg := s.ConfigService.Config(ctx, true)
	alias := cfg.LookupAlias(ctx, path)
	return s.ResolveKegAlias(ctx, alias)
}

func (s *KegService) ResolveKegAlias(ctx context.Context, alias string) (*keg.Keg, error) {
	target, err := s.ConfigService.ResolveTarget(ctx, alias, true)
	if err != nil {
		return nil, err
	}
	return keg.NewKegFromTarget(ctx, *target)
}
