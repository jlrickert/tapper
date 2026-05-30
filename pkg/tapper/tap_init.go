package tapper

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	appCtx "github.com/jlrickert/cli-toolkit/appctx"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

type InitOptions struct {
	// Destination selection. Exactly one group may be set.
	Project bool
	User    bool
	Cwd     bool   // use cwd as the project root base instead of git root
	Path    string // explicit filesystem path; implies local destination
	Hub     string // non-empty selects hub destination; value is the hub name

	// Hub-specific options.
	UserName string // hub namespace
	TokenEnv string

	Creator string
	Title   string
	Keg     string

	// NonInteractive suppresses interactive prompts when set, forcing the
	// caller to rely on flag-driven defaults (platform user-data dir, alias
	// inferred from cwd, etc.) even when the surface is attached to a TTY.
	// The Tap method itself does not consult this field — TTY handling is a
	// CLI/MCP concern — but the option lives on InitOptions so both the CLI
	// flag and the MCP input field map to the same canonical contract.
	NonInteractive bool
}

func (o InitOptions) LocalDestination() bool {
	return o.Project || o.Cwd || strings.TrimSpace(o.Path) != ""
}

// InitKeg creates a keg with the alias specified in options.Keg.
//
// It validates destination flags and initializes one of three destinations:
//   - user: filesystem-backed keg under the first configured kegSearchPaths entry
//   - project: filesystem-backed keg under project path or explicit --path
//   - hub: API target entry written to config only
func (t *Tap) InitKeg(ctx context.Context, options InitOptions) (*keg.Target, error) {
	alias := strings.TrimSpace(options.Keg)
	if err := ValidateKegAlias(alias); err != nil {
		return nil, err
	}
	options.Keg = alias

	enabled := 0
	if options.LocalDestination() {
		enabled++
	}
	if options.User {
		enabled++
	}
	if options.Hub != "" {
		enabled++
	}
	if enabled > 1 {
		return nil, fmt.Errorf("only one destination may be selected: local (--project/--cwd/--path), --user, or --hub")
	}

	destination := "user"
	switch {
	case options.LocalDestination():
		destination = "project"
	case options.Hub != "":
		destination = "hub"
	case options.User:
		destination = "user"
	}

	var (
		target *keg.Target
		err    error
	)
	switch destination {
	case "hub":
		target, err = t.initHub(initHubOptions{
			Alias:         options.Keg,
			User:          options.UserName,
			Hub:           options.Hub,
			AddUserConfig: true,
			Title:         options.Title,
			Creator:       options.Creator,
		})
	case "user":
		target, err = t.initUserKeg(ctx, options)
	case "project":
		projectPath := strings.TrimSpace(options.Path)
		if projectPath == "" {
			base, resolveErr := t.Runtime.Getwd()
			if resolveErr != nil {
				return nil, fmt.Errorf("unable to determine working directory: %w", resolveErr)
			}
			if !options.Cwd {
				if gitRoot := appCtx.FindGitRoot(ctx, t.Runtime, base); gitRoot != "" {
					base = gitRoot
				}
			}
			projectPath = filepath.Join(base, "kegs", options.Keg)
		}
		projectPath, err = t.Runtime.ResolvePath(projectPath, false)
		if err != nil {
			return nil, fmt.Errorf("unable to resolve project path %q: %w", projectPath, err)
		}
		target, err = t.initProjectKeg(ctx, initLocalOptions{
			Path:    projectPath,
			Title:   options.Title,
			Creator: options.Creator,
		})
	default:
		return nil, fmt.Errorf("invalid init destination")
	}

	if err != nil {
		return nil, err
	}
	return target, nil
}

type initLocalOptions struct {
	Path string

	Creator string
	Title   string
}

