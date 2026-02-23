package tapper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	appCtx "github.com/jlrickert/cli-toolkit/apppaths"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"gopkg.in/yaml.v3"
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

// StatsOptions configures behavior for Tap.Stats.
type StatsOptions struct {
	// NodeID is the node identifier to inspect (e.g., "0", "42")
	NodeID string

	KegTargetOptions
}

// EditOptions configures behavior for Tap.Edit.
type EditOptions struct {
	// NodeID is the node identifier to edit (e.g., "0", "42")
	NodeID string

	KegTargetOptions

	// Stream carries stdin piping information.
	Stream *toolkit.Stream
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

	// Read content
	content, err := k.Repo.ReadContent(ctx, *node)
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			return "", fmt.Errorf("node %s not found", node.Path())
		}
		return "", fmt.Errorf("unable to read node content: %w", err)
	}

	// Read metadata
	meta, err := k.Repo.ReadMeta(ctx, *node)
	if err != nil && !errors.Is(err, keg.ErrNotExist) {
		return "", fmt.Errorf("unable to read node metadata: %w", err)
	}

	if err := k.Touch(ctx, *node); err != nil {
		return "", fmt.Errorf("unable to update node access: %w", err)
	}

	if opts.ContentOnly {
		return string(content), nil
	}

	if opts.StatsOnly {
		stats, err := k.Repo.ReadStats(ctx, *node)
		if err != nil {
			if errors.Is(err, keg.ErrNotExist) {
				stats = &keg.NodeStats{}
			} else {
				return "", fmt.Errorf("unable to read node stats: %w", err)
			}
		}
		return formatStatsOnlyYAML(ctx, stats), nil
	}

	if opts.MetaOnly {
		return string(meta), nil
	}

	output := formatFrontmatter(meta, content)
	return output, nil
}

func formatFrontmatter(meta []byte, content []byte) string {
	metaText := strings.TrimRight(string(meta), "\n")
	return fmt.Sprintf("---\n%s\n---\n%s", metaText, string(content))
}

func formatStatsOnlyYAML(ctx context.Context, stats *keg.NodeStats) string {
	meta := keg.NewMeta(ctx, time.Time{})
	return strings.TrimRight(meta.ToYAMLWithStats(stats), "\n")
}

// Edit opens a node in an editor using a temporary markdown file.
//
// The temp file format is:
//
//	---
//	<meta yaml>
//	---
//	<markdown body>
//
// If stdin is piped, it seeds the temp file content. On save, frontmatter is
// written to meta.yaml and the body is written to the node content file.
func (t *Tap) Edit(ctx context.Context, opts EditOptions) error {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}

	node, err := keg.ParseNode(opts.NodeID)
	if err != nil {
		return fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
	}
	if node == nil {
		return fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
	}

	id := keg.NodeId{ID: node.ID, Code: node.Code}
	exists, err := k.Repo.HasNode(ctx, id)
	if err != nil {
		return fmt.Errorf("unable to inspect node: %w", err)
	}
	if !exists {
		return fmt.Errorf("node %s not found", id.Path())
	}

	content, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		return fmt.Errorf("unable to read node content: %w", err)
	}
	meta, err := k.Repo.ReadMeta(ctx, id)
	if err != nil {
		if !errors.Is(err, keg.ErrNotExist) {
			return fmt.Errorf("unable to read node metadata: %w", err)
		}
		meta = nil
	}

	originalRaw := composeEditNodeFile(meta, content)
	initialRaw := originalRaw
	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			initialRaw = pipedRaw
		}
	}

	tempPath, err := newEditorTempFilePath(t.Runtime, "tap-edit-", ".md")
	if err != nil {
		return fmt.Errorf("unable to create temp file path: %w", err)
	}
	if err := t.Runtime.WriteFile(tempPath, initialRaw, 0o600); err != nil {
		return fmt.Errorf("unable to write temp edit file: %w", err)
	}
	defer func() {
		_ = t.Runtime.Remove(tempPath, false)
	}()

	if err := toolkit.Edit(ctx, t.Runtime, tempPath); err != nil {
		return fmt.Errorf("unable to edit node: %w", err)
	}

	editedRaw, err := t.Runtime.ReadFile(tempPath)
	if err != nil {
		return fmt.Errorf("unable to read edited node file: %w", err)
	}
	if bytes.Equal(editedRaw, originalRaw) {
		return nil
	}

	hasFrontmatter, frontmatterRaw, bodyRaw, err := splitEditNodeFile(editedRaw)
	if err != nil {
		return err
	}

	if hasFrontmatter {
		metaNode, parseErr := keg.ParseMeta(ctx, frontmatterRaw)
		if parseErr != nil {
			return fmt.Errorf("invalid frontmatter metadata: %w", parseErr)
		}
		if err := k.SetMeta(ctx, id, metaNode); err != nil {
			return fmt.Errorf("unable to save node metadata: %w", err)
		}
	}

	if err := k.SetContent(ctx, id, bodyRaw); err != nil {
		return fmt.Errorf("unable to save node content: %w", err)
	}

	return nil
}

