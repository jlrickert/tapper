package tap

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	proj "github.com/jlrickert/cli-toolkit/project"

	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

// TapProject is a small wrapper around stdproject.Project that adds cached
// tapper-specific config values and convenience helpers.
type TapProject struct {
	// Underlying std project (unexported).
	stdProj *proj.Project

	// Optional overrides applied before constructing the std project.
	root       string
	configRoot string
	stateRoot  string
	dataRoot   string
	cacheRoot  string

	// Make auto root detection public so callers can inspect or set it.
	AutoRootDetect bool

	// Cached configs.
	dCfg *Config
	sCfg *Config
	uCfg *Config
	lCfg *Config
}

type ProjectOption = func(ctx context.Context, p *TapProject)

// WithStateRoot sets the state root path on the Project.
func WithStateRoot(path string) ProjectOption {
	return func(ctx context.Context, p *TapProject) {
		p.stateRoot = path
	}
}

// WithConfigRoot sets the config root path on the Project.
func WithConfigRoot(path string) ProjectOption {
	return func(ctx context.Context, p *TapProject) {
		p.configRoot = path
	}
}

// WithDataRoot sets the data root path on the Project.
func WithDataRoot(path string) ProjectOption {
	return func(ctx context.Context, p *TapProject) {
		p.dataRoot = path
	}
}

// WithCacheRoot sets the cache root path on the Project.
func WithCacheRoot(path string) ProjectOption {
	return func(ctx context.Context, p *TapProject) {
		p.cacheRoot = path
	}
}

// WithRoot sets the repository root on the Project.
func WithRoot(path string) ProjectOption {
	return func(ctx context.Context, p *TapProject) {
		p.root = path
	}
}

// WithAutoRootDetect requests automatic repository root detection when
// constructing the underlying std project.
func WithAutoRootDetect() ProjectOption {
	return func(ctx context.Context, p *TapProject) {
		p.AutoRootDetect = true
	}
}

// NewProject constructs a TapProject backed by github.com/jlrickert/go-std/project.
// It applies any provided Tap ProjectOption values first, then calls into the
// std project constructor to fill platform defaults.
func NewProject(ctx context.Context, opts ...ProjectOption) (*TapProject, error) {
	tp := &TapProject{}
	for _, f := range opts {
		f(ctx, tp)
	}

	var stdOpts []proj.ProjectOption
	if tp.root != "" {
		stdOpts = append(stdOpts, proj.WithRoot(tp.root))
	}
	if tp.configRoot != "" {
		stdOpts = append(stdOpts, proj.WithConfigRoot(tp.configRoot))
	}
	if tp.stateRoot != "" {
		stdOpts = append(stdOpts, proj.WithStateRoot(tp.stateRoot))
	}
	if tp.dataRoot != "" {
		stdOpts = append(stdOpts, proj.WithDataRoot(tp.dataRoot))
	}
	if tp.cacheRoot != "" {
		stdOpts = append(stdOpts, proj.WithCacheRoot(tp.cacheRoot))
	}
	if tp.AutoRootDetect {
		stdOpts = append(stdOpts, proj.WithAutoRootDetect())
	}
	// Construct the underlying std project using the tapper app name.
	p, err := proj.NewProject(ctx, DefaultAppName, stdOpts...)
	if err != nil {
		return nil, err
	}
	tp.stdProj = p
	return tp, nil
}

// Root returns the effective project root for the TapProject. If an explicit
// override was provided it is returned; otherwise the underlying std project
// root is returned when available.
func (p *TapProject) Root() string {
	return p.stdProj.Root
}

// ConfigRoot returns the effective config root. If an explicit override was
// provided it is returned; otherwise the underlying std project's config root
// is returned when available.
func (p *TapProject) ConfigRoot(ctx context.Context) (string, error) {
	return p.stdProj.ConfigRoot(ctx)
}

