package tapper

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
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

type ImportOptions struct {
	KegTargetOptions
	Input string
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

// Import loads a keg-archive from a file path or http(s) URL into the
// resolved keg and returns the imported node ids.
func (t *Tap) Import(ctx context.Context, opts ImportOptions) ([]keg.NodeId, error) {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}

	archiveBytes, err := readArchiveInput(ctx, t.Runtime, opts.Input)
	if err != nil {
		return nil, err
	}

	imported, err := k.ImportNodes(ctx, bytes.NewReader(archiveBytes), keg.ImportNodesOptions{})
	if err != nil {
		return nil, err
	}

	ids := make([]keg.NodeId, 0, len(imported))
	for _, node := range imported {
		ids = append(ids, node.ID)
	}
	return ids, nil
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

func readArchiveInput(ctx context.Context, rt *toolkit.Runtime, input string) ([]byte, error) {
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, input, nil)
		if err != nil {
			return nil, fmt.Errorf("unable to create archive request: %w", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("unable to download archive: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("unable to download archive: status %d", resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("unable to read archive download: %w", err)
		}
		return data, nil
	}

	path, err := expandArchivePath(rt, input)
	if err != nil {
		return nil, err
	}
	resolved, err := rt.ResolvePath(path, false)
	if err != nil {
		return nil, fmt.Errorf("unable to resolve archive path %s: %w", path, err)
	}
	if _, err := rt.Stat(resolved, false); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("archive not found: %s: %w", resolved, err)
		}
		return nil, fmt.Errorf("unable to stat archive %s: %w", resolved, err)
	}
	data, err := rt.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("unable to read archive %s: %w", resolved, err)
	}
	return data, nil
}