func composeEditNodeFile(meta []byte, content []byte) []byte {
	metaText := strings.TrimRight(string(meta), "\n")
	return []byte(fmt.Sprintf("---\n%s\n---\n%s", metaText, string(content)))
}

func splitEditNodeFile(raw []byte) (bool, []byte, []byte, error) {
	if len(raw) == 0 {
		return false, nil, raw, nil
	}

	trimmed := raw
	if bytes.HasPrefix(trimmed, []byte("\xef\xbb\xbf")) {
		trimmed = trimmed[3:]
	}

	var rest []byte
	switch {
	case bytes.HasPrefix(trimmed, []byte("---\n")):
		rest = trimmed[len([]byte("---\n")):]
	case bytes.HasPrefix(trimmed, []byte("---\r\n")):
		rest = trimmed[len([]byte("---\r\n")):]
	default:
		return false, nil, raw, nil
	}

	choices := [][]byte{
		[]byte("\n---\r\n"),
		[]byte("\n---\n"),
		[]byte("\r\n---\n"),
		[]byte("\n---"),
	}
	endIdx := -1
	endLen := 0
	for _, marker := range choices {
		if idx := bytes.Index(rest, marker); idx >= 0 {
			endIdx = idx
			endLen = len(marker)
			break
		}
	}
	if endIdx < 0 {
		return false, nil, nil, fmt.Errorf("invalid frontmatter: missing closing delimiter")
	}

	frontmatter := bytes.TrimSpace(rest[:endIdx])
	if len(frontmatter) > 0 {
		var check map[string]any
		if err := yaml.Unmarshal(frontmatter, &check); err != nil {
			return false, nil, nil, fmt.Errorf("invalid frontmatter yaml: %w", err)
		}
	}

	body := bytes.TrimLeft(rest[endIdx+endLen:], "\r\n")
	return true, frontmatter, body, nil
}

// Stats reads and displays programmatic node stats.
func (t *Tap) Stats(ctx context.Context, opts StatsOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}

	node, err := keg.ParseNode(opts.NodeID)
	if err != nil {
		return "", fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
	}
	if node == nil {
		return "", fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
	}

	exists, err := k.Repo.HasNode(ctx, *node)
	if err != nil {
		return "", fmt.Errorf("unable to inspect node: %w", err)
	}
	if !exists {
		return "", fmt.Errorf("node %s not found", node.Path())
	}

	stats, err := k.Repo.ReadStats(ctx, *node)
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			stats = &keg.NodeStats{}
		} else {
			return "", fmt.Errorf("unable to read node stats: %w", err)
		}
	}

	return formatStatsOnlyYAML(ctx, stats), nil
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

type MoveOptions struct {
	KegTargetOptions

	SourceID string
	DestID   string
}

func (t *Tap) Move(ctx context.Context, opts MoveOptions) error {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}

	src, err := keg.ParseNode(opts.SourceID)
	if err != nil {
		return fmt.Errorf("invalid source node ID %q: %w", opts.SourceID, err)
	}
	if src == nil {
		return fmt.Errorf("invalid source node ID %q: %w", opts.SourceID, keg.ErrInvalid)
	}

	dst, err := keg.ParseNode(opts.DestID)
	if err != nil {
		return fmt.Errorf("invalid destination node ID %q: %w", opts.DestID, err)
	}
	if dst == nil {
		return fmt.Errorf("invalid destination node ID %q: %w", opts.DestID, keg.ErrInvalid)
	}

	srcID := keg.NodeId{ID: src.ID, Code: src.Code}
	dstID := keg.NodeId{ID: dst.ID, Code: dst.Code}
	if err := k.Move(ctx, srcID, dstID); err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			return fmt.Errorf("node %s not found", srcID.Path())
		}
		if errors.Is(err, keg.ErrDestinationExists) {
			return fmt.Errorf("destination node %s already exists", dstID.Path())
		}
		return fmt.Errorf("unable to move node: %w", err)
	}

	return nil
}

