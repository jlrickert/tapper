package tapper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	appCtx "github.com/jlrickert/cli-toolkit/apppaths"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

type Tap struct {
	Root string
	// Runtime carries process-level dependencies.
	Runtime *toolkit.Runtime

	//Api *TapApi

	PathService   *PathService
	ConfigService *ConfigService
	KegService    *KegService
}

type TapOptions struct {
	Root       string
	ConfigPath string
	Runtime    *toolkit.Runtime
}

func NewTap(opts TapOptions) (*Tap, error) {
	rt := opts.Runtime
	if rt == nil {
		var err error
		rt, err = toolkit.NewRuntime()
		if err != nil {
			return nil, fmt.Errorf("unable to create runtime: %w", err)
		}
	}
	if err := rt.Validate(); err != nil {
		return nil, fmt.Errorf("invalid runtime: %w", err)
	}

	if opts.Root == "" {
		wd, err := rt.Getwd()
		if err != nil {
			return nil, fmt.Errorf("unable to determine working directory: %w", err)
		}
		opts.Root = wd
	}
	pathService, err := NewPathService(rt, opts.Root)
	if err != nil {
		return nil, fmt.Errorf("unable to create path service: %w", err)
	}
	configService := &ConfigService{
		Runtime:     rt,
		PathService: pathService,
		ConfigPath:  opts.ConfigPath,
	}
	kegService := &KegService{
		Runtime:       rt,
		ConfigService: configService,
	}
	return &Tap{
		Runtime:       rt,
		Root:          opts.Root,
		PathService:   pathService,
		ConfigService: configService,
		KegService:    kegService,
	}, nil
}

// KegTargetOptions describes how a command should resolve a keg target.
type KegTargetOptions struct {
	// Keg is the configured alias.
	Keg string

	// Project resolves using project-local keg discovery.
	Project bool

	// Cwd, when combined with Project, uses cwd as the base instead of git root.
	Cwd bool

	// Path is an explicit local project path used for project keg discovery.
	Path string
}

func (t *Tap) resolveKeg(ctx context.Context, opts KegTargetOptions) (*keg.Keg, error) {
	return t.KegService.Resolve(ctx, ResolveKegOptions{
		Root:    t.Root,
		Keg:     opts.Keg,
		Project: opts.Project,
		Cwd:     opts.Cwd,
		Path:    opts.Path,
		NoCache: false,
	})
}

// CatOptions configures behavior for Runner.Cat.
type CatOptions struct {
	// NodeID is the node identifier to read (e.g., "0", "42")
	NodeID string

	KegTargetOptions

	// ContentOnly displays content only.
	ContentOnly bool

	// StatsOnly displays stats only.
	StatsOnly bool

	// MetaOnly displays metadata only.
	MetaOnly bool
}

// Cat reads and displays a node's content with its metadata as frontmatter.
//
// The metadata (meta.yaml) is output as YAML frontmatter above the node's
// primary content (README.md).
func (t *Tap) Cat(ctx context.Context, opts CatOptions) (string, error) {
	outputModes := 0
	if opts.ContentOnly {
		outputModes++
	}
	if opts.StatsOnly {
		outputModes++
	}
	if opts.MetaOnly {
		outputModes++
	}
	if outputModes > 1 {
		return "", fmt.Errorf("only one output mode may be selected: --content-only, --stats-only, --meta-only")
	}

	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
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
		if errors.Is(err, keg.ErrNotExist) {
			return "", fmt.Errorf("node %s not found", node.Path())
		}
		return "", fmt.Errorf("unable to read node metadata: %w", err)
	}

	stats, err := k.Repo.ReadStats(ctx, *node)
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			return "", fmt.Errorf("node %s not found", node.Path())
		}
		return "", fmt.Errorf("unable to read node stats: %w", err)
	}

	// Read content
	content, err := k.Repo.ReadContent(ctx, *node)
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			return "", fmt.Errorf("node %s not found", node.Path())
		}
		return "", fmt.Errorf("unable to read node content: %w", err)
	}

	if opts.ContentOnly {
		return string(content), nil
	}

	if opts.StatsOnly {
		return formatStatsOnlyYAML(ctx, stats), nil
	}

	if opts.MetaOnly {
		return string(meta), nil
	}

	mergedMeta, err := mergeMetaAndStatsYAML(ctx, meta, stats)
	if err != nil {
		return "", err
	}
	output := formatFrontmatter([]byte(mergedMeta), content)
	return output, nil
}

