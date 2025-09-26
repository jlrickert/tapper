package tap

import (
	"context"
	"fmt"
	"path/filepath"

	std "github.com/jlrickert/go-std/pkg"

	terrors "github.com/jlrickert/tapper/pkg/errors"
	"github.com/jlrickert/tapper/pkg/keg"
)

type Project struct {
	// Root is the path to the root of the project
	Root            string
	UserConfigRoot  string
	StateRoot       string
	DataRoot        string
	CacheRoot       string
	LocalConfigRoot string

	uCfg *UserConfig
	lCfg *LocalConfig
}

type ProjectOptions struct {
	AppName string
}

func newDefaultProjectOptions() *ProjectOptions {
	return &ProjectOptions{
		AppName: DefaultAppName,
	}
}

type ProjectOption = func(opt *ProjectOptions)

func WithAppName(appName string) ProjectOption {
	return func(opt *ProjectOptions) {
		opt.AppName = appName
	}
}

func LoadCurrentProject(ctx context.Context, opts ...ProjectOption) (*Project, error) {
	env := std.EnvFromContext(ctx)

	wd, err := env.GetWd()

	if err != nil {
		return nil, fmt.Errorf("unable to load current project: %w", err)
	}
	root := std.FindGitRoot(ctx, wd)
	return LoadProject(ctx, root, opts...)
}

func LoadProject(ctx context.Context, root string, opts ...ProjectOption) (*Project, error) {
	defaults := newDefaultProjectOptions()
	for _, f := range opts {
		f(defaults)
	}

	appName := defaults.AppName
	p := Project{Root: root}

	if path, err := std.UserConfigPath(ctx); err != nil {
		return nil, fmt.Errorf(
			"unable to get find user config path: %w",
			terrors.ErrNotFound,
		)
	} else {
		p.UserConfigRoot = filepath.Join(path, appName)
	}

	if path, err := std.UserDataPath(ctx); err != nil {
		return nil, fmt.Errorf(
			"unable to get find user data path: %w",
			terrors.ErrNotFound,
		)
	} else {
		p.DataRoot = filepath.Join(path, appName)
	}

	if path, err := std.UserStatePath(ctx); err != nil {
		return nil, fmt.Errorf(
			"unable to get find user state root: %w",
			terrors.ErrNotFound,
		)
	} else {
		p.StateRoot = filepath.Join(path, appName)
	}

	if path, err := std.UserCachePath(ctx); err != nil {
		return nil, fmt.Errorf(
			"unable to get find user cache root: %w",
			terrors.ErrNotFound,
		)
	} else {
		p.CacheRoot = filepath.Join(path, appName)
	}

	p.LocalConfigRoot = filepath.Join(root, DefaultLocalConfigDir)

	return &p, nil
}

func (p *Project) DefaultTarget(ctx context.Context) *keg.KegTarget {
	return nil
}
