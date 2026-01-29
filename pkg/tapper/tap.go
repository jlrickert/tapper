package tapper

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

type Tap struct {
	Root string

	//Api *TapApi

	PathService   *PathService
	ConfigService *ConfigService
	KegService    *KegService
}

type TapOptions struct {
	Root       string
	ConfigPath string
}

func NewTap(ctx context.Context, opts TapOptions) (*Tap, error) {
	if opts.Root == "" {
		env := toolkit.EnvFromContext(ctx)
		wd, _ := env.Getwd()
		opts.Root = wd
		return nil, fmt.Errorf("root path required")
	}
	pathService, err := NewPathService(ctx, opts.Root)
	if err != nil {
		return nil, fmt.Errorf("unable to create path service: %w", err)
	}
	configService := &ConfigService{PathService: pathService, ConfigPath: opts.ConfigPath}
	kegService := &KegService{ConfigService: configService}
	return &Tap{
		Root:          opts.Root,
		PathService:   pathService,
		ConfigService: configService,
		KegService:    kegService,
	}, nil
}

// CatOptions configures behavior for Runner.Cat.
type CatOptions struct {
	// NodeID is the node identifier to read (e.g., "0", "42")
	NodeID string

	// Alias of the keg to read from
	Alias string

	// Meta indicates whether to display only meta data
	Meta bool
}

