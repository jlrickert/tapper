package tapper

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/jlrickert/cli-toolkit/appctx"

	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

// TapApi is a small wrapper around stdproject.Project that adds cached
// tapper-specific config values and convenience helpers.
type TapApi struct {
	*appctx.AppContext

	// Cached configs.
	dCfg *Config
	sCfg *Config
	uCfg *Config
	lCfg *Config

	cfg *Config
}

// newTapContext constructs a TapProject backed by github.com/jlrickert/go-std/project.
// It applies any provided Tap ProjectOption values first, then calls into the
// std project constructor to fill platform defaults.
func newTapContext(ctx context.Context, root string) (*TapApi, error) {
	aCtx, err := appctx.NewAppContext(ctx, root, DefaultAppName)
	if err != nil {
		return nil, err
	}
	tCtx := TapApi{
		AppContext: aCtx,
		dCfg:       nil,
		sCfg:       nil,
		uCfg:       nil,
		lCfg:       nil,
	}
	return &tCtx, nil
}

func (tCtx *TapApi) ConfigPath() string {
	return filepath.Join(tCtx.ConfigRoot, "config.yaml")
}

func (tCtx *TapApi) LoadConfig(ctx context.Context, path string) (*Config, error) {
	cfg, err := ReadConfig(ctx, path)
	if err != nil {
		return nil, err
	}
	tCtx.cfg = cfg
	return tCtx.cfg, nil
}

// Config reads and merges data, state, user, and local configuration files for
// the project. Merge order: data, state, user, local. Later values override
// earlier ones.
func (tCtx *TapApi) Config(ctx context.Context, cache bool) *Config {
	if tCtx.cfg != nil && !cache {
		return tCtx.cfg
	}
	lCfg, err := tCtx.LocalConfig(ctx, cache)
	if err != nil {
		lCfg = &Config{}
	}
	uCfg, err := tCtx.UserConfig(ctx, cache)
	if err != nil {
		uCfg = &Config{}
	}

	tCtx.cfg = MergeConfig(uCfg, lCfg)
	return tCtx.cfg
}

func (tCtx *TapApi) LocalConfigPath() string {
	return filepath.Join(tCtx.LocalConfigRoot, "config.yaml")
}

// LocalConfig returns the repo-local override configuration. When cache is true
// a previously loaded value may be returned.
func (tCtx *TapApi) LocalConfig(ctx context.Context, cache bool) (*Config, error) {
	if cache && tCtx.lCfg != nil {
		return tCtx.lCfg, nil
	}
	cfg, err := ReadConfig(ctx, filepath.Join(tCtx.LocalConfigRoot, "config.yaml"))
	if err != nil {
		// propagate other errors
		return nil, err
	}
	tCtx.lCfg = cfg
	return cfg, nil
}

// LocalConfigUpdate loads the local config, applies f and writes the result.
// When cache is true cached values may be used for read.
func (tCtx *TapApi) LocalConfigUpdate(ctx context.Context, f func(cfg *Config), cache bool) error {
	cfg, err := tCtx.LocalConfig(ctx, cache)
	if errors.Is(err, keg.ErrNotExist) {
		cfg = &Config{}
	} else if err != nil {
		return err
	}
	f(cfg)
	return cfg.Write(ctx, filepath.Join(tCtx.LocalConfigRoot, "config.yaml"))
}

// UserConfig returns the merged user configuration. When cache is true a
// previously loaded value may be returned.
func (tCtx *TapApi) UserConfig(ctx context.Context, cache bool) (*Config, error) {
	if cache && tCtx.uCfg != nil {
		return tCtx.uCfg, nil
	}
	cfg, err := ReadConfig(ctx, filepath.Join(tCtx.ConfigRoot, "config.yaml"))
	if err != nil {
		// propagate other errors
		return nil, err
	}
	tCtx.uCfg = cfg
	return cfg, nil
}

// UserConfigUpdate loads the user config, applies f and writes the result. When
// cache is true cached values may be used for read.
func (tCtx *TapApi) UserConfigUpdate(ctx context.Context, f func(cfg *Config),
	cache bool) error {
	cfg, err := tCtx.UserConfig(ctx, cache)
	if errors.Is(err, keg.ErrNotExist) {
		cfg = &Config{data: &configDTO{}}
	} else if err != nil {
		return err
	}
	f(cfg)
	return cfg.Write(ctx, filepath.Join(tCtx.ConfigRoot, "config.yaml"))
}