type RemoveOptions struct {
	KegTargetOptions

	NodeID string
}

func (t *Tap) Remove(ctx context.Context, opts RemoveOptions) error {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}

	node, err := keg.ParseNode(opts.NodeID)
	if err != nil {
		return fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
	}
	if node == nil {
		return fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
	}

	id := keg.NodeId{ID: node.ID, Code: node.Code}
	if err := k.Remove(ctx, id); err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			return fmt.Errorf("node %s not found", id.Path())
		}
		return fmt.Errorf("unable to remove node: %w", err)
	}

	return nil
}

// IndexOptions configures behavior for Runner.Index.
type IndexOptions struct {
	KegTargetOptions

	// Rebuild rebuilds the full index
	Rebuild bool

	// NoUpdate skips updating node meta information
	NoUpdate bool
}

// Index updates indices for a keg (nodes.tsv, tags, links, backlinks).
// Default behavior is incremental. Set opts.Rebuild to force a full rebuild.
func (t *Tap) Index(ctx context.Context, opts IndexOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to determine keg: %w", err)
	}

	err = k.Index(ctx, keg.IndexOptions{
		Rebuild:  opts.Rebuild,
		NoUpdate: opts.NoUpdate,
	})
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

	// For file-backed kegs, return the raw config contents so unknown sections
	// (for example custom fields, entities, zekia blocks) are preserved.
	if k.Target != nil && k.Target.Scheme() == kegurl.SchemeFile {
		raw, rawErr := readRawKegConfig(t.Runtime, k.Target.Path())
		if rawErr == nil {
			return string(raw), nil
		}
		if !os.IsNotExist(rawErr) {
			return "", fmt.Errorf("unable to read raw keg config: %w", rawErr)
		}
	}

	cfg, err := k.Config(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to read keg config: %w", err)
	}

	// Convert config to YAML format
	return cfg.String(), nil
}

func readRawKegConfig(rt *toolkit.Runtime, root string) ([]byte, error) {
	_, raw, err := readRawKegConfigWithPath(rt, root)
	return raw, err
}

func readRawKegConfigWithPath(rt *toolkit.Runtime, root string) (string, []byte, error) {
	base := toolkit.ExpandEnv(rt, root)
	if expanded, err := toolkit.ExpandPath(rt, base); err == nil {
		base = expanded
	}

	var firstErr error
	for _, name := range []string{"keg", "keg.yaml", "keg.yml"} {
		path := filepath.Join(base, name)
		if resolved, err := rt.ResolvePath(path, true); err == nil {
			path = resolved
		}

		data, err := rt.ReadFile(path)
		if err == nil {
			return path, data, nil
		}
		if os.IsNotExist(err) {
			continue
		}
		if firstErr == nil {
			firstErr = err
		}
	}

	if firstErr != nil {
		return "", nil, firstErr
	}
	return "", nil, os.ErrNotExist
}

func newEditorTempFilePath(rt *toolkit.Runtime, prefix string, suffix string) (string, error) {
	base := ""
	if strings.TrimSpace(rt.GetJail()) != "" {
		if home, err := rt.GetHome(); err == nil && strings.TrimSpace(home) != "" {
			base = filepath.Join(home, ".cache", "tapper", "tmp")
		} else {
			base = "/tmp"
		}
	} else {
		base = strings.TrimSpace(rt.GetTempDir())
		if base == "" {
			base = os.TempDir()
		}
	}

	expanded := toolkit.ExpandEnv(rt, base)
	if p, err := toolkit.ExpandPath(rt, expanded); err == nil {
		expanded = p
	}

	if err := rt.Mkdir(expanded, 0o755, true); err != nil {
		return "", err
	}

	for i := 0; i < 64; i++ {
		path := filepath.Join(expanded,
			fmt.Sprintf("%s%d-%02d%s", prefix, time.Now().UnixNano(), i, suffix))
		if _, err := rt.Stat(path, false); err == nil {
			continue
		} else if os.IsNotExist(err) {
			return path, nil
		} else {
			return "", err
		}
	}
	return "", fmt.Errorf("unable to allocate temp file path")
}