// initProjectKeg creates a filesystem-backed keg repository at path.
//
// The destination directory is created and initialized via keg.Init, then
// creator/title metadata is applied to the generated keg config.
func (t *Tap) initProjectKeg(ctx context.Context, opts initLocalOptions) (*keg.Target, error) {
	target := keg.NewFile(opts.Path)
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

func (t *Tap) initUserKeg(ctx context.Context, opts InitOptions) (*keg.Target, error) {
	cfg, err := t.ConfigService.Config(true)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	// User kegs live under the built-in local hub's basePath, laid out as
	// <basePath>/@local/<alias> to match Config.ResolveRef's local mapping.
	repoPath := ""
	if hub, ok := cfg.Hub(LocalHubName); ok {
		repoPath = strings.TrimSpace(hub.BasePath)
	}
	if repoPath == "" {
		repoPath, err = defaultUserKegRoot(t.Runtime)
		if err != nil {
			return nil, fmt.Errorf("local hub basePath not configured and platform default unavailable: %w", err)
		}
	}
	kegPath := filepath.Join(repoPath, opts.Keg)

	target := keg.NewFile(kegPath)
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
	if alias != "" {
		userCfg, err := t.ConfigService.UserConfig(false)
		if err != nil {
			if !errors.Is(err, keg.ErrNotExist) {
				return nil, err
			}
			userCfg = &Config{data: &configDTO{}}
		}
		ref := KegRef{Hub: LocalHubName, Namespace: LocalHubName, Name: alias}
		if err := userCfg.AddKeg(alias, ref); err != nil {
			return nil, err
		}
		if err := userCfg.Write(t.Runtime, t.PathService.UserConfig()); err != nil {
			return nil, err
		}
		t.ConfigService.ResetCache()
	}
	return k.Target, nil
}

type initHubOptions struct {
	Hub   string
	User  string
	Alias string

	AddUserConfig  bool
	AddLocalConfig bool

	Creator string
	Title   string
}

// initHub creates an API target and optionally stores it in user config.
func (t *Tap) initHub(opts initHubOptions) (*keg.Target, error) {
	if err := ValidateKegAlias(opts.Alias); err != nil {
		return nil, err
	}

	// Determine hub name. Prefer explicit flag, then project config.
	hubName := opts.Hub
	if hubName == "" {
		cfg, _ := t.ConfigService.Config(true)
		if cfg != nil && cfg.DefaultHub() != "" {
			hubName = cfg.DefaultHub()
		}
	}
	if hubName == "" {
		// final fallback
		hubName = DefaultHubName
	}

	// Determine namespace owner. Defaults to the OS user, whose default
	// namespace shares their username; falls back to the configured
	// default keg, then a literal "user" placeholder.
	namespace := opts.User
	if namespace == "" {
		u, _ := t.Runtime.GetUser()
		if u != "" {
			namespace = u
		} else {
			// try to fall back to project-local default if present
			if cfg, cfgErr := t.ConfigService.Config(true); cfgErr == nil && cfg != nil && cfg.DefaultKeg() != "" {
				// ignore: best-effort only
				namespace = cfg.DefaultKeg()
			}
		}
		if namespace == "" {
			namespace = "user"
		}
	}

	target := keg.NewApi(hubName, namespace, opts.Alias)

	if opts.AddUserConfig {
		userCfg, err := t.ConfigService.UserConfig(false)
		if err != nil {
			if !errors.Is(err, keg.ErrNotExist) {
				return nil, err
			}
			userCfg = &Config{data: &configDTO{}}
		}
		ref := KegRef{Hub: hubName, Namespace: namespace, Name: opts.Alias}
		if err := userCfg.AddKeg(opts.Alias, ref); err != nil {
			return nil, err
		}
		if err := userCfg.Write(t.Runtime, t.PathService.UserConfig()); err != nil {
			return nil, err
		}
		t.ConfigService.ResetCache()
	}

	return &target, nil
}

// defaultUserKegRoot returns the platform-default directory under which user
// kegs are created when no kegSearchPaths is configured. Linux/macOS resolve
// to <XDG_DATA_HOME or ~/.local/share>/tapper/kegs; Windows resolves to
// %LOCALAPPDATA%\data\tapper\kegs. Resolution flows through the cli-toolkit
// runtime so sandboxed tests get the same answer as production.
func defaultUserKegRoot(rt *toolkit.Runtime) (string, error) {
	dataDir, err := toolkit.UserDataPath(rt)
	if err != nil {
		return "", fmt.Errorf("resolve user data dir: %w", err)
	}
	return filepath.Join(dataDir, "tapper", "kegs"), nil
}
