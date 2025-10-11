package tap

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	std "github.com/jlrickert/go-std/pkg"

	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

// Project holds paths and configuration roots for a repository-backed
// project. Root is the repository root. Other roots default to platform
// user-scoped locations when not provided.
type Project struct {
	// Root is the path to the root of the project.
	Root string

	// ConfigRoot is the base directory for user configuration files.
	ConfigRoot string

	// StateRoot holds transient state files for the project.
	StateRoot string

	// DataRoot is for programmatically managed data shipped with the program.
	DataRoot string

	// CacheRoot is for cache artifacts.
	CacheRoot string

	// LocalConfigRoot is the repo-local override location (for example
	// root/.tapper).
	LocalConfigRoot string

	// dCfg is the programmatically managed data configuration.
	dCfg *Config

	// uCfg is the user-managed configuration.
	uCfg *Config

	// lCfg is the local project configuration.
	lCfg *Config
}

type ProjectOption = func(opt *Project)

// WithStateRoot sets the state root path on the Project.
func WithStateRoot(path string) ProjectOption {
	return func(opt *Project) {
		opt.StateRoot = path
	}
}

// WithConfigRoot sets the config root path on the Project.
func WithConfigRoot(path string) ProjectOption {
	return func(opt *Project) {
		opt.ConfigRoot = path
	}
}

// WithDataRoot sets the data root path on the Project.
func WithDataRoot(path string) ProjectOption {
	return func(opt *Project) {
		opt.DataRoot = path
	}
}

// WithCacheRoot sets the cache root path on the Project.
func WithCacheRoot(path string) ProjectOption {
	return func(opt *Project) {
		opt.CacheRoot = path
	}
}

// WithRoot sets the repository root on the Project.
func WithRoot(path string) ProjectOption {
	return func(opt *Project) {
		opt.Root = path
	}
}

// WithAutoRootDetect returns an option that sets Root by detecting the
// repository top-level directory using the Env from the provided context.
func WithAutoRootDetect(ctx context.Context) ProjectOption {
	return func(opt *Project) {
		env := std.EnvFromContext(ctx)
		wd, _ := env.Getwd()
		root := std.FindGitRoot(ctx, wd)
		opt.Root = root
	}
}

// NewProject constructs a Project and fills missing roots using platform
// defaults derived from the provided context.
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
			return nil, fmt.Errorf("unable to find user config path: %w", keg.ErrNotExist)
		} else {
			p.ConfigRoot = filepath.Join(path, DefaultAppName)
		}
	}

	if p.DataRoot == "" {
		if path, err := std.UserDataPath(ctx); err != nil {
			return nil, fmt.Errorf("unable to find user data path: %w", keg.ErrNotExist)
		} else {
			p.DataRoot = filepath.Join(path, DefaultAppName)
		}
	}

	if p.StateRoot == "" {
		if path, err := std.UserStatePath(ctx); err != nil {
			return nil, fmt.Errorf("unable to find user state root: %w", keg.ErrNotExist)
		} else {
			p.StateRoot = filepath.Join(path, DefaultAppName)
		}
	}

	if p.CacheRoot == "" {
		if path, err := std.UserCachePath(ctx); err != nil {
			return nil, fmt.Errorf("unable to find user cache root: %w", keg.ErrNotExist)
		} else {
			p.CacheRoot = filepath.Join(path, DefaultAppName)
		}
	}

	return p, nil
}

// Config reads and merges data, user, and local configuration files for the
// project. Missing files are replaced with sensible defaults when appropriate.
func (p *Project) Config(ctx context.Context) *Config {
	dataCfg, err := ReadConfig(ctx, filepath.Join(p.DataRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		dataCfg = DefaultDataConfig(ctx)
	}

	userCfg, err := ReadConfig(ctx, filepath.Join(p.ConfigRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		userCfg = DefaultUserConfig(ctx)
	}

	localCfg, err := ReadConfig(ctx, filepath.Join(p.LocalConfigRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		// Local overrides should be empty when missing so they do not
		// unexpectedly override user settings.
		localCfg = &Config{}
	}
	return MergeConfig(dataCfg, userCfg, localCfg)
}

func (p *Project) LocalConfig(ctx context.Context) (*Config, error) {
	return ReadConfig(ctx, filepath.Join(p.LocalConfigRoot, "config.yaml"))
}

// ListKegs returns the list of known keg aliases from the merged config.
// The result is sorted to be deterministic.
func (p *Project) ListKegs(ctx context.Context) []string {
	cfg := p.Config(ctx)
	var xs []string
	for k := range cfg.Kegs {
		xs = append(xs, k)
	}
	sort.Strings(xs)
	return xs
}

// DefaultKeg selects the appropriate keg target for the project root using
// the merged config's project-keg resolution rules.
func (p *Project) DefaultKeg(ctx context.Context) (*kegurl.Target, error) {
	cfg := p.Config(ctx)
	local, err := p.LocalConfig(ctx)
	if err == nil && local.DefaultKeg != "" {
		return cfg.ResolveAlias(ctx, local.DefaultKeg)
	}
	return cfg.ResolveProjectKeg(ctx, p.Root)
}
