package tapper

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

type Tap struct {
	Root string
	// Runtime carries process-level dependencies.
	Runtime *toolkit.Runtime

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
