package tapper

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

type ExportOptions struct {
	KegTargetOptions
	NodeIDs     []string
	WithHistory bool
	OutputPath  string
}

// Export writes a keg-archive of the selected nodes (all nodes when none are
// named) to opts.OutputPath and returns the resolved output path.
func (t *Tap) Export(ctx context.Context, opts ExportOptions) (string, error) {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleViewer)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}

	ids, err := exportNodeIDs(opts.NodeIDs)
	if err != nil {
		return "", err
	}

	source := ""
	if k.Target() != nil {
		source = k.Target().String()
	}

	rc, err := k.ExportNodes(ctx, keg.ExportNodesOptions{
		NodeIDs:     ids,
		WithHistory: opts.WithHistory,
		WithAssets:  true,
		Source:      source,
	})
	if err != nil {
		return "", err
	}
	defer rc.Close()
	archive, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}

	output, err := expandArchivePath(t.Runtime, opts.OutputPath)
	if err != nil {
		return "", err
	}
	if err := t.Runtime.Mkdir(filepath.Dir(output), 0o755, true); err != nil {
		return "", err
	}
	if err := t.Runtime.AtomicWriteFile(output, archive, 0o644); err != nil {
		return "", err
	}
	return output, nil
}

// exportNodeIDs parses explicit export node-id arguments. An empty list means
// "all nodes" and is passed through as nil.
func exportNodeIDs(raw []string) ([]keg.NodeId, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]keg.NodeId, 0, len(raw))
	for _, value := range raw {
		// Export ids are scoped to the single source keg passed in by the
		// caller; a cross-keg redirect is meaningless for a one-keg export, so
		// these stay bare.
		id, err := parseNodeID(value)
		if err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	slices.SortFunc(out, func(a, b keg.NodeId) int {
		return a.Compare(b)
	})
	return out, nil
}

func expandArchivePath(rt *toolkit.Runtime, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("output path is required: %w", keg.ErrInvalid)
	}
	path := toolkit.ExpandEnv(rt, raw)
	if expanded, err := toolkit.ExpandPath(rt, path); err == nil {
		path = expanded
	}
	return filepath.Clean(path), nil
}
