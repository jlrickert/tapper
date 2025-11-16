package tap

import (
	"context"
	"errors"
	"path/filepath"
	"sort"

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

// NewTapContext constructs a TapProject backed by github.com/jlrickert/go-std/project.
// It applies any provided Tap ProjectOption values first, then calls into the
// std project constructor to fill platform defaults.
func NewTapContext(ctx context.Context, root string) (*TapContext, error) {
	aCtx, err := appctx.NewAppContext(ctx, DefaultAppName)
	if err != nil {
		return nil, err
	}
	aCtx.Root = root
	tCtx := TapContext{
		AppContext: aCtx,
	}
	return &tCtx, nil
}

// Config reads and merges data, state, user, and local configuration files for
// the project. Merge order: data, state, user, local. Later values override
// earlier ones.
func (p *TapContext) Config(ctx context.Context) *Config {
	// Data config
	dataCfg, err := ReadConfig(ctx, filepath.Join(p.DataRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		dataCfg = &Config{}
	} else if err != nil {
		dataCfg = &Config{}
	}

	// State config
	stateCfg, err := ReadConfig(ctx, filepath.Join(p.StateRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		stateCfg = &Config{}
	} else if err != nil {
		stateCfg = &Config{}
	}

	// User config
	userCfg, err := ReadConfig(ctx, filepath.Join(p.ConfigRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		userCfg = DefaultUserConfig(ctx)
	} else if err != nil {
		userCfg = &Config{}
	}

	// Local config
	localCfg, err := ReadConfig(ctx, filepath.Join(p.LocalConfigRoot, "config.yaml"))
	if errors.Is(err, keg.ErrNotExist) {
		localCfg = &Config{}
	} else if err != nil {
		localCfg = &Config{}
	}

	return MergeConfig(dataCfg, stateCfg, userCfg, localCfg)
}

// LocalConfig returns the repo-local override configuration. When cache is true
// a previously loaded value may be returned.
func (p *TapContext) LocalConfig(ctx context.Context, cache bool) (*Config, error) {
	if cache && p.lCfg != nil {
		return p.lCfg, nil
	}
	cfg, err := ReadConfig(ctx, filepath.Join(p.LocalConfigRoot, "config.yaml"))
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
func (p *TapContext) LocalConfigUpdate(ctx context.Context, f func(cfg *Config), cache bool) error {
	cfg, err := p.LocalConfig(ctx, cache)
	if errors.Is(err, keg.ErrNotExist) {
		cfg = &Config{}
	} else if err != nil {
		return err
	}
	f(cfg)
	return cfg.Write(ctx, filepath.Join(p.LocalConfigRoot, "config.yaml"))
}

// UserConfig returns the merged user configuration. When cache is true a
// previously loaded value may be returned.
func (p *TapContext) UserConfig(ctx context.Context, cache bool) (*Config, error) {
	if cache && p.uCfg != nil {
		return p.uCfg, nil
	}
	cfg, err := ReadConfig(ctx, filepath.Join(p.ConfigRoot, "config.yaml"))
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
func (p *TapContext) UserConfigUpdate(ctx context.Context, f func(cfg *Config),
	cache bool) error {
	cfg, err := p.UserConfig(ctx, cache)
	if errors.Is(err, keg.ErrNotExist) {
		cfg = &Config{}
	} else if err != nil {
		return err
	}
	f(cfg)
	return cfg.Write(ctx, filepath.Join(p.ConfigRoot, "config.yaml"))
}

// StateConfig returns the state configuration. When cache is true a previously
// loaded value may be returned.
func (p *TapContext) StateConfig(ctx context.Context, cache bool) (*Config, error) {
	if cache && p.sCfg != nil {
		return p.sCfg, nil
	}
	cfg, err := ReadConfig(ctx, filepath.Join(p.StateRoot, "config.yaml"))
	if err != nil {
		return nil, err
	}
	p.sCfg = cfg
	return cfg, nil
}

// StateConfigUpdate loads the state config, applies f and writes the result.
// When cache is true cached values may be used for read.
func (p *TapContext) StateConfigUpdate(ctx context.Context, f func(cfg *Config),
	cache bool) error {
	cfg, err := p.StateConfig(ctx, cache)
	if errors.Is(err, keg.ErrNotExist) {
		cfg = &Config{}
	} else if err != nil {
		return err
	}
	f(cfg)
	return cfg.Write(ctx, filepath.Join(p.StateRoot, "config.yaml"))
}

// DataConfig returns the data configuration. When cache is true a previously
// loaded value may be returned.
func (p *TapContext) DataConfig(ctx context.Context, cache bool) (*Config, error) {
	if cache && p.dCfg != nil {
		return p.dCfg, nil
	}
	cfg, err := ReadConfig(ctx, filepath.Join(p.DataRoot, "config.yaml"))
	if err != nil {
		return nil, err
	}
	p.dCfg = cfg
	return cfg, nil
}

// DataConfigUpdate loads the data config, applies f and writes the result.
// When cache is true cached values may be used for read.
func (p *TapContext) DataConfigUpdate(ctx context.Context, f func(cfg *Config),
	cache bool) error {
	cfg, err := p.DataConfig(ctx, cache)
	if errors.Is(err, keg.ErrNotExist) {
		cfg = &Config{}
	} else if err != nil {
		return err
	}
	f(cfg)
	return cfg.Write(ctx, filepath.Join(p.DataRoot, "config.yaml"))
}

// ListKegs returns the list of known keg aliases from the merged config.
// The result is sorted to be deterministic.
func (p *TapContext) ListKegs(ctx context.Context) []string {
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
func (p *TapContext) DefaultKeg(ctx context.Context) (*kegurl.Target, error) {
	cfg := p.Config(ctx)
	local, err := p.LocalConfig(ctx, true)
	if err == nil && local.DefaultKeg != "" {
		return cfg.ResolveAlias(ctx, local.DefaultKeg)
	}
	return cfg.ResolveProjectKeg(ctx, p.Root)
}