// StateConfig returns the state configuration. When cache is true a previously
// loaded value may be returned.
func (tCtx *TapApi) StateConfig(ctx context.Context, cache bool) (*Config, error) {
	if cache && tCtx.sCfg != nil {
		return tCtx.sCfg, nil
	}
	cfg, err := ReadConfig(ctx, filepath.Join(tCtx.StateRoot, "config.yaml"))
	if err != nil {
		return nil, err
	}
	tCtx.sCfg = cfg
	return cfg, nil
}

// StateConfigUpdate loads the state config, applies f and writes the result.
// When cache is true cached values may be used for read.
func (tCtx *TapApi) StateConfigUpdate(ctx context.Context, f func(cfg *Config),
	cache bool) error {
	cfg, err := tCtx.StateConfig(ctx, cache)
	if errors.Is(err, keg.ErrNotExist) {
		cfg = &Config{}
	} else if err != nil {
		return err
	}
	f(cfg)
	return cfg.Write(ctx, filepath.Join(tCtx.StateRoot, "config.yaml"))
}

// DataConfig returns the data configuration. When cache is true a previously
// loaded value may be returned.
func (tCtx *TapApi) DataConfig(ctx context.Context, cache bool) (*Config, error) {
	if cache && tCtx.dCfg != nil {
		return tCtx.dCfg, nil
	}
	cfg, err := ReadConfig(ctx, filepath.Join(tCtx.DataRoot, "config.yaml"))
	if err != nil {
		return nil, err
	}
	tCtx.dCfg = cfg
	return cfg, nil
}

// DataConfigUpdate loads the data config, applies f, and writes the result.
// When the cache is true, cached values may be used for reading.
func (tCtx *TapApi) DataConfigUpdate(ctx context.Context, f func(cfg *Config),
	cache bool) error {
	cfg, err := tCtx.DataConfig(ctx, cache)
	if errors.Is(err, keg.ErrNotExist) {
		cfg = &Config{}
	} else if err != nil {
		return err
	}
	f(cfg)
	return cfg.Write(ctx, filepath.Join(tCtx.DataRoot, "config.yaml"))
}

// ListKegs returns the list of known keg aliases from the merged config.
// The result is sorted to be deterministic.
func (tCtx *TapApi) ListKegs(ctx context.Context, cache bool) []string {
	return tCtx.Config(ctx, cache).ListKegs()
}

// DefaultKeg selects the appropriate keg target for the project root using the
// merged config's project-keg resolution rules.
func (tCtx *TapApi) DefaultKeg(ctx context.Context, cache bool) (*kegurl.Target, error) {
	cfg := tCtx.Config(ctx, cache)
	if cfg == nil {
		return nil, nil
	}
	if cfg.DefaultKeg() == "" {
		return nil, nil
	}
	return cfg.ResolveAlias(cfg.DefaultKeg())
}

// ResolveKeg resolves and returns a keg target based on the provided options, including alias and configuration settings.
func (tCtx *TapApi) ResolveKeg(ctx context.Context, alias string, cache bool) (*kegurl.Target, error) {
	cfg := tCtx.Config(ctx, cache)
	if cfg == nil {
		return nil, nil
	}

	return cfg.ResolveAlias(alias)
}

type ResolveKegOpts struct {
	Root  string
	Alias string
	Cache bool
}

func (tCtx *TapApi) Target(ctx context.Context, opts *ResolveKegOpts) (*kegurl.Target, error) {
	if opts == nil {
		opts = &ResolveKegOpts{Cache: true, Root: tCtx.Root}
	}
	if opts.Root == "" {
		opts.Root = tCtx.Root
	}

	cfg := tCtx.Config(ctx, opts.Cache)
	if cfg == nil {
		return nil, fmt.Errorf("no configuration available")
	}
	if opts.Alias != "" {
		return cfg.ResolveAlias(opts.Alias)
	}
	target, err := cfg.ResolveKegMap(ctx, opts.Root)
	if target != nil && err == nil {
		return target, err
	}
	alias := cfg.DefaultKeg()
	return cfg.ResolveAlias(alias)
}
