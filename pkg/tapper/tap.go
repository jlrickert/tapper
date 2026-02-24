package tapper

import (
	"context"
	"fmt"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

type Tap struct {
	Root string
	// Runtime carries process-level dependencies.
	Runtime *toolkit.Runtime

	PathService   *PathService
	ConfigService *ConfigService
	KegService    *KegService
}

type TapOptions struct {
	Root       string
	ConfigPath string
	Runtime    *toolkit.Runtime
}

func NewTap(opts TapOptions) (*Tap, error) {
	rt := opts.Runtime
	if rt == nil {
		var err error
		rt, err = toolkit.NewRuntime()
		if err != nil {
			return nil, fmt.Errorf("unable to create runtime: %w", err)
		}
	}
	if err := rt.Validate(); err != nil {
		return nil, fmt.Errorf("invalid runtime: %w", err)
	}

	if opts.Root == "" {
		wd, err := rt.Getwd()
		if err != nil {
			return nil, fmt.Errorf("unable to determine working directory: %w", err)
		}
		opts.Root = wd
	}
	pathService, err := NewPathService(rt, opts.Root)
	if err != nil {
		return nil, fmt.Errorf("unable to create path service: %w", err)
	}
	configService := &ConfigService{
		Runtime:     rt,
		PathService: pathService,
		ConfigPath:  opts.ConfigPath,
	}
	kegService := &KegService{
		Runtime:       rt,
		ConfigService: configService,
	}
	return &Tap{
		Runtime:       rt,
		Root:          opts.Root,
		PathService:   pathService,
		ConfigService: configService,
		KegService:    kegService,
	}, nil
}

// KegTargetOptions describes how a command should resolve a keg target.
type KegTargetOptions struct {
	// Keg is the configured alias.
	Keg string

	// Project resolves using project-local keg discovery.
	Project bool

	// Cwd, when combined with Project, uses cwd as the base instead of git root.
	Cwd bool

	// Path is an explicit local project path used for project keg discovery.
	Path string
}

func (t *Tap) resolveKeg(ctx context.Context, opts KegTargetOptions) (*keg.Keg, error) {
	return t.KegService.Resolve(ctx, ResolveKegOptions{
		Root:    t.Root,
		Keg:     opts.Keg,
		Project: opts.Project,
		Cwd:     opts.Cwd,
		Path:    opts.Path,
		NoCache: false,
	})
}
