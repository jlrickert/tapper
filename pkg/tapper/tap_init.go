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
	// Local (filesystem) project destination selectors. Any of these forces a
	// project-local keg under the git root / cwd / explicit path; namespace and
	// hub resolution do not apply.
	Project bool
	User    bool   // pin the reserved @local namespace (this machine's local hub)
	Cwd     bool   // use cwd as the project root base instead of git root
	Path    string // explicit filesystem path; implies a local project destination

	// Destination overrides for the namespace-centric resolution. An empty
	// Namespace defers to config (kegs[name].Namespace → default/fallback); an
	// empty Hub defers to namespaces[ns].Hub → default/fallback. A bare name
	// thus resolves to the default namespace+hub — typically a remote create —
	// while "@local/<name>" pins the filesystem hub.
	Namespace string
	Hub       string

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

// LocalDestination reports whether the options force a project-local
// filesystem keg (as opposed to the namespace-resolved user/hub destination).
func (o InitOptions) LocalDestination() bool {
	return o.Project || o.Cwd || strings.TrimSpace(o.Path) != ""
}

// InitKeg creates a keg named options.Keg and initializes it at the resolved
// destination. Destination resolution is namespace-centric:
//
//   - --project/--cwd/--path → a project-local filesystem keg under the git
//     root (or cwd / explicit path).
//   - otherwise the name resolves through namespace → hub (with --namespace and
//     --hub as overrides, and --user pinning @local): a local hub yields a
//     filesystem keg at <basePath>/@<namespace>/<name>; a remote hub creates
//     the keg on the hub (POST /api/v1/@<namespace>/kegs), failing if it
//     already exists. On success the keg is recorded in user config.
func (t *Tap) InitKeg(ctx context.Context, options InitOptions) (*keg.Target, error) {
	name := strings.TrimSpace(options.Keg)
	if err := ValidateKegAlias(name); err != nil {
		return nil, err
	}
	options.Keg = name

	// Explicit project-local destination: pure filesystem, no namespace/hub.
	if options.LocalDestination() {
		if options.User {
			return nil, fmt.Errorf("--user cannot be combined with a local destination (--project/--cwd/--path)")
		}
		if strings.TrimSpace(options.Hub) != "" {
			return nil, fmt.Errorf("--hub cannot be combined with a local destination (--project/--cwd/--path)")
		}
		return t.initProjectDestination(ctx, options)
	}

	cfg, err := t.ConfigService.Config(true)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Resolve the namespace then the hosting hub, mirroring Config.ResolveRef's
	// precedence so we can both create and record the keg. --user pins @local.
	explicitNS := strings.TrimSpace(options.Namespace) != ""
	explicitHub := strings.TrimSpace(options.Hub) != ""
	namespace := strings.TrimSpace(options.Namespace)
	if namespace == "" && options.User {
		namespace = LocalHubName
	}
	if namespace == "" {
		namespace = cfg.resolveNamespaceForName()
	}
	hubName := strings.TrimSpace(options.Hub)
	if hubName == "" {
		hubName = cfg.resolveHubForNamespace(namespace)
	}
	entry, ok := cfg.Hub(hubName)
	if !ok {
		return nil, fmt.Errorf("hub %q is not configured", hubName)
	}
	kind := strings.TrimSpace(entry.Kind)
	if kind == "" {
		kind = HubKindRemote
	}
	if namespace == "" {
		switch {
		case kind == HubKindLocal:
			namespace = LocalHubName
		case explicitHub || explicitNS:
			// The caller explicitly steered at a remote hub but gave no
			// namespace and none is configured — surface it rather than guess.
			return nil, fmt.Errorf("cannot init %q: no namespace given (try @<namespace>/%s)", name, name)
		default:
			// Unconfigured bare init: fall back to the local hub so `tap init
			// <name>` still works without setup. A configured default/fallback
			// namespace (e.g. from `tap bootstrap`) routes bare names to the
			// remote hub instead.
			namespace = LocalHubName
			hubName = cfg.localHubName()
			if entry, ok = cfg.Hub(hubName); !ok {
				return nil, fmt.Errorf("local hub %q is not configured", hubName)
			}
			kind = HubKindLocal
		}
	}

	target, err := cfg.ResolveRef(t.Runtime, KegRef{Hub: hubName, Namespace: namespace, Name: name})
	if err != nil {
		return nil, fmt.Errorf("resolve init destination: %w", err)
	}

	if kind == HubKindLocal {
		return t.initLocalKeg(ctx, options, target, namespace, name)
	}
	return t.initRemoteKeg(ctx, options, target, hubName, namespace, name)
}

