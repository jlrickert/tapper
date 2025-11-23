package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/jlrickert/tapper/pkg/tap"
)

// InitOptions configures behavior for Runner.Init.
type InitOptions struct {
	// Type could be local, file, or registry
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

// Init creates a keg entry for the given name.
//
// If name is empty an ErrInvalid-wrapped error is returned. Init obtains the
// project via getProject and then performs the actions required to create a
// keg. The current implementation defers to project.DefaultKeg for further
// resolution and returns any error encountered when obtaining the project.
func (r *Runner) Init(ctx context.Context, name string, options *InitOptions) error {
	switch options.Type {
	case "registry":
	case "file":
		cfg := r.project.Config(ctx)
		if cfg == nil || cfg.UserRepoPath == "" {
			return fmt.Errorf("userRepoPath not configured: %w", keg.ErrNotExist)
		}
		r.initFile(ctx, InitFileOptions{
			Alias:          options.Alias,
			Path:           cfg.UserRepoPath,
			AddUserConfig:  options.AddUserConfig,
			AddLocalConfig: options.AddLocalConfig,
			Title:          options.Title,
			Creator:        options.Creator,
		})
	case "local":
		return r.initLocal(ctx, initLocalOptions{
			Alias:          options.Alias,
			AddUserConfig:  options.AddUserConfig,
			AddLocalConfig: options.AddLocalConfig,
			Title:          options.Title,
			Creator:        options.Creator,
		})
	default:
		if name == "." {
			return r.initLocal(ctx, initLocalOptions{
				Alias:          options.Alias,
				AddUserConfig:  options.AddUserConfig,
				AddLocalConfig: options.AddLocalConfig,
				Creator:        options.Creator,
				Title:          options.Title,
			})
		}
		u, err := kegurl.Parse(name)
		if err != nil {
			return fmt.Errorf("unable to init keg: %w", err)
		}
		switch u.Scheme() {
		case kegurl.SchemeFile:
			return r.initFile(ctx, InitFileOptions{
				Path:           u.Path(),
				AddUserConfig:  options.AddUserConfig,
				AddLocalConfig: options.AddLocalConfig,
				Alias:          options.Alias,
				Creator:        options.Creator,
				Title:          options.Title,
			})
		case kegurl.SchemeRegistry:
		}
		u.Scheme()
	}
	if name == "." || name == "" {
		env := toolkit.EnvFromContext(ctx)
		pwd, err := env.Getwd()
		if err != nil {
			return err
		}
		return r.initLocal(ctx, initLocalOptions{
			Alias:          pwd,
			AddUserConfig:  options.AddUserConfig,
			AddLocalConfig: options.AddLocalConfig,

			Title:   options.Title,
			Creator: options.Type,
		})
	}
	if name == "" {
		return fmt.Errorf("name required: %w", keg.ErrInvalid)
	}
	project, err := r.getProject(ctx)
	if err != nil {
		return err
	}
	project.DefaultKeg(ctx)
	return nil
}

type initLocalOptions struct {
	Alias          string
	AddUserConfig  bool
	AddLocalConfig bool

	Creator string
	Title   string
}

// initLocal creates a filesystem-backed keg repository at path.
//
// If path is empty the current working directory is used. The function uses
// the Env from ctx to resolve the working directory when available and falls
// back to os.Getwd otherwise. The destination directory is created. An initial
// keg configuration is written as YAML to "keg" inside the destination. A
// dex/ directory and a nodes.tsv file containing the zero node entry are
// created. A zero node README is written to "0/README.md".
//
// Errors are wrapped with contextual messages to aid callers.
func (r *Runner) initLocal(ctx context.Context, opts initLocalOptions) error {
	proj, err := r.getProject(ctx)
	if err != nil {
		return fmt.Errorf("unable to init keg: %w", err)
	}

	k, err := keg.NewKegFromTarget(ctx, kegurl.NewFile(proj.Root))
	if err != nil {
		return fmt.Errorf("unable to init keg: %w", err)
	}
	return k.Init(ctx)
}

type InitFileOptions struct {
	Path string

	AddUserConfig  bool
	AddLocalConfig bool

	Alias   string
	Creator string
	Title   string
}

func (r *Runner) initFile(ctx context.Context, opt InitFileOptions) error {
	// Determine the directory that will host the keg repo. We accept either:
	// - explicit Path (file or directory) or
	// - default location under user data: $XDG_DATA_HOME/kegs/@user/<alias>
	var repoDir string

	env := toolkit.EnvFromContext(ctx)

	if opt.Path != "" {
		p := filepath.Clean(opt.Path)
		// If the provided path looks like a file (base == "keg"), treat the parent
		// as the repo directory. Otherwise treat path as a directory and place a
		// "keg" file inside it.
		if filepath.Base(p) == "keg" {
			repoDir = filepath.Dir(p)
		} else {
			repoDir = p
		}
	} else {
		// Path not provided: derive user and place under user data path.
		user, _ := env.GetUser()
		if user == "" {
			// Fallback to a sensible default username.
			user = "user"
		}
		base, err := toolkit.UserDataPath(ctx)
		if err != nil {
			return fmt.Errorf("unable to determine user data path: %w", err)
		}
		alias := opt.Alias
		if alias == "" {
			return fmt.Errorf("alias required when path not provided: %w", keg.ErrInvalid)
		}
		repoDir = filepath.Join(base, "kegs", "@"+user, alias)
	}

	// Ensure directory exists.
	if err := toolkit.Mkdir(ctx, repoDir, os.FileMode(0o755), true); err != nil {
		return fmt.Errorf("unable to create keg directory %s: %w", repoDir, err)
	}

	// Initialize the keg repository at the target directory.
	k, err := keg.NewKegFromTarget(ctx, kegurl.NewFile(repoDir))
	if err != nil {
		return fmt.Errorf("unable to init keg from target: %w", err)
	}
	if err := k.Init(ctx); err != nil {
		return fmt.Errorf("unable to init keg: %w", err)
	}

	// Optionally update user or local config with the created target.
	if opt.AddUserConfig || opt.AddLocalConfig {
		proj, err := r.getProject(ctx)
		if err != nil {
			return fmt.Errorf("unable to update config: %w", err)
		}
		// Build target to store in config: use the repo directory as a file-style
		// target (convention is to point at the repo root for file targets).
		target := kegurl.NewFile(repoDir)
		alias := opt.Alias
		if alias == "" {
			// When alias missing, prefer the directory base.
			alias = filepath.Base(repoDir)
		}
		if opt.AddUserConfig {
			if err := proj.UserConfigUpdate(ctx, func(cfg *tap.Config) {
				if cfg.Kegs == nil {
					cfg.Kegs = map[string]kegurl.Target{}
				}
				cfg.Kegs[alias] = target
			}, false); err != nil {
				return fmt.Errorf("unable to write user config: %w", err)
			}
		}
		if opt.AddLocalConfig {
			if err := proj.LocalConfigUpdate(ctx, func(cfg *tap.Config) {
				// LocalConfig uses a different struct but helper accepts *tap.Config
				if cfg.Kegs == nil {
					cfg.Kegs = map[string]kegurl.Target{}
				}
				cfg.Kegs[alias] = target
			}, false); err != nil {
				return fmt.Errorf("unable to write local config: %w", err)
			}
		}
	}

	return nil
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

func (r *Runner) initRegistry(ctx context.Context, opts initRegistryOptions) error {
	if opts.Alias == "" {
		return fmt.Errorf("alias required: %w", keg.ErrInvalid)
	}

	proj, err := r.getProject(ctx)
	if err != nil {
		return fmt.Errorf("unable to init registry keg: %w", err)
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
				return fmt.Errorf("unable to write user config: %w", err)
			}
		}
		if opts.AddLocalConfig {
			if err := proj.LocalConfigUpdate(ctx, func(cfg *tap.Config) {
				if cfg.Kegs == nil {
					cfg.Kegs = map[string]kegurl.Target{}
				}
				cfg.Kegs[alias] = target
			}, false); err != nil {
				return fmt.Errorf("unable to write local config: %w", err)
			}
		}
	}

	return nil
}