func formatFrontmatter(meta []byte, content []byte) string {
	metaText := strings.TrimRight(string(meta), "\n")
	return fmt.Sprintf("---\n%s\n---\n%s", metaText, string(content))
}

func mergeMetaAndStatsYAML(ctx context.Context, metaRaw []byte, stats *keg.NodeStats) (string, error) {
	meta, err := keg.ParseMeta(ctx, metaRaw)
	if err != nil {
		return "", fmt.Errorf("unable to parse node metadata: %w", err)
	}
	return strings.TrimRight(meta.ToYAMLWithStats(stats), "\n"), nil
}

func formatStatsOnlyYAML(ctx context.Context, stats *keg.NodeStats) string {
	meta := keg.NewMeta(ctx, time.Time{})
	return strings.TrimRight(meta.ToYAMLWithStats(stats), "\n")
}

// CreateOptions configures behavior for Runner.Create.
type CreateOptions struct {
	KegTargetOptions

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
func (t *Tap) Create(ctx context.Context, opts CreateOptions) (keg.NodeId, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return keg.NodeId{}, fmt.Errorf("unable to determine default keg: %w", err)
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

	node, err := k.Create(ctx, &keg.CreateOptions{
		Title: opts.Title,
		Lead:  opts.Lead,
		Tags:  opts.Tags,
		Body:  body,
		Attrs: attrs,
	})
	if err != nil {
		return keg.NodeId{}, fmt.Errorf("unable to create node: %w", err)
	}

	return node, nil
}

// IndexOptions configures behavior for Runner.Index.
type IndexOptions struct {
	KegTargetOptions

	// Rebuild rebuilds the full index
	Rebuild bool

	// NoUpdate skips updating node meta information
	NoUpdate bool
}

// Index rebuilds all indices for a keg (nodes.tsv, tags, links, backlinks).
//
// This scans all nodes and regenerates the dex indices. Useful after
// manually modifying files or to refresh stale indices.
func (t *Tap) Index(ctx context.Context, opts IndexOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to determine keg: %w", err)
	}

	// Rebuild indices - pass empty node as placeholder
	err = k.Index(ctx, keg.IndexOptions{NoUpdate: opts.NoUpdate})
	if err != nil {
		return "", fmt.Errorf("unable to rebuild indices: %w", err)
	}

	output := fmt.Sprintf("Indices rebuilt for %s\n", k.Target.Path())
	return output, nil
}

// InitOptions configures behavior for Runner.InitKeg.
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

// ConfigOptions configures behavior for Tap.Config.
type ConfigOptions struct {
	// Project indicates whether to display project config
	Project bool

	// User indicates whether to display user config
	User bool

	// Template prints out a templated. Combine with either project or user
	// flag. Defaults to using --user flag
	Template bool
}

// Config displays the merged or project configuration.
func (t *Tap) Config(opts ConfigOptions) (string, error) {
	var cfg *Config
	if opts.Template {
		if opts.Project {
			cfg := DefaultProjectConfig("", "")
			data, err := cfg.ToYAML()
			return string(data), err
		}
		cfg := DefaultUserConfig("", "")
		data, err := cfg.ToYAML()
		return string(data), err
	}
	if opts.Project {
		lCfg, err := t.ConfigService.ProjectConfig(false)
		if err != nil {
			return "", err
		}
		cfg = lCfg
	} else if opts.User {
		uCfg, err := t.ConfigService.UserConfig(false)
		if err != nil {
			return "", err
		}
		cfg = uCfg
	} else {
		cfg = t.ConfigService.Config(true)
	}

	data, err := cfg.ToYAML()
	if err != nil {
		return "", fmt.Errorf("unable to serialize config: %w", err)
	}

	return string(data), nil
}

