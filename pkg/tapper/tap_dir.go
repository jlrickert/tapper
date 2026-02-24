package tapper

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
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
