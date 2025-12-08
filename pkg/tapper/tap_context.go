package tapper

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/jlrickert/cli-toolkit/appctx"

	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

// TapContext is a small wrapper around stdproject.Project that adds cached
// tapper-specific config values and convenience helpers.
type TapContext struct {
	*appctx.AppContext

	// Cached configs.
	dCfg *Config
	sCfg *Config
	uCfg *Config
	lCfg *Config
}

// newTapContext constructs a TapProject backed by github.com/jlrickert/go-std/project.
// It applies any provided Tap ProjectOption values first, then calls into the
// std project constructor to fill platform defaults.
func newTapContext(ctx context.Context, root string) (*TapContext, error) {
	aCtx, err := appctx.NewAppContext(ctx, root, DefaultAppName)
	if err != nil {
		return nil, err
	}
	tCtx := TapContext{
		AppContext: aCtx,
	}
	return &tCtx, nil
}

func (tCtx *TapContext) ConfigPath() string {
	return filepath.Join(tCtx.ConfigRoot, "config.yaml")
}

// Config reads and merges data, state, user, and local configuration files for
// the project. Merge order: data, state, user, local. Later values override
// earlier ones.
func (tCtx *TapContext) Config(ctx context.Context) *Config {
	// Local config
	localCfg, err := ReadConfig(ctx, tCtx.ConfigPath())
	if errors.Is(err, keg.ErrNotExist) {
		localCfg = &Config{}
	} else if err != nil {
		localCfg = &Config{}
	}

	// User config
	userCfg, err := ReadConfig(ctx, filepath.Join(tCtx.ConfigRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		userCfg = &Config{}
	} else if err != nil {
		userCfg = &Config{}
	}

	return MergeConfig(userCfg, localCfg)
}

func (tCtx *TapContext) LocalConfigPath() string {
	return filepath.Join(tCtx.LocalConfigRoot, "config.yaml")
}

// LocalConfig returns the repo-local override configuration. When cache is true
// a previously loaded value may be returned.
func (tCtx *TapContext) LocalConfig(ctx context.Context, cache bool) (*Config, error) {
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
func (tCtx *TapContext) LocalConfigUpdate(ctx context.Context, f func(cfg *Config), cache bool) error {
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
func (tCtx *TapContext) UserConfig(ctx context.Context, cache bool) (*Config, error) {
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
func (tCtx *TapContext) UserConfigUpdate(ctx context.Context, f func(cfg *Config),
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
func (tCtx *TapContext) StateConfig(ctx context.Context, cache bool) (*Config, error) {
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
func (tCtx *TapContext) StateConfigUpdate(ctx context.Context, f func(cfg *Config),
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
func (tCtx *TapContext) DataConfig(ctx context.Context, cache bool) (*Config, error) {
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

// DataConfigUpdate loads the data config, applies f and writes the result.
// When cache is true cached values may be used for read.
func (tCtx *TapContext) DataConfigUpdate(ctx context.Context, f func(cfg *Config),
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
func (tCtx *TapContext) ListKegs(ctx context.Context) []string {
	return tCtx.Config(ctx).ListKegs()
}

// DefaultKeg selects the appropriate keg target for the project root using the
// merged config's project-keg resolution rules.
func (tCtx *TapContext) DefaultKeg(ctx context.Context) (*kegurl.Target, error) {
	cfg := tCtx.Config(ctx)
	local, err := tCtx.LocalConfig(ctx, true)
	if err == nil && local.DefaultKeg() != "" {
		return cfg.ResolveAlias(local.DefaultKeg())
	}
	return cfg.ResolveKegMap(ctx, tCtx.Root)
}

type ResolveKegOpts struct {
	Alias string
}

func (tCtx *TapContext) ResolveKeg(ctx context.Context, opts *ResolveKegOpts) (*kegurl.Target, error) {
	cfg := tCtx.Config(ctx)
	if cfg == nil {
		return nil, nil
	}

	if opts != nil && opts.Alias != "" {
		return cfg.ResolveAlias(opts.Alias)
	}
	target, _ := cfg.ResolveKegMap(ctx, tCtx.Root)
	if target != nil {
		return target, nil
	}

	return cfg.ResolveAlias(cfg.DefaultKeg())
}