// Cat reads and displays a node's content with its metadata as frontmatter.
//
// The metadata (meta.yaml) is output as YAML frontmatter above the node's
// primary content (README.md).
func (t *Tap) Cat(ctx context.Context, opts CatOptions) (string, error) {
	k, err := t.KegService.Resolve(ctx, ResolveKegOptions{
		Root:    t.Root,
		Alias:   opts.Alias,
		NoCache: false,
	})
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

	if opts.Meta {
		return string(meta), nil
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
	k, err := t.KegService.Resolve(ctx, ResolveKegOptions{
		Root:    t.Root,
		Alias:   opts.Alias,
		NoCache: false,
	})
	if err != nil {
		return keg.Node{}, fmt.Errorf("unable to determine default keg: %w", err)
	}

	var body []byte
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
	k, err := t.KegService.Resolve(ctx, ResolveKegOptions{
		Root:    t.Root,
		Alias:   opts.Alias,
		NoCache: false,
	})
	if err != nil {
		return "", fmt.Errorf("unable to determine keg: %w", err)
	}

	// Rebuild indices - pass empty node as placeholder
	err = k.Index(ctx, keg.Node{})
	if err != nil {
		return "", fmt.Errorf("unable to rebuild indices: %w", err)
	}

	output := fmt.Sprintf("Indices rebuilt for %s\n", k.Target.Path())
	return output, nil
}

// InitOptions configures behavior for Runner.Init.
type InitOptions struct {
	// Type could be project, user, or registry
	Type string

	// When type is registry
	Repo     string
	User     string
	TokenEnv string

	// When type is user
	Name string

	// When type is local
	Path string

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
	switch options.Type {
	case "registry":
		_, err = t.initRegistry(ctx, initRegistryOptions{
			Alias:   options.Alias,
			User:    "",
			Repo:    "",
			Title:   options.Title,
			Creator: options.Creator,
		})
	case "user":
		_, err = t.initUserKeg(ctx, options)
	case "project":
		_, err = t.initProjectKeg(ctx, initLocalOptions{
			Path:    options.Path,
			Title:   options.Title,
			Creator: options.Creator,
		})
	default:
		return fmt.Errorf("%s is an invalid repo type", options.Type)
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
	return k.Target, err
}

func (t *Tap) initUserKeg(ctx context.Context, opts *InitOptions) (*kegurl.Target, error) {
	cfg := t.ConfigService.Config(ctx, true)
	repoPath := cfg.UserRepoPath()
	if repoPath == "" {
		return nil, fmt.Errorf("userRepoPath not defined in user config: %w", keg.ErrNotExist)
	}

	kegPath := filepath.Join(repoPath, opts.Name)

	target := kegurl.NewFile(kegPath)
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
	return k.Target, err
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

	// Determine repo (registry) name. Prefer explicit flag, then project config.
	repoName := opts.Repo
	if repoName == "" {
		cfg := t.ConfigService.Config(ctx, true)
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
			if cfg := t.ConfigService.Config(ctx, true); cfg != nil && cfg.DefaultKeg() != "" {
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

// ConfigOptions configures behavior for Tap.Config.
type ConfigOptions struct {
	// Project indicates whether to display project config
	Project bool

	// User indicates whether to display user config
	User bool
}

// Config displays the merged or project configuration.
func (t *Tap) Config(ctx context.Context, opts ConfigOptions) (string, error) {
	var cfg *Config
	if opts.Project {
		lCfg, err := t.ConfigService.ProjectConfig(ctx, false)
		if err != nil {
			return "", fmt.Errorf("unable to read project config: %w", err)
		}
		cfg = lCfg
	} else if opts.User {
		uCfg, err := t.ConfigService.UserConfig(ctx, false)
		if err != nil {
			return "", fmt.Errorf("unable to read project config: %w", err)
		}
		cfg = uCfg
	} else {
		cfg = t.ConfigService.Config(ctx, true)
	}

	data, err := cfg.ToYAML(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to serialize config: %w", err)
	}

	return string(data), nil
}

// ConfigEditOptions configures behavior for Tap.ConfigEdit.
type ConfigEditOptions struct {
	// Project indicates whether to edit local config instead of user config
	Project bool

	ConfigPath string
}

// ConfigEdit opens the configuration file in the default editor.
func (t *Tap) ConfigEdit(ctx context.Context, opts ConfigEditOptions) error {
	var configPath string
	if opts.ConfigPath != "" {
		configPath = opts.ConfigPath
	} else if opts.Project {
		configPath = t.PathService.ProjectConfig()
	} else {
		configPath = t.PathService.UserConfig()
	}

	// If config doesn't exist, create a default one
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		var cfg *Config
		if opts.Project {
			cfg = DefaultProjectConfig("", "")
		} else {
			cfg = DefaultUserConfig("public", "~/Documents/kegs")
		}
		if err := cfg.Write(ctx, configPath); err != nil {
			return fmt.Errorf("unable to create default config: %w", err)
		}
	}

	err := toolkit.Edit(ctx, configPath)
	return err
}

// InfoOptions configures behavior for Tap.Info.
type InfoOptions struct {
	// Alias of the keg to display info for
	Alias string
}

// Info displays the keg metadata (keg.yaml file contents).
func (t *Tap) Info(ctx context.Context, opts InfoOptions) (string, error) {
	k, err := t.KegService.Resolve(ctx, ResolveKegOptions{
		Root:    t.Root,
		Alias:   opts.Alias,
		NoCache: false,
	})
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}

	cfg, err := k.Config(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to read keg config: %w", err)
	}

	// Convert config to YAML format
	return cfg.String(), nil
}

// InfoEditOptions configures behavior for Tap.InfoEdit.
type InfoEditOptions struct {
	// Alias of the keg to edit info for
	Alias string
}

func (t *Tap) LookupKeg(ctx context.Context, alias string) (*keg.Keg, error) {
	k, err := t.KegService.Resolve(ctx, ResolveKegOptions{
		Root:    t.Root,
		Alias:   alias,
		NoCache: false,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}
	return k, nil
}

// InfoEdit opens the keg configuration file in the default editor.
func (t *Tap) InfoEdit(ctx context.Context, opts InfoEditOptions) error {
	k, err := t.LookupKeg(ctx, opts.Alias)
	if err != nil {
		return err
	}

	if k.Target.Scheme() != kegurl.SchemeFile {
		return fmt.Errorf("%s", "Only local kegs are supported")
	}

	// Get the keg config file path
	// The keg file is typically at the root of the keg directory
	configPath := filepath.Join(k.Target.Path(), "keg")
	if k.Target.Scheme() == kegurl.SchemeFile {
		configPath = filepath.Join(k.Target.Path(), "keg")
	}

	// Ensure config exists by reading it first
	_, err = k.Config(ctx)
	if err != nil {
		return fmt.Errorf("unable to read keg config: %w", err)
	}

	// Open the file in the editor
	err = toolkit.Edit(ctx, configPath)
	if err != nil {
		return fmt.Errorf("unable to edit keg config: %w", err)
	}

	// Try to parse the edited config to validate it
	_, err = k.Config(ctx)
	if err != nil {
		return fmt.Errorf("keg config is invalid after editing: %w", err)
	}

	return nil
}

func firstDir(path string) string {
	// Clean path first
	path = filepath.Clean(path)

	// Split by OS separator
	parts := strings.Split(path, string(filepath.Separator))

	// Skip the empty first part (from absolute paths like /foo or C:\foo)
	for i := 0; i < len(parts); i++ {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return ""
}

// ListKegs returns all available keg directories by scanning the user repository
// and merging with configured keg aliases. When cache is true, cached config
// values may be used.
func (t *Tap) ListKegs(ctx context.Context, cache bool) ([]string, error) {
	cfg := t.ConfigService.Config(ctx, cache)
	userRepo, _ := toolkit.ExpandPath(ctx, cfg.UserRepoPath())

	// Find files
	var results []string
	pattern := filepath.Join(userRepo, "*", "keg")
	if kegPaths, err := toolkit.Glob(ctx, pattern); err == nil {
		for _, kegPath := range kegPaths {
			path, err := filepath.Rel(userRepo, kegPath)
			if err == nil {
				results = append(results, path)
			}
		}
	}

	results = append(results, cfg.ListKegs()...)

	// Extract unique directories containing keg files
	kegDirs := make([]string, 0, len(results))
	seenDirs := make(map[string]bool)
	for _, result := range results {
		dir := firstDir(result)
		if !seenDirs[dir] {
			kegDirs = append(kegDirs, dir)
			seenDirs[dir] = true
		}
	}

	return kegDirs, nil
}
