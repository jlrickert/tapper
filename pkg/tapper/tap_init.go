package tapper

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	appCtx "github.com/jlrickert/cli-toolkit/apppaths"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

type InitOptions struct {
	// Destination selection. Exactly one may be true.
	Project  bool
	User     bool
	Registry bool

	// For project destination: when true, use cwd as the project root base.
	// Otherwise git root is preferred (falling back to cwd).
	Cwd bool

	// Registry-specific options.
	Repo     string // registry name
	UserName string // registry namespace
	TokenEnv string

	// Keg name (user destination)
	Name string

	// Explicit filesystem path for local destinations.
	Path string

	Creator string
	Title   string
	Keg     string
}

// InitKeg creates a keg entry for the given name.
//
// If the name is empty, an ErrInvalid-wrapped error is returned. InitKeg gets the
// project via getProject and then performs the actions required to create a
// keg. The current implementation defers to project.DefaultKeg for further
// resolution and returns any error encountered when obtaining the project.
func (t *Tap) InitKeg(ctx context.Context, name string, options InitOptions) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("name is required: %w", keg.ErrInvalid)
	}

	enabled := 0
	if options.Project {
		enabled++
	}
	if options.User {
		enabled++
	}
	if options.Registry {
		enabled++
	}
	if enabled > 1 {
		return fmt.Errorf("only one destination may be selected: --project, --user, or --registry")
	}
	if options.Cwd && !options.Project {
		return fmt.Errorf("--cwd can only be used with --project")
	}

	alias := strings.TrimSpace(options.Keg)
	if alias == "" {
		switch name {
		case ".":
			cwd, err := t.Runtime.Getwd()
			if err != nil {
				return fmt.Errorf("unable to determine working directory for alias inference: %w", err)
			}
			alias = filepath.Base(cwd)
		default:
			alias = filepath.Base(name)
		}
	}
	if alias == "" {
		return fmt.Errorf("keg alias is required: %w", keg.ErrInvalid)
	}
	options.Keg = alias

	destination := "user"
	switch {
	case options.Project:
		destination = "project"
	case options.Registry:
		destination = "registry"
	case options.User:
		destination = "user"
	}

	var err error
	switch destination {
	case "registry":
		_, err = t.initRegistry(initRegistryOptions{
			Alias:         options.Keg,
			User:          options.UserName,
			Repo:          options.Repo,
			AddUserConfig: true,
			Title:         options.Title,
			Creator:       options.Creator,
		})
	case "user":
		if options.Name == "" || options.Name == "." {
			if name == "." {
				options.Name = options.Keg
			} else {
				options.Name = name
			}
		}
		_, err = t.initUserKeg(ctx, options)
	case "project":
		projectPath := strings.TrimSpace(options.Path)
		if projectPath == "" {
			base, resolveErr := t.Runtime.Getwd()
			if resolveErr != nil {
				return fmt.Errorf("unable to determine working directory: %w", resolveErr)
			}
			if !options.Cwd {
				if gitRoot := appCtx.FindGitRoot(ctx, t.Runtime, base); gitRoot != "" {
					base = gitRoot
				}
			}
			projectPath = filepath.Join(base, "docs")
		}
		projectPath = toolkit.ExpandEnv(t.Runtime, projectPath)
		projectPath, err = toolkit.ExpandPath(t.Runtime, projectPath)
		if err != nil {
			return fmt.Errorf("unable to resolve project path %q: %w", options.Path, err)
		}
		_, err = t.initProjectKeg(ctx, initLocalOptions{
			Path:    projectPath,
			Title:   options.Title,
			Creator: options.Creator,
		})
	default:
		return fmt.Errorf("invalid init destination")
	}

	return err
}

type initLocalOptions struct {
	Path string

	Creator string
	Title   string
}