// ConfigEditOptions configures behavior for Tap.ConfigEdit.
type ConfigEditOptions struct {
	// Project indicates whether to edit local config instead of user config
	Project bool

	User bool

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
			cfg = DefaultProjectConfig("project", "kegs")
		} else {
			cfg = DefaultUserConfig("public", "~/Documents/kegs")
		}
		if err := cfg.Write(t.Runtime, configPath); err != nil {
			return fmt.Errorf("unable to create default config: %w", err)
		}
	}

	err := toolkit.Edit(ctx, t.Runtime, configPath)
	return err
}

// InfoOptions configures behavior for Tap.Info.
type InfoOptions struct {
	KegTargetOptions
}

// Info displays the keg metadata (keg.yaml file contents).
func (t *Tap) Info(ctx context.Context, opts InfoOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
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
	KegTargetOptions
}

func (t *Tap) LookupKeg(ctx context.Context, kegAlias string) (*keg.Keg, error) {
	k, err := t.KegService.Resolve(ctx, ResolveKegOptions{
		Root:    t.Root,
		Keg:     kegAlias,
		NoCache: false,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}
	return k, nil
}

// InfoEdit opens the keg configuration file in the default editor.
func (t *Tap) InfoEdit(ctx context.Context, opts InfoEditOptions) error {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return err
	}

	if k.Target.Scheme() != kegurl.SchemeFile {
		return fmt.Errorf("%s", "Only local kegs are supported")
	}

	configPath, err := t.Runtime.ResolvePath(filepath.Join(k.Target.Path(), "keg"), true)
	if err != nil {
		return fmt.Errorf("unable to resolve keg config path: %w", err)
	}

	// Ensure config exists by reading it first
	_, err = k.Config(ctx)
	if err != nil {
		return fmt.Errorf("unable to read keg config: %w", err)
	}

	// Open the file in the editor
	err = toolkit.Edit(ctx, t.Runtime, configPath)
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

type ListOptions struct {
	KegTargetOptions

	// Format to use. %i is node id, %d
	// %i is node id
	// %d is date
	// %t is node title
	// %% for literal %
	Format string

	IdOnly bool
}

func (t *Tap) List(ctx context.Context, opts ListOptions) ([]string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}
	dex, err := k.Dex(ctx)
	if err != nil {
		return []string{}, fmt.Errorf("unable to read dex: %w", err)
	}

	format := opts.Format
	if format == "" {
		format = "%i\t%d\t%t"
	}

	entries := dex.Nodes(ctx)
	lines := make([]string, 0)
	for _, entry := range entries {
		line := format
		line = strings.Replace(line, "%i", entry.ID, -1)
		line = strings.Replace(line, "%d", entry.Updated.Format(time.RFC3339), -1)
		line = strings.Replace(line, "%t", entry.Title, -1)
		lines = append(lines, line)
	}
	return lines, nil
}

type DirOptions struct {
	KegTargetOptions
}

func (t *Tap) Dir(ctx context.Context, opts DirOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}
	return k.Target.File, nil
}

// ListKegs returns all available keg directories by scanning the user repository
// and merging with configured keg aliases. When cache is true, cached config
// values may be used.
func (t *Tap) ListKegs(cache bool) ([]string, error) {
	cfg := t.ConfigService.Config(cache)
	userRepo, _ := toolkit.ExpandPath(t.Runtime, cfg.UserRepoPath())

	// Find files
	var results []string
	pattern := filepath.Join(userRepo, "*", "keg")
	if kegPaths, err := t.Runtime.Glob(pattern); err == nil {
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