// StateRoot returns the effective state root. If an explicit override was
// provided it is returned; otherwise the underlying std project's state root
// is returned when available.
func (p *TapProject) StateRoot(ctx context.Context) (string, error) {
	return p.stdProj.StateRoot(ctx)
}

// DataRoot returns the effective data root. If an explicit override was
// provided it is returned; otherwise the underlying std project's data root
// is returned when available.
func (p *TapProject) DataRoot(ctx context.Context) (string, error) {
	return p.stdProj.DataRoot(ctx)
}

// CacheRoot returns the effective cache root. If an explicit override was
// provided it is returned; otherwise the underlying std project's cache root
// is returned when available.
func (p *TapProject) CacheRoot(ctx context.Context) (string, error) {
	return p.stdProj.CacheRoot(ctx)
}

// Config reads and merges data, state, user, and local configuration files for
// the project. Merge order: data, state, user, local. Later values override
// earlier ones.
func (p *TapProject) Config(ctx context.Context) *Config {
	// Data config
	dataRoot, _ := p.stdProj.DataRoot(ctx)
	dataCfg, err := ReadConfig(ctx, filepath.Join(dataRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		dataCfg = &Config{}
	} else if err != nil {
		dataCfg = &Config{}
	}

	// State config
	stateRoot, _ := p.stdProj.StateRoot(ctx)
	stateCfg, err := ReadConfig(ctx, filepath.Join(stateRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		stateCfg = &Config{}
	} else if err != nil {
		stateCfg = &Config{}
	}

	// User config
	cfgRoot, _ := p.stdProj.ConfigRoot(ctx)
	userCfg, err := ReadConfig(ctx, filepath.Join(cfgRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		userCfg = DefaultUserConfig(ctx)
	} else if err != nil {
		userCfg = &Config{}
	}

	// Local config
	localRoot, _ := p.stdProj.LocalConfigRoot(ctx)
	localCfg, err := ReadConfig(ctx, filepath.Join(localRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		localCfg = &Config{}
	} else if err != nil {
		localCfg = &Config{}
	}

	return MergeConfig(dataCfg, stateCfg, userCfg, localCfg)
}

// LocalConfig returns the repo-local override configuration. When cache is true
// a previously loaded value may be returned.
func (p *TapProject) LocalConfig(ctx context.Context, cache bool) (*Config, error) {
	if cache && p.lCfg != nil {
		return p.lCfg, nil
	}
	localRoot, _ := p.stdProj.LocalConfigRoot(ctx)
	cfg, err := ReadConfig(ctx, filepath.Join(localRoot, filepath.Join(localRoot, "config.yaml")))
	if errors.Is(err, keg.ErrNotExist) {
		cfg = DefaultUserConfig(ctx)
	}
	if err != nil {
		// propagate other errors
		return nil, err
	}
	p.lCfg = cfg
	return cfg, nil
}

// LocalConfigUpdate loads the local config, applies f and writes the result.
// When cache is true cached values may be used for read.
func (p *TapProject) LocalConfigUpdate(ctx context.Context, f func(cfg *Config), cache bool) error {
	cfg, err := p.LocalConfig(ctx, cache)
	if errors.Is(err, keg.ErrNotExist) {
		cfg = &Config{}
	} else if err != nil {
		return err
	}
	f(cfg)
	localRoot, _ := p.stdProj.LocalConfigRoot(ctx)
	return cfg.Write(ctx, filepath.Join(localRoot, "config.yaml"))
}

// UserConfig returns the merged user configuration. When cache is true a
// previously loaded value may be returned.
func (p *TapProject) UserConfig(ctx context.Context, cache bool) (*Config, error) {
	if cache && p.uCfg != nil {
		return p.uCfg, nil
	}
	cfgRoot, _ := p.stdProj.ConfigRoot(ctx)
	cfg, err := ReadConfig(ctx, filepath.Join(cfgRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		cfg = DefaultUserConfig(ctx)
	}
	if err != nil {
		// propagate other errors
		return nil, err
	}
	p.uCfg = cfg
	return cfg, nil
}

// UserConfigUpdate loads the user config, applies f and writes the result. When
// cache is true cached values may be used for read.
func (p *TapProject) UserConfigUpdate(ctx context.Context, f func(cfg *Config),
	cache bool) error {
	cfg, err := p.UserConfig(ctx, cache)
	if errors.Is(err, keg.ErrNotExist) {
		cfg = &Config{}
	} else if err != nil {
		return err
	}
	f(cfg)
	cfgRoot, _ := p.stdProj.ConfigRoot(ctx)
	fmt.Println(cfg)
	return cfg.Write(ctx, filepath.Join(cfgRoot, "config.yaml"))
}

// StateConfig returns the state configuration. When cache is true a previously
// loaded value may be returned.
func (p *TapProject) StateConfig(ctx context.Context, cache bool) (*Config, error) {
	if cache && p.sCfg != nil {
		return p.sCfg, nil
	}
	stateRoot, _ := p.stdProj.StateRoot(ctx)
	cfg, err := ReadConfig(ctx, filepath.Join(stateRoot, "config.yaml"))
	if err != nil {
		return nil, err
	}
	p.sCfg = cfg
	return cfg, nil
}

// StateConfigUpdate loads the state config, applies f and writes the result.
// When cache is true cached values may be used for read.
func (p *TapProject) StateConfigUpdate(ctx context.Context, f func(cfg *Config),
	cache bool) error {
	cfg, err := p.StateConfig(ctx, cache)
	if errors.Is(err, keg.ErrNotExist) {
		cfg = &Config{}
	} else if err != nil {
		return err
	}
	f(cfg)
	stateRoot, _ := p.stdProj.StateRoot(ctx)
	return cfg.Write(ctx, filepath.Join(stateRoot, "config.yaml"))
}

// DataConfig returns the data configuration. When cache is true a previously
// loaded value may be returned.
func (p *TapProject) DataConfig(ctx context.Context, cache bool) (*Config, error) {
	if cache && p.dCfg != nil {
		return p.dCfg, nil
	}
	dataRoot, _ := p.stdProj.DataRoot(ctx)
	cfg, err := ReadConfig(ctx, filepath.Join(dataRoot, "config.yaml"))
	if err != nil {
		return nil, err
	}
	p.dCfg = cfg
	return cfg, nil
}

// DataConfigUpdate loads the data config, applies f and writes the result.
// When cache is true cached values may be used for read.
func (p *TapProject) DataConfigUpdate(ctx context.Context, f func(cfg *Config),
	cache bool) error {
	cfg, err := p.DataConfig(ctx, cache)
	if errors.Is(err, keg.ErrNotExist) {
		cfg = &Config{}
	} else if err != nil {
		return err
	}
	f(cfg)
	dataRoot, _ := p.stdProj.DataRoot(ctx)
	return cfg.Write(ctx, filepath.Join(dataRoot, "config.yaml"))
}

// ListKegs returns the list of known keg aliases from the merged config.
// The result is sorted to be deterministic.
func (p *TapProject) ListKegs(ctx context.Context) []string {
	cfg := p.Config(ctx)
	var xs []string
	for k := range cfg.Kegs {
		xs = append(xs, k)
	}
	sort.Strings(xs)
	return xs
}

// DefaultKeg selects the appropriate keg target for the project root using the
// merged config's project-keg resolution rules.
func (p *TapProject) DefaultKeg(ctx context.Context) (*kegurl.Target, error) {
	cfg := p.Config(ctx)
	local, err := p.LocalConfig(ctx, true)
	if err == nil && local.DefaultKeg != "" {
		return cfg.ResolveAlias(ctx, local.DefaultKeg)
	}
	return cfg.ResolveProjectKeg(ctx, p.Root())
}