// InfoEditOptions configures behavior for Tap.InfoEdit.
type InfoEditOptions struct {
	KegTargetOptions
	Stream *toolkit.Stream
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

	var (
		configPath  string
		originalRaw []byte
	)
	if k.Target != nil && k.Target.Scheme() == kegurl.SchemeFile {
		path, raw, readErr := readRawKegConfigWithPath(t.Runtime, k.Target.Path())
		if readErr != nil {
			return fmt.Errorf("unable to read keg config: %w", readErr)
		}
		configPath = path
		originalRaw = raw
	} else {
		cfg, cfgErr := k.Config(ctx)
		if cfgErr != nil {
			return fmt.Errorf("unable to read keg config: %w", cfgErr)
		}
		originalRaw = []byte(cfg.String())
	}

	initialRaw := originalRaw
	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			initialRaw = pipedRaw
		}
	}

	tempPath, err := newEditorTempFilePath(t.Runtime, "tap-info-", ".yaml")
	if err != nil {
		return fmt.Errorf("unable to create temp file path: %w", err)
	}
	if err := t.Runtime.WriteFile(tempPath, initialRaw, 0o600); err != nil {
		return fmt.Errorf("unable to write temp config file: %w", err)
	}
	defer func() {
		_ = t.Runtime.Remove(tempPath, false)
	}()

	if err := toolkit.Edit(ctx, t.Runtime, tempPath); err != nil {
		return fmt.Errorf("unable to edit keg config: %w", err)
	}

	editedRaw, err := t.Runtime.ReadFile(tempPath)
	if err != nil {
		return fmt.Errorf("unable to read edited temp config file: %w", err)
	}
	if bytes.Equal(editedRaw, originalRaw) {
		return nil
	}

	if _, err := keg.ParseKegConfig(editedRaw); err != nil {
		return fmt.Errorf("keg config is invalid after editing: %w", err)
	}

	if configPath != "" {
		resolvedPath, err := t.Runtime.ResolvePath(configPath, true)
		if err != nil {
			return fmt.Errorf("unable to resolve keg config path: %w", err)
		}
		if err := t.Runtime.AtomicWriteFile(resolvedPath, editedRaw, 0o644); err != nil {
			return fmt.Errorf("unable to save edited keg config: %w", err)
		}
		return nil
	}

	if err := k.SetConfig(ctx, editedRaw); err != nil {
		return fmt.Errorf("unable to save edited keg config: %w", err)
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

	Reverse bool
}

type BacklinksOptions struct {
	KegTargetOptions

	// NodeID is the target node to inspect incoming links for.
	NodeID string

	// Format to use. %i is node id
	// %d is date
	// %t is node title
	// %% for literal %
	Format string

	IdOnly bool

	Reverse bool
}

type GrepOptions struct {
	KegTargetOptions

	// Query is the regex pattern used to search nodes.
	Query string

	// Format to use. %i is node id
	// %d is date
	// %t is node title
	// %% for literal %
	Format string

	IdOnly bool

	Reverse bool

	// IgnoreCase enables case-insensitive regex matching.
	IgnoreCase bool
}

type TagsOptions struct {
	KegTargetOptions

	// Tag filters nodes by tag. When empty, all tags are listed.
	Tag string

	// Format to use. %i is node id
	// %d is date
	// %t is node title
	// %% for literal %
	Format string

	IdOnly bool

	Reverse bool
}

type grepMatch struct {
	entry keg.NodeIndexEntry
	lines []string
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

	entries := dex.Nodes(ctx)
	return renderNodeEntries(entries, opts.Format, opts.IdOnly, opts.Reverse), nil
}

func (t *Tap) Backlinks(ctx context.Context, opts BacklinksOptions) ([]string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}
	dex, err := k.Dex(ctx)
	if err != nil {
		return []string{}, fmt.Errorf("unable to read dex: %w", err)
	}

	node, err := keg.ParseNode(opts.NodeID)
	if err != nil {
		return []string{}, fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
	}
	if node == nil {
		return []string{}, fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
	}
	id := keg.NodeId{ID: node.ID, Code: node.Code}

	exists, err := k.Repo.HasNode(ctx, id)
	if err != nil {
		return []string{}, fmt.Errorf("unable to inspect node: %w", err)
	}
	if !exists {
		return []string{}, fmt.Errorf("node %s not found", id.Path())
	}

	backlinks, ok := dex.Backlinks(ctx, id)
	if !ok || len(backlinks) == 0 {
		return []string{}, nil
	}

	entries := make([]keg.NodeIndexEntry, 0, len(backlinks))
	for _, source := range backlinks {
		ref := dex.GetRef(ctx, source)
		if ref != nil {
			entries = append(entries, *ref)
			continue
		}
		entries = append(entries, keg.NodeIndexEntry{ID: source.Path()})
	}
	sortNodeIndexEntries(entries)
	return renderNodeEntries(entries, opts.Format, opts.IdOnly, opts.Reverse), nil
}