// initProjectKeg creates a filesystem-backed keg repository at path.
//
// If path is empty the current working directory is used. The function uses
// the Env from ctx to resolve the working directory when available and falls
// back to os.Getwd otherwise. The destination directory is created. An initial
// keg configuration is written as YAML to "keg" inside the destination. A
// dex/ directory and a nodes.tsv file containing the zero node entry are
// created. A zero node README is written to "0/README.md".
//
// Errors are wrapped with contextual messages to aid callers.
func (t *Tap) initProjectKeg(ctx context.Context, opts initLocalOptions) (*kegurl.Target, error) {
	target := kegurl.NewFile(opts.Path)
	k, err := keg.NewKegFromTarget(ctx, target, t.Runtime)
	if err != nil {
		return nil, fmt.Errorf("unable to init keg: %w", err)
	}
	err = k.Init(ctx)
	if err != nil {
		return nil, err
	}
	err = k.UpdateConfig(ctx, func(kc *keg.Config) {
		kc.Creator = opts.Creator
		kc.Title = opts.Title
	})
	return k.Target, err
}

func (t *Tap) initUserKeg(ctx context.Context, opts InitOptions) (*kegurl.Target, error) {
	cfg := t.ConfigService.Config(true)
	repoPath := cfg.UserRepoPath()
	if repoPath == "" {
		return nil, fmt.Errorf("userRepoPath not defined in user config: %w", keg.ErrNotExist)
	}

	kegPath := filepath.Join(repoPath, opts.Name)

	target := kegurl.NewFile(kegPath)
	k, err := keg.NewKegFromTarget(ctx, target, t.Runtime)
	if err != nil {
		return nil, fmt.Errorf("unable to init keg: %w", err)
	}
	err = k.Init(ctx)
	if err != nil {
		return nil, err
	}
	err = k.UpdateConfig(ctx, func(kc *keg.Config) {
		kc.Creator = opts.Creator
		kc.Title = opts.Title
	})
	if err != nil {
		return nil, err
	}

	alias := opts.Keg
	if alias == "" {
		alias = opts.Name
	}
	if alias != "" {
		userCfg, err := t.ConfigService.UserConfig(false)
		if err != nil {
			return nil, err
		}
		if err := userCfg.AddKeg(alias, target); err != nil {
			return nil, err
		}
		if err := userCfg.Write(t.Runtime, t.PathService.UserConfig()); err != nil {
			return nil, err
		}
		t.ConfigService.ResetCache()
	}
	return k.Target, nil
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

func (t *Tap) initRegistry(opts initRegistryOptions) (*kegurl.Target, error) {
	if opts.Alias == "" {
		return nil, fmt.Errorf("alias required: %w", keg.ErrInvalid)
	}

	// Determine repo (registry) name. Prefer explicit flag, then project config.
	repoName := opts.Repo
	if repoName == "" {
		cfg := t.ConfigService.Config(true)
		if cfg != nil && cfg.DefaultRegistry() != "" {
			repoName = cfg.DefaultRegistry()
		}
	}
	if repoName == "" {
		// final fallback
		repoName = "knut"
	}

	// Determine user namespace.
	user := opts.User
	if user == "" {
		u, _ := t.Runtime.GetUser()
		if u != "" {
			user = u
		} else {
			// try to fall back to project-local default if present
			if cfg := t.ConfigService.Config(true); cfg != nil && cfg.DefaultKeg() != "" {
				// ignore: best-effort only
				user = cfg.DefaultKeg()
			}
		}
		if user == "" {
			user = "user"
		}
	}

	target := kegurl.NewApi(repoName, user, opts.Alias)

	if opts.AddUserConfig {
		userCfg, err := t.ConfigService.UserConfig(false)
		if err != nil {
			return nil, err
		}
		if err := userCfg.AddKeg(opts.Alias, target); err != nil {
			return nil, err
		}
		if err := userCfg.Write(t.Runtime, t.PathService.UserConfig()); err != nil {
			return nil, err
		}
		t.ConfigService.ResetCache()
	}

	return &target, nil
}
