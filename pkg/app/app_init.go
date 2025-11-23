package app

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/jlrickert/tapper/pkg/tap"
)

// InitOptions configures behavior for Runner.Init.
type InitOptions struct {
	Name string
	Repo string
	User string

	// Type could be local, user, or registry
	Type string

	// AddConfig adds config to user config
	AddUserConfig bool

	// AddLocalConfig adds the alias to the local project
	AddLocalConfig bool

	Creator string
	Title   string
	Alias   string

	TokenEnv string
}

// DoInit creates a keg entry for the given name.
//
// If name is empty an ErrInvalid-wrapped error is returned. DoInit obtains the
// project via getProject and then performs the actions required to create a
// keg. The current implementation defers to project.DefaultKeg for further
// resolution and returns any error encountered when obtaining the project.
func (r *Runner) DoInit(ctx context.Context, name string, options *InitOptions) error {
	var err error
	var target *kegurl.Target
	switch options.Type {
	case "registry":
		target, err = r.initRegistry(ctx, initRegistryOptions{
			Alias:          options.Alias,
			User:           "",
			Repo:           "",
			AddUserConfig:  options.AddUserConfig,
			AddLocalConfig: options.AddLocalConfig,
			Title:          options.Title,
			Creator:        options.Creator,
		})
	case "user":
		target, err = r.initUserKeg(ctx, options)
	case "local":
		target, err = r.initLocalKeg(ctx, initLocalOptions{
			Alias:          options.Alias,
			AddUserConfig:  options.AddUserConfig,
			AddLocalConfig: options.AddLocalConfig,
			Title:          options.Title,
			Creator:        options.Creator,
		})
	default:
		return fmt.Errorf("%s is an invalid repo type", options.Type)
	}

	if err != nil {
		return err
	}

	cfg := r.project.Config(ctx)
	if cfg == nil || cfg.UserRepoPath == "" {
		return nil
	}
	tapCtx, _ := r.getTapCtx(ctx)
	return tapCtx.UserConfigUpdate(ctx, func(cfg *tap.Config) {
		cfg.AddKeg(ctx, options.Alias, *target)
	}, true)
}

type initLocalOptions struct {
	Alias          string
	AddUserConfig  bool
	AddLocalConfig bool

	Creator string
	Title   string
}

// initLocalKeg creates a filesystem-backed keg repository at path.
//
// If path is empty the current working directory is used. The function uses
// the Env from ctx to resolve the working directory when available and falls
// back to os.Getwd otherwise. The destination directory is created. An initial
// keg configuration is written as YAML to "keg" inside the destination. A
// dex/ directory and a nodes.tsv file containing the zero node entry are
// created. A zero node README is written to "0/README.md".
//
// Errors are wrapped with contextual messages to aid callers.
func (r *Runner) initLocalKeg(ctx context.Context, _ initLocalOptions) (*kegurl.Target, error) {
	proj, err := r.getTapCtx(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to init keg: %w", err)
	}

	target := kegurl.NewFile(proj.Root)
	k, err := keg.NewKegFromTarget(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("unable to init keg: %w", err)
	}
	return &target, k.Init(ctx)
}

type InitFileOptions struct {
	Path string

	AddUserConfig  bool
	AddLocalConfig bool

	Alias   string
	Creator string
	Title   string
}

func (r *Runner) initUserKeg(ctx context.Context, opt *InitOptions) (*kegurl.Target, error) {
	tapCtx, err := r.getTapCtx(ctx)
	if err != nil {
		return nil, err
	}
	cfg := tapCtx.Config(ctx)
	base := cfg.UserRepoPath
	if base == "" {
		return nil, fmt.Errorf("userRepoPath not defined in user config: %w", keg.ErrNotExist)
	}

	kegPath := filepath.Join(base, opt.Name)

	target := kegurl.NewFile(kegPath)
	k, err := keg.NewKegFromTarget(ctx, target)
	err = k.Init(ctx)
	return &target, err
}

type initRegistryOptions struct {
	Repo  string
	User  string
	Alias string

	AddUserConfig  bool
	AddLocalConfig bool

	Creator string
	Title   string
}

func (r *Runner) initRegistry(ctx context.Context, opts initRegistryOptions) (*kegurl.Target, error) {
	if opts.Alias == "" {
		return nil, fmt.Errorf("alias required: %w", keg.ErrInvalid)
	}

	proj, err := r.getTapCtx(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to init registry keg: %w", err)
	}

	// Determine repo (registry) name. Prefer explicit flag, then project config.
	repoName := opts.Repo
	if repoName == "" {
		cfg := proj.Config(ctx)
		if cfg != nil && cfg.DefaultRegistry != "" {
			repoName = cfg.DefaultRegistry
		}
	}
	if repoName == "" {
		// final fallback
		repoName = "knut"
	}

	// Determine user namespace.
	user := opts.User
	if user == "" {
		env := toolkit.EnvFromContext(ctx)
		u, _ := env.GetUser()
		if u != "" {
			user = u
		} else {
			// try to fall back to project-local default if present
			if cfg := proj.Config(ctx); cfg != nil && cfg.DefaultKeg != "" {
				// ignore: best-effort only
				user = cfg.DefaultKeg
			}
		}
		if user == "" {
			user = "user"
		}
	}

	target := kegurl.NewApi(repoName, user, opts.Alias)

	// Optionally write to user/local config.
	if opts.AddUserConfig || opts.AddLocalConfig {
		alias := opts.Alias
		if opts.AddUserConfig {
			if err := proj.UserConfigUpdate(ctx, func(cfg *tap.Config) {
				if cfg.Kegs == nil {
					cfg.Kegs = map[string]kegurl.Target{}
				}
				cfg.Kegs[alias] = target
			}, false); err != nil {
				return nil, fmt.Errorf("unable to write user config: %w", err)
			}
		}
		if opts.AddLocalConfig {
			if err := proj.LocalConfigUpdate(ctx, func(cfg *tap.Config) {
				if cfg.Kegs == nil {
					cfg.Kegs = map[string]kegurl.Target{}
				}
				cfg.Kegs[alias] = target
			}, false); err != nil {
				return nil, fmt.Errorf("unable to write local config: %w", err)
			}
		}
	}

	return &target, nil
}