// initProjectDestination creates a project-local filesystem keg under the git
// root (or cwd / explicit --path) at <root>/kegs/<name>.
func (t *Tap) initProjectDestination(ctx context.Context, options InitOptions) (*keg.Target, error) {
	projectPath := strings.TrimSpace(options.Path)
	if projectPath == "" {
		base, err := t.Runtime.Getwd()
		if err != nil {
			return nil, fmt.Errorf("unable to determine working directory: %w", err)
		}
		if !options.Cwd {
			if gitRoot := appCtx.FindGitRoot(ctx, t.Runtime, base); gitRoot != "" {
				base = gitRoot
			}
		}
		projectPath = filepath.Join(base, "kegs", options.Keg)
	}
	resolved, err := t.Runtime.ResolvePath(projectPath, false)
	if err != nil {
		return nil, fmt.Errorf("unable to resolve project path %q: %w", projectPath, err)
	}
	return t.initProjectKeg(ctx, initLocalOptions{
		Path:    resolved,
		Title:   options.Title,
		Creator: options.Creator,
	})
}

// initLocalKeg materializes a filesystem-backed keg at the resolved local
// target and records it in user config under (name → namespace).
func (t *Tap) initLocalKeg(ctx context.Context, options InitOptions, target *keg.Target, namespace, name string) (*keg.Target, error) {
	k, err := keg.NewKegFromTarget(ctx, *target, t.Runtime)
	if err != nil {
		return nil, fmt.Errorf("unable to init keg: %w", err)
	}
	if err := k.Init(ctx); err != nil {
		return nil, err
	}
	if err := keg.UpdateConfig(ctx, k, func(kc *keg.Config) {
		kc.Creator = options.Creator
		kc.Title = options.Title
	}); err != nil {
		return nil, err
	}
	// Local namespaces resolve their hub via localHubName() and a local keg name
	// resolves through the namespace-centric chain, so there is nothing to
	// record for a local keg.
	if err := t.recordInitKeg("", namespace); err != nil {
		return nil, err
	}
	return k.Target(), nil
}

// initRemoteKeg creates the keg on the hub (POST /api/v1/@<namespace>/kegs),
// surfacing a 409 as an "already exists" error, then records the keg in user
// config (name → namespace and namespace → hub).
func (t *Tap) initRemoteKeg(ctx context.Context, options InitOptions, target *keg.Target, hubName, namespace, name string) (*keg.Target, error) {
	hubURL := strings.TrimSpace(target.HubURL)
	if hubURL == "" {
		hubURL = strings.TrimSpace(target.Url)
	}
	if hubURL == "" {
		return nil, fmt.Errorf("remote init requires a hub url; none resolved for hub %q", hubName)
	}
	token := t.hubTokenForTarget(target)
	if token == "" {
		return nil, fmt.Errorf("not logged in to hub %q (run `tap auth login --hub %s`)", hubName, hubURL)
	}
	if err := CreateKeg(ctx, hubURL, token, namespace, name, options.Title, ""); err != nil {
		return nil, err
	}
	if err := t.recordInitKeg(hubName, namespace); err != nil {
		return nil, err
	}
	return target, nil
}

// recordInitKeg persists routing for a freshly-created keg so future
// references resolve it. With the kegs alias table removed, a keg name resolves
// through the namespace-centric chain, so the only thing worth recording is the
// namespace→hub mapping for a remote keg (namespaces[namespace] pins the
// hosting hub). A local keg — or one with no namespace/hub to pin — needs
// nothing recorded and this is a no-op.
func (t *Tap) recordInitKeg(hubName, namespace string) error {
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(hubName) == "" {
		return nil
	}
	userCfg, err := t.ConfigService.UserConfig(false)
	if err != nil {
		if !errors.Is(err, keg.ErrNotExist) {
			return err
		}
		userCfg = &Config{data: &configDTO{}}
	}
	if err := userCfg.SetNamespace(namespace, NamespaceRef{Hub: hubName}); err != nil {
		return err
	}
	if err := userCfg.Write(t.Runtime, t.PathService.UserConfig()); err != nil {
		return err
	}
	t.ConfigService.ResetCache()
	return nil
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
	err = keg.UpdateConfig(ctx, k, func(kc *keg.Config) {
		kc.Creator = opts.Creator
		kc.Title = opts.Title
	})
	return k.Target(), err
}

// defaultUserKegRoot returns the platform-default directory under which user
// kegs are created when the local hub has no basePath configured. Linux/macOS
// resolve to <XDG_DATA_HOME or ~/.local/share>/tapper/kegs; Windows resolves to
// %LOCALAPPDATA%\data\tapper\kegs. Resolution flows through the cli-toolkit
// runtime so sandboxed tests get the same answer as production.
func defaultUserKegRoot(rt *toolkit.Runtime) (string, error) {
	dataDir, err := toolkit.UserDataPath(rt)
	if err != nil {
		return "", fmt.Errorf("resolve user data dir: %w", err)
	}
	return filepath.Join(dataDir, "tapper", "kegs"), nil
}
