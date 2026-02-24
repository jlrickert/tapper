package tapper

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"gopkg.in/yaml.v3"
)

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

// KegInfoOptions configures behavior for Tap.KegInfo.
type KegInfoOptions struct {
	KegTargetOptions
}

// KegInfo displays diagnostics for a resolved keg.
func (t *Tap) KegInfo(ctx context.Context, opts KegInfoOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}
	if _, err := k.Config(ctx); err != nil {
		return "", fmt.Errorf("unable to read keg config: %w", err)
	}

	workingDir, err := t.Runtime.Getwd()
	if err != nil {
		return "", fmt.Errorf("unable to get working directory: %w", err)
	}

	nodeIDs, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to list nodes: %w", err)
	}

	type assetDiagnostics struct {
		Supported       bool `yaml:"supported"`
		NodesWithAssets int  `yaml:"nodes_with_assets"`
		TotalAssets     int  `yaml:"total_assets"`
	}
	type diagnostics struct {
		WorkingDirectory string `yaml:"working_directory"`
		Target           string `yaml:"target,omitempty"`
		Scheme           string `yaml:"scheme,omitempty"`
		KegDirectory     string `yaml:"keg_directory,omitempty"`

		NodeCount int `yaml:"node_count"`

		Assets struct {
			Files  assetDiagnostics `yaml:"files"`
			Images assetDiagnostics `yaml:"images"`
		} `yaml:"assets"`
	}

	out := diagnostics{
		WorkingDirectory: workingDir,
		NodeCount:        len(nodeIDs),
	}

	if k.Target != nil {
		out.Target = k.Target.String()
		out.Scheme = k.Target.Scheme()
		if k.Target.Scheme() == kegurl.SchemeFile {
			path := toolkit.ExpandEnv(t.Runtime, k.Target.Path())
			if expanded, expandErr := toolkit.ExpandPath(t.Runtime, path); expandErr == nil {
				path = expanded
			}
			out.KegDirectory = filepath.Clean(path)
		} else {
			out.KegDirectory = k.Target.Path()
		}
	}

	if repoFiles, ok := k.Repo.(keg.RepositoryFiles); ok {
		out.Assets.Files.Supported = true
		for _, id := range nodeIDs {
			names, listErr := repoFiles.ListFiles(ctx, id)
			if listErr != nil {
				return "", fmt.Errorf("unable to list files for node %s: %w", id.Path(), listErr)
			}
			if len(names) > 0 {
				out.Assets.Files.NodesWithAssets++
			}
			out.Assets.Files.TotalAssets += len(names)
		}
	}

	if repoImages, ok := k.Repo.(keg.RepositoryImages); ok {
		out.Assets.Images.Supported = true
		for _, id := range nodeIDs {
			names, listErr := repoImages.ListImages(ctx, id)
			if listErr != nil {
				return "", fmt.Errorf("unable to list images for node %s: %w", id.Path(), listErr)
			}
			if len(names) > 0 {
				out.Assets.Images.NodesWithAssets++
			}
			out.Assets.Images.TotalAssets += len(names)
		}
	}

	b, err := yaml.Marshal(out)
	if err != nil {
		return "", fmt.Errorf("unable to marshal info output: %w", err)
	}
	return string(b), nil
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

	saveConfig := func(data []byte) error {
		if configPath != "" {
			resolvedPath, err := t.Runtime.ResolvePath(configPath, true)
			if err != nil {
				return fmt.Errorf("unable to resolve keg config path: %w", err)
			}
			if err := t.Runtime.AtomicWriteFile(resolvedPath, data, 0o644); err != nil {
				return fmt.Errorf("unable to save edited keg config: %w", err)
			}
			return nil
		}
		if err := k.SetConfig(ctx, data); err != nil {
			return fmt.Errorf("unable to save edited keg config: %w", err)
		}
		return nil
	}

	initialRaw := originalRaw
	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			if bytes.Equal(pipedRaw, originalRaw) {
				return nil
			}
			if _, parseErr := keg.ParseKegConfig(pipedRaw); parseErr != nil {
				return fmt.Errorf("keg config from stdin is invalid: %w", parseErr)
			}
			return saveConfig(pipedRaw)
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

	if err := editWithLiveSaves(ctx, t.Runtime, tempPath, func(editedRaw []byte) error {
		if _, err := keg.ParseKegConfig(editedRaw); err != nil {
			return fmt.Errorf("keg config is invalid after editing: %w", err)
		}
		return saveConfig(editedRaw)
	}); err != nil {
		return fmt.Errorf("unable to edit keg config: %w", err)
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
