package tapper

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

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

		id, err := parseNodeID(opts.NodeID)
		if err != nil {
			return "", err
		}

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

// ListKegs returns available keg aliases from local discovery paths and config.
// When cache is true, cached config values may be used.
func (t *Tap) ListKegs(cache bool) ([]string, error) {
	cfg, err := t.ConfigService.Config(cache)
	if err != nil {
		return nil, fmt.Errorf("failed to list kegs: %w", err)
	}
	var results []string
	if localKegs, err := t.ConfigService.DiscoveredKegAliases(cache); err == nil {
		results = append(results, localKegs...)
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