func (t *Tap) Grep(ctx context.Context, opts GrepOptions) ([]string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}
	dex, err := k.Dex(ctx)
	if err != nil {
		return []string{}, fmt.Errorf("unable to read dex: %w", err)
	}

	pattern := opts.Query
	if opts.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return []string{}, fmt.Errorf("invalid query regex %q: %w", opts.Query, err)
	}

	entries := dex.Nodes(ctx)
	matches := make([]grepMatch, 0)
	for _, entry := range entries {
		id, parseErr := keg.ParseNode(entry.ID)
		if parseErr != nil || id == nil {
			continue
		}

		contentRaw, contentErr := k.Repo.ReadContent(ctx, *id)
		if contentErr != nil {
			if errors.Is(contentErr, keg.ErrNotExist) {
				continue
			}
			return []string{}, fmt.Errorf("unable to read node content: %w", contentErr)
		}
		lineMatches := grepContentLineMatches(re, contentRaw)
		if len(lineMatches) > 0 {
			matches = append(matches, grepMatch{
				entry: entry,
				lines: lineMatches,
			})
		}
	}

	matchedEntries := make([]keg.NodeIndexEntry, 0, len(matches))
	for _, match := range matches {
		matchedEntries = append(matchedEntries, match.entry)
	}
	if opts.IdOnly || opts.Format != "" {
		return renderNodeEntries(matchedEntries, opts.Format, opts.IdOnly, opts.Reverse), nil
	}
	return renderGrepMatches(matches, opts.Reverse), nil
}

func (t *Tap) Tags(ctx context.Context, opts TagsOptions) ([]string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return []string{}, fmt.Errorf("unable to open keg: %w", err)
	}
	dex, err := k.Dex(ctx)
	if err != nil {
		return []string{}, fmt.Errorf("unable to read dex: %w", err)
	}

	tag := strings.TrimSpace(opts.Tag)
	if tag == "" {
		tags := dex.TagList(ctx)
		sortStringsAsc(tags)
		if opts.Reverse {
			reverseStrings(tags)
		}
		return tags, nil
	}

	expr, err := parseTagExpression(tag)
	if err != nil {
		return []string{}, fmt.Errorf("invalid tag expression: %w", err)
	}

	indexEntries := dex.Nodes(ctx)
	universe := make(map[string]struct{}, len(indexEntries))
	entryByID := make(map[string]keg.NodeIndexEntry, len(indexEntries))
	for _, entry := range indexEntries {
		entryByID[entry.ID] = entry
		universe[entry.ID] = struct{}{}
		node, parseErr := keg.ParseNode(entry.ID)
		if parseErr == nil && node != nil {
			path := node.Path()
			entryByID[path] = entry
			universe[path] = struct{}{}
		}
	}

	matchedIDs := evaluateTagExpression(expr, universe, func(tagName string) map[string]struct{} {
		nodes, ok := dex.TagNodes(ctx, tagName)
		if !ok || len(nodes) == 0 {
			return map[string]struct{}{}
		}
		return setFromNodeIDs(nodes)
	})
	if len(matchedIDs) == 0 {
		return []string{}, nil
	}

	entries := make([]keg.NodeIndexEntry, 0, len(matchedIDs))
	for nodeID := range matchedIDs {
		if entry, ok := entryByID[nodeID]; ok {
			entries = append(entries, entry)
			continue
		}
		node, parseErr := keg.ParseNode(nodeID)
		if parseErr == nil && node != nil {
			ref := dex.GetRef(ctx, *node)
			if ref != nil {
				entries = append(entries, *ref)
				continue
			}
		}
		entries = append(entries, keg.NodeIndexEntry{ID: nodeID})
	}
	sortNodeIndexEntries(entries)
	return renderNodeEntries(entries, opts.Format, opts.IdOnly, opts.Reverse), nil
}

