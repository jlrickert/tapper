package tapper

//import (
//	"context"
//	"errors"
//	"fmt"
//	"path/filepath"
//
//	"github.com/jlrickert/cli-toolkit/appctx"
//	"github.com/jlrickert/tapper/pkg/keg"
//	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
//)
//
//// TapApi is a small wrapper around AppContext. Project that adds cached
//// tapper-specific config values and convenience helpers.
//type TapApi struct {
//	*appctx.AppContext
//
//	Paths *PathService
//
//	// Cached configs.
//	dCfg *Config
//	sCfg *Config
//	uCfg *Config
//	lCfg *Config
//
//	cfg *Config
//}
//
//// newTapApi constructs a TapProject backed by github.com/jlrickert/go-std/project.
//// It applies any provided Tap ProjectOption values first, then calls into the
//// std project constructor to fill platform defaults.
//func newTapApi(ctx context.Context, pathService appctx.AppContext) (*TapApi, error) {
//	aCtx, err := appctx.NewAppContext(ctx, pathService.Root, DefaultAppName)
//	if err != nil {
//		return nil, err
//	}
//	tCtx := TapApi{
//		AppContext: aCtx,
//		Paths:      pathService,
//		dCfg:       nil,
//		sCfg:       nil,
//		uCfg:       nil,
//		lCfg:       nil,
//	}
//	return &tCtx, nil
//}
//
//func (api *TapApi) ConfigPath() string {
//	return filepath.Join(api.ConfigRoot, "config.yaml")
//}
//
//func (api *TapApi) LoadConfig(ctx context.Context, path string) (*Config, error) {
//	cfg, err := ReadConfig(ctx, path)
//	if err != nil {
//		return nil, err
//	}
//	api.cfg = cfg
//	return api.cfg, nil
//}
//
//// Config reads and merges data, state, user, and local configuration files for
//// the project. Merge order: data, state, user, local. Later values override
//// earlier ones.
//func (api *TapApi) Config(ctx context.Context, cache bool) *Config {
//	if cache && api.cfg != nil {
//		return api.cfg
//	}
//	lCfg, err := api.LocalConfig(ctx, cache)
//	if err != nil {
//		lCfg = &Config{}
//	}
//	uCfg, err := api.UserConfig(ctx, cache)
//	if err != nil {
//		uCfg = &Config{}
//	}
//
//	api.cfg = MergeConfig(uCfg, lCfg)
//	return api.cfg
//}
//
//func (api *TapApi) LocalConfigPath() string {
//	return filepath.Join(api.LocalConfigRoot, "config.yaml")
//}
//
//// LocalConfig returns the repo-local override configuration. When cache is true
//// a previously loaded value may be returned.
//func (api *TapApi) LocalConfig(ctx context.Context, cache bool) (*Config, error) {
//	if cache && api.lCfg != nil {
//		return api.lCfg, nil
//	}
//	cfg, err := ReadConfig(ctx, filepath.Join(api.LocalConfigRoot, "config.yaml"))
//	if err != nil {
//		// propagate other errors
//		return nil, err
//	}
//	api.lCfg = cfg
//	return cfg, nil
//}
//
//// LocalConfigUpdate loads the local config, applies f and writes the result.
//// When cache is true cached values may be used for read.
//func (api *TapApi) LocalConfigUpdate(ctx context.Context, f func(cfg *Config), cache bool) error {
//	cfg, err := api.LocalConfig(ctx, cache)
//	if errors.Is(err, keg.ErrNotExist) {
//		cfg = &Config{}
//	} else if err != nil {
//		return err
//	}
//	f(cfg)
//	return cfg.Write(ctx, filepath.Join(api.LocalConfigRoot, "config.yaml"))
//}
//
//// UserConfig returns the merged user configuration. When cache is true a
//// previously loaded value may be returned.
//func (api *TapApi) UserConfig(ctx context.Context, cache bool) (*Config, error) {
//	if cache && api.uCfg != nil {
//		return api.uCfg, nil
//	}
//	cfg, err := ReadConfig(ctx, filepath.Join(api.ConfigRoot, "config.yaml"))
//	if err != nil {
//		// propagate other errors
//		return nil, err
//	}
//	api.uCfg = cfg
//	return cfg, nil
//}
//
//// UserConfigUpdate loads the user config, applies f and writes the result. When
//// cache is true cached values may be used for read.
//func (api *TapApi) UserConfigUpdate(ctx context.Context, f func(cfg *Config),
//	cache bool) error {
//	cfg, err := api.UserConfig(ctx, cache)
//	if errors.Is(err, keg.ErrNotExist) {
//		cfg = &Config{data: &configDTO{}}
//	} else if err != nil {
//		return err
//	}
//	f(cfg)
//	return cfg.Write(ctx, filepath.Join(api.ConfigRoot, "config.yaml"))
//}
//
//// StateConfig returns the state configuration. When cache is true a previously
//// loaded value may be returned.
//func (api *TapApi) StateConfig(ctx context.Context, cache bool) (*Config, error) {
//	if cache && api.sCfg != nil {
//		return api.sCfg, nil
//	}
//	cfg, err := ReadConfig(ctx, filepath.Join(api.StateRoot, "config.yaml"))
//	if err != nil {
//		return nil, err
//	}
//	api.sCfg = cfg
//	return cfg, nil
//}
//
//// StateConfigUpdate loads the state config, applies f and writes the result.
//// When cache is true cached values may be used for read.
//func (api *TapApi) StateConfigUpdate(ctx context.Context, f func(cfg *Config),
//	cache bool) error {
//	cfg, err := api.StateConfig(ctx, cache)
//	if errors.Is(err, keg.ErrNotExist) {
//		cfg = &Config{}
//	} else if err != nil {
//		return err
//	}
//	f(cfg)
//	return cfg.Write(ctx, filepath.Join(api.StateRoot, "config.yaml"))
//}
//
//// DataConfig returns the data configuration. When cache is true a previously
//// loaded value may be returned.
//func (api *TapApi) DataConfig(ctx context.Context, cache bool) (*Config, error) {
//	if cache && api.dCfg != nil {
//		return api.dCfg, nil
//	}
//	cfg, err := ReadConfig(ctx, filepath.Join(api.DataRoot, "config.yaml"))
//	if err != nil {
//		return nil, err
//	}
//	api.dCfg = cfg
//	return cfg, nil
//}
//
//// DataConfigUpdate loads the data config, applies f, and writes the result.
//// When the cache is true, cached values may be used for reading.
//func (api *TapApi) DataConfigUpdate(ctx context.Context, f func(cfg *Config),
//	cache bool) error {
//	cfg, err := api.DataConfig(ctx, cache)
//	if errors.Is(err, keg.ErrNotExist) {
//		cfg = &Config{}
//	} else if err != nil {
//		return err
//	}
//	f(cfg)
//	return cfg.Write(ctx, filepath.Join(api.DataRoot, "config.yaml"))
//}
//
//// DefaultKeg selects the appropriate keg target for the project root using the
//// merged config's project-keg resolution rules.
//func (api *TapApi) DefaultKeg(ctx context.Context, cache bool) (*kegurl.Target, error) {
//	cfg := api.Config(ctx, cache)
//	if cfg == nil {
//		return nil, nil
//	}
//	if cfg.DefaultKeg() == "" {
//		return nil, nil
//	}
//	return cfg.ResolveAlias(cfg.DefaultKeg())
//}
//
//// ResolveKeg resolves and returns a keg target based on the provided options, including alias and configuration settings.
//func (api *TapApi) ResolveKeg(ctx context.Context, alias string, cache bool) (*kegurl.Target, error) {
//	cfg := api.Config(ctx, cache)
//	if cfg == nil {
//		return nil, nil
//	}
//
//	return cfg.ResolveAlias(alias)
//}
//
////type ResolveKegOpts struct {
////	Root  string
////	Keg string
////	Cache bool
////}
//
////func (api *TapApi) Target(ctx context.Context, opts *ResolveKegOpts) (*kegurl.Target, error) {
////	if opts == nil {
////		opts = &ResolveKegOpts{Cache: true, Root: api.Root}
////	}
////	if opts.Root == "" {
////		opts.Root = api.Root
////	}
////
////	cfg := api.Config(ctx, opts.Cache)
////	if cfg == nil {
////		return nil, fmt.Errorf("no configuration available")
////	}
////	if opts.Keg != "" {
////		return cfg.ResolveAlias(opts.Keg)
////	}
////	target, err := cfg.ResolveKegMap(ctx, opts.Root)
////	if target != nil && err == nil {
////		return target, err
////	}
////	alias := cfg.DefaultKeg()
////	return cfg.ResolveAlias(alias)
////}
