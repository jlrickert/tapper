package tapper

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

type Tap struct {
	Root string

	tCtx *TapContext
}

func NewTap(ctx context.Context) (*Tap, error) {
	env := toolkit.EnvFromContext(ctx)
	wd, err := env.Getwd()
	if err != nil {
		return nil, err
	}
	return &Tap{Root: wd}, nil
}

func (t *Tap) Context(ctx context.Context) (*TapContext, error) {
	if t.tCtx != nil {
		return t.tCtx, nil
	}
	tCtx, err := newTapContext(ctx, t.Root)
	if err != nil {
		return nil, fmt.Errorf("unable to create project: %w", err)
	}
	t.tCtx = tCtx
	return t.tCtx, nil
}

// CatOptions configures behavior for Runner.Cat.
type CatOptions struct {
	// NodeID is the node identifier to read (e.g., "0", "42")
	NodeID string

	// Alias of the keg to read from
	Alias string
}

// Cat reads and displays a node's content with its metadata as frontmatter.
//
// The metadata (meta.yaml) is output as YAML frontmatter above the node's
// primary content (README.md).
func (t *Tap) Cat(ctx context.Context, opts CatOptions) (string, error) {
	proj, err := t.Context(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to read node: %w", err)
	}

	target, err := proj.ResolveKeg(ctx, &ResolveKegOpts{Alias: opts.Alias})
	if err != nil {
		return "", fmt.Errorf("unable to determine keg: %w", err)
	}

	if target == nil {
		return "", fmt.Errorf("no keg configured: %w", keg.ErrInvalid)
	}

	k, err := keg.NewKegFromTarget(ctx, *target)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}

	// Parse the node ID
	node, err := keg.ParseNode(opts.NodeID)
	if err != nil {
		return "", fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
	}
	if node == nil {
		return "", fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
	}

	// Read metadata
	meta, err := k.Repo.ReadMeta(ctx, *node)
	if err != nil {
		return "", fmt.Errorf("unable to read node metadata: %w", err)
	}

	// Read content
	content, err := k.Repo.ReadContent(ctx, *node)
	if err != nil {
		return "", fmt.Errorf("unable to read node content: %w", err)
	}

	// Format as frontmatter + content
	output := fmt.Sprintf("---\n%s---\n%s", string(meta), string(content))
	return output, nil
}

// CreateOptions configures behavior for Runner.Create.
type CreateOptions struct {
	// alias of the keg to create the node on
	Alias string

	Title  string
	Lead   string
	Tags   []string
	Attrs  map[string]string
	Stream *toolkit.Stream
}

// Create creates a new node in the project's default keg.
//
// It resolves the TapProject (via r.getProject), determines the project's
// default keg target, constructs a Keg service for that target, and delegates
// to keg.Keg.Create to allocate and persist the new node.
//
// Errors are wrapped with contextual messages to aid callers.
func (t *Tap) Create(ctx context.Context, opts CreateOptions) (keg.Node, error) {
	tCtx, err := t.Context(ctx)
	if err != nil {
		return keg.Node{}, fmt.Errorf("unable to create node: %w", err)
	}

	target, err := tCtx.ResolveKeg(ctx, &ResolveKegOpts{Alias: opts.Alias})
	if err != nil {
		return keg.Node{}, fmt.Errorf("unable to determine default keg: %w", err)
	}

	if target == nil {
		return keg.Node{}, fmt.Errorf("no default keg configured: %w", keg.ErrInvalid)
	}

	k, err := keg.NewKegFromTarget(ctx, *target)
	if err != nil {
		return keg.Node{}, fmt.Errorf("unable to open keg: %w", err)
	}

	body := []byte{}
	attrs := make(map[string]any, len(opts.Attrs))
	if opts.Stream != nil && opts.Stream.IsPiped {
		b, _ := io.ReadAll(opts.Stream.In)
		body = b
	} else {
		// Convert map[string]string to map[string]any
		for k, v := range opts.Attrs {
			attrs[k] = v
		}
	}

	node, err := k.Create(ctx, &keg.KegCreateOptions{
		Title: opts.Title,
		Lead:  opts.Lead,
		Tags:  opts.Tags,
		Body:  body,
		Attrs: attrs,
	})
	if err != nil {
		return keg.Node{}, fmt.Errorf("unable to create node: %w", err)
	}

	return node, nil
}

// IndexOptions configures behavior for Runner.Index.
type IndexOptions struct {
	// Alias of the keg to index
	Alias string
}

// Index rebuilds all indices for a keg (nodes.tsv, tags, links, backlinks).
//
// This scans all nodes and regenerates the dex indices. Useful after
// manually modifying files or to refresh stale indices.
func (t *Tap) Index(ctx context.Context, opts IndexOptions) (string, error) {
	proj, err := t.Context(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to index: %w", err)
	}

	target, err := proj.ResolveKeg(ctx, &ResolveKegOpts{Alias: opts.Alias})
	if err != nil {
		return "", fmt.Errorf("unable to determine keg: %w", err)
	}

	if target == nil {
		return "", fmt.Errorf("no keg configured: %w", keg.ErrInvalid)
	}

	k, err := keg.NewKegFromTarget(ctx, *target)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}

	// Rebuild indices - pass empty node as placeholder
	err = k.Index(ctx, keg.Node{})
	if err != nil {
		return "", fmt.Errorf("unable to rebuild indices: %w", err)
	}

	output := fmt.Sprintf("Indices rebuilt for %s\n", target.Path())
	return output, nil
}

