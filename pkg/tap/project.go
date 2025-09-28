package tap

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	std "github.com/jlrickert/go-std/pkg"

	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

type Project struct {
	// Root is the path to the root of the project
	Root            string
	ConfigRoot      string
	StateRoot       string
	DataRoot        string
	CacheRoot       string
	LocalConfigRoot string

	// data configuration. This is managed programmatically
	dCfg *Config

	// User configuration. User manually manages this
	uCfg *Config

	// Local config for the project.
	lCfg *Config
}

type ProjectOption = func(opt *Project)

func WithStateRoot(path string) ProjectOption {
	return func(opt *Project) {
		opt.StateRoot = path
	}
}

func WithConfigRoot(path string) ProjectOption {
	return func(opt *Project) {
		opt.ConfigRoot = path
	}
}

func WithDataRoot(path string) ProjectOption {
	return func(opt *Project) {
		opt.DataRoot = path
	}
}

func WithCacheRoot(path string) ProjectOption {
	return func(opt *Project) {
		opt.CacheRoot = path
	}
}

func WithRoot(path string) ProjectOption {
	return func(opt *Project) {
		opt.Root = path
	}
}

func WithAutoRootDetect(ctx context.Context) ProjectOption {
	return func(opt *Project) {
		env := std.EnvFromContext(ctx)
		wd, _ := env.Getwd()
		root := std.FindGitRoot(ctx, wd)
		opt.Root = root
	}
}

func NewProject(ctx context.Context, opts ...ProjectOption) (*Project, error) {
	env := std.EnvFromContext(ctx)
	p := &Project{}

	for _, f := range opts {
		f(p)
	}

	if p.Root == "" {
		wd, err := env.Getwd()
		if err != nil {
			return p, fmt.Errorf("unable to infer project: %w", err)
		}
		p.Root = wd
	}

	if p.ConfigRoot == "" {
		if path, err := std.UserConfigPath(ctx); err != nil {
			return nil, fmt.Errorf(
				"unable to get find user config path: %w",
				keg.ErrNotExist,
			)
		} else {
			p.ConfigRoot = filepath.Join(path, DefaultAppName)
		}
	}

	if p.DataRoot == "" {
		if path, err := std.UserDataPath(ctx); err != nil {
			return nil, fmt.Errorf(
				"unable to get find user data path: %w",
				keg.ErrNotExist,
			)
		} else {
			p.DataRoot = filepath.Join(path, DefaultAppName)
		}

	}
	if p.StateRoot == "" {
		if path, err := std.UserStatePath(ctx); err != nil {
			return nil, fmt.Errorf(
				"unable to get find user state root: %w",
				keg.ErrNotExist,
			)
		} else {
			p.StateRoot = filepath.Join(path, DefaultAppName)
		}
	}

	if p.CacheRoot == "" {
		if path, err := std.UserCachePath(ctx); err != nil {
			return nil, fmt.Errorf(
				"unable to get find user cache root: %w",
				keg.ErrNotExist,
			)
		} else {
			p.CacheRoot = filepath.Join(path, DefaultAppName)
		}

	}

	return p, nil
}

func (p *Project) Config(ctx context.Context) *Config {
	dataCfg, err := ReadConfig(ctx, filepath.Join(p.DataRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		dataCfg = DefaultDataConfig(ctx)
	}

	userCfg, err := ReadConfig(ctx, filepath.Join(p.ConfigRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		userCfg = DefaultDataConfig(ctx)
	}
	localCfg, err := ReadConfig(ctx, filepath.Join(p.LocalConfigRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		userCfg = DefaultDataConfig(ctx)
	}
	return MergeConfig(dataCfg, userCfg, localCfg)
}

func (p *Project) DefaultTarget(ctx context.Context) (*kegurl.Target, error) {
	cfg := p.Config(ctx)
	cfg.ResolveProjectKeg(ctx, p.Root)
	return cfg.ResolveAlias(ctx, cfg.DefaultKeg)
}

func (p *Project) ResolveKeg(ctx context.Context) (*kegurl.Target, error) {
	cfg := p.Config(ctx)
	return cfg.ResolveProjectKeg(ctx, p.Root)
}