func grepContentLineMatches(re *regexp.Regexp, raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}

	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	parts := strings.Split(content, "\n")
	lines := make([]string, 0)
	for i, part := range parts {
		line := strings.TrimRight(part, "\r")
		if re.MatchString(line) {
			lines = append(lines, fmt.Sprintf("%d:%s", i+1, line))
		}
	}
	return lines
}

func renderGrepMatches(matches []grepMatch, reverse bool) []string {
	lines := make([]string, 0)

	start := 0
	end := len(matches)
	step := 1
	if reverse {
		start = len(matches) - 1
		end = -1
		step = -1
	}

	first := true
	for i := start; i != end; i += step {
		match := matches[i]
		if !first {
			lines = append(lines, "")
		}
		first = false

		header := strings.TrimSpace(match.entry.Title)
		if header == "" {
			lines = append(lines, match.entry.ID)
		} else {
			lines = append(lines, fmt.Sprintf("%s %s", match.entry.ID, header))
		}
		lines = append(lines, match.lines...)
	}

	return lines
}

func renderNodeEntries(entries []keg.NodeIndexEntry, format string, idOnly bool, reverse bool) []string {
	lines := make([]string, 0)

	start := 0
	end := len(entries)
	step := 1
	if reverse {
		start = len(entries) - 1
		end = -1
		step = -1
	}

	for i := start; i != end; i += step {
		entry := entries[i]
		if idOnly {
			lines = append(lines, entry.ID)
			continue
		}

		lineFormat := format
		if lineFormat == "" {
			lineFormat = "%i\t%d\t%t"
		}

		line := lineFormat
		line = strings.Replace(line, "%i", entry.ID, -1)
		line = strings.Replace(line, "%d", entry.Updated.Format(time.RFC3339), -1)
		line = strings.Replace(line, "%t", entry.Title, -1)
		lines = append(lines, line)
	}
	return lines
}

func sortNodeIndexEntries(entries []keg.NodeIndexEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0; j-- {
			if compareNodeEntryID(entries[j-1].ID, entries[j].ID) <= 0 {
				break
			}
			entries[j-1], entries[j] = entries[j], entries[j-1]
		}
	}
}

func sortStringsAsc(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0; j-- {
			if values[j-1] <= values[j] {
				break
			}
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}

func reverseStrings(values []string) {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
}

func compareNodeEntryID(a, b string) int {
	na, ea := keg.ParseNode(a)
	nb, eb := keg.ParseNode(b)
	if ea == nil && eb == nil && na != nil && nb != nil {
		return na.Compare(*nb)
	}
	if ea == nil && na != nil {
		return -1
	}
	if eb == nil && nb != nil {
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

type DirOptions struct {
	KegTargetOptions

	NodeID string
}

func (t *Tap) Dir(ctx context.Context, opts DirOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}
	if k.Target == nil {
		return "", fmt.Errorf("keg target is not configured")
	}

	if k.Target.Scheme() == kegurl.SchemeFile {
		path := toolkit.ExpandEnv(t.Runtime, k.Target.File)
		expanded, err := toolkit.ExpandPath(t.Runtime, path)
		if err != nil {
			return "", fmt.Errorf("unable to resolve keg directory: %w", err)
		}
		kegDir := filepath.Clean(expanded)

		if strings.TrimSpace(opts.NodeID) == "" {
			return kegDir, nil
		}

		node, err := keg.ParseNode(opts.NodeID)
		if err != nil {
			return "", fmt.Errorf("invalid node ID %q: %w", opts.NodeID, err)
		}
		if node == nil {
			return "", fmt.Errorf("invalid node ID %q: %w", opts.NodeID, keg.ErrInvalid)
		}
		id := keg.NodeId{ID: node.ID, Code: node.Code}

		exists, err := k.Repo.HasNode(ctx, id)
		if err != nil {
			return "", fmt.Errorf("unable to check node existence: %w", err)
		}
		if !exists {
			return "", fmt.Errorf("node %s not found", id.Path())
		}

		return filepath.Join(kegDir, id.Path()), nil
	}

	if strings.TrimSpace(opts.NodeID) != "" {
		return "", fmt.Errorf("node directory is only available for local file-backed kegs")
	}

	return k.Target.Path(), nil
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