// InitOptions configures behavior for Runner.Init.
type InitOptions struct {
	// Type could be local, user, or registry
	Type string

	// When type is registry
	Repo     string
	User     string
	TokenEnv string

	// When type is user
	Name string

	// When type is local
	Path string

	// AddConfig adds config to user config
	FlagAddToConfig bool
	FlagNoAddConfig bool

	// AddLocalConfig adds the alias to the local project
	AddLocalConfig bool

	Creator string
	Title   string
	Alias   string
}

// Init creates a keg entry for the given name.
//
// If name is empty an ErrInvalid-wrapped error is returned. Init obtains the
// project via getProject and then performs the actions required to create a
// keg. The current implementation defers to project.DefaultKeg for further
// resolution and returns any error encountered when obtaining the project.
func (t *Tap) Init(ctx context.Context, name string, options *InitOptions) error {
	var err error
	var target *kegurl.Target
	switch options.Type {
	case "registry":
		target, err = t.initRegistry(ctx, initRegistryOptions{
			Alias:          options.Alias,
			User:           "",
			Repo:           "",
			AddUserConfig:  options.FlagAddToConfig,
			AddLocalConfig: options.AddLocalConfig,
			Title:          options.Title,
			Creator:        options.Creator,
		})
	case "user":
		k, e := t.initUserKeg(ctx, options)
		err = e
		target = k.Target
	case "local":
		k, e := t.initLocalKeg(ctx, initLocalOptions{
			Path:           options.Path,
			AddUserConfig:  options.FlagAddToConfig,
			AddLocalConfig: options.AddLocalConfig,
			Title:          options.Title,
			Creator:        options.Creator,
		})
		err = e
		target = k.Target
	default:
		return fmt.Errorf("%s is an invalid repo type", options.Type)
	}

	if err != nil {
		return err
	}

	tCtx, err := t.Context(ctx)
	cfg := tCtx.Config(ctx)
	if cfg == nil || cfg.UserRepoPath() == "" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("unable to add alias %s update user config: %w", options.Alias, err)
	}

	if options.FlagNoAddConfig {
		return nil
	}

	if !options.FlagAddToConfig && options.Type == "local" {
		return nil
	}

	return tCtx.UserConfigUpdate(ctx, func(cfg *Config) {
		alias := options.Alias
		if options.Alias == "." {
			alias = t.Root
		}
		if options.Alias == "~" {
			a, err := toolkit.ResolvePath(ctx, "~", false)
			if err != nil {
				alias = a
			}
		}
		cfg.AddKeg(alias, *target)
	}, true)
}

type initLocalOptions struct {
	Path           string
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
func (t *Tap) initLocalKeg(ctx context.Context, opts initLocalOptions) (*keg.Keg, error) {
	target := kegurl.NewFile(opts.Path)
	k, err := keg.NewKegFromTarget(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("unable to init keg: %w", err)
	}
	err = k.Init(ctx)
	if err != nil {
		return nil, err
	}
	err = k.UpdateConfig(ctx, func(kc *keg.KegConfig) {
		kc.Creator = opts.Creator
		kc.Title = opts.Title
	})
	return k, err
}

func (t *Tap) initUserKeg(ctx context.Context, opts *InitOptions) (*keg.Keg, error) {
	tapCtx, err := t.Context(ctx)
	if err != nil {
		return nil, err
	}
	cfg := tapCtx.Config(ctx)
	repoPath := cfg.UserRepoPath()
	if repoPath == "" {
		return nil, fmt.Errorf("userRepoPath not defined in user config: %w", keg.ErrNotExist)
	}

	kegPath := filepath.Join(repoPath, opts.Name)

	target := kegurl.NewFile(kegPath)
	k, err := keg.NewKegFromTarget(ctx, target)
	err = k.Init(ctx)
	if err != nil {
		return nil, err
	}
	err = k.UpdateConfig(ctx, func(kc *keg.KegConfig) {
		kc.Creator = opts.Creator
		kc.Title = opts.Title
	})
	return k, err
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

func (t *Tap) initRegistry(ctx context.Context, opts initRegistryOptions) (*kegurl.Target, error) {
	if opts.Alias == "" {
		return nil, fmt.Errorf("alias required: %w", keg.ErrInvalid)
	}

	proj, err := t.Context(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to init registry keg: %w", err)
	}

	// Determine repo (registry) name. Prefer explicit flag, then project config.
	repoName := opts.Repo
	if repoName == "" {
		cfg := proj.Config(ctx)
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
		env := toolkit.EnvFromContext(ctx)
		u, _ := env.GetUser()
		if u != "" {
			user = u
		} else {
			// try to fall back to project-local default if present
			if cfg := proj.Config(ctx); cfg != nil && cfg.DefaultKeg() != "" {
				// ignore: best-effort only
				user = cfg.DefaultKeg()
			}
		}
		if user == "" {
			user = "user"
		}
	}

	target := kegurl.NewApi(repoName, user, opts.Alias)

	return &target, nil
}
