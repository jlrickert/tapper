package keg

import (
	"context"
	"errors"
	"fmt"
)

// ReadNode assembles the node's full state: content (required), raw meta and
// stats (optional, zero-valued when absent), and asset name lists (nil when
// the backend lacks the capability).
func (k *LocalKeg) ReadNode(ctx context.Context, id NodeId) (*NodeView, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to read node: %w", err)
	}

	content, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		return nil, err
	}

	meta, err := k.Repo.ReadMeta(ctx, id)
	if err != nil && !errors.Is(err, ErrNotExist) {
		return nil, err
	}

	stats, err := k.Repo.ReadStats(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrNotExist) {
			return nil, err
		}
		stats = &NodeStats{}
	}
	if stats == nil {
		stats = &NodeStats{}
	}

	view := &NodeView{
		ID:      id,
		Content: content,
		Meta:    meta,
		Stats:   stats,
	}
	if files, ok := k.Repo.(RepositoryFiles); ok {
		names, err := files.ListFiles(ctx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return nil, err
		}
		if names == nil {
			names = []string{}
		}
		view.Files = names
	}
	if images, ok := k.Repo.(RepositoryImages); ok {
		names, err := images.ListImages(ctx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return nil, err
		}
		if names == nil {
			names = []string{}
		}
		view.Images = names
	}
	return view, nil
}

// NodeExists reports whether id is a fully written node (content present), as
// opposed to a bare reservation directory left behind by FsRepo.Next() or
// FsRepo.WithNodeLock(). It holds no node lock; mutating operations re-check
// under lock.
func (k *LocalKeg) NodeExists(ctx context.Context, id NodeId) (bool, error) {
	return k.nodeExistsWithContent(ctx, id)
}

// GetMetaRaw returns the node's metadata bytes exactly as stored, preserving
// formatting for round-trip editing. A missing meta file returns ErrNotExist.
func (k *LocalKeg) GetMetaRaw(ctx context.Context, id NodeId) ([]byte, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to read node meta: %w", err)
	}
	return k.Repo.ReadMeta(ctx, id)
}

// ListNodes returns all node ids present in the keg.
func (k *LocalKeg) ListNodes(ctx context.Context) ([]NodeId, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}
	return k.Repo.ListNodes(ctx)
}

// ListIndexes returns available index artifact names.
func (k *LocalKeg) ListIndexes(ctx context.Context) ([]string, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to list indexes: %w", err)
	}
	names, err := k.Repo.ListIndexes(ctx)
	if err != nil {
		return nil, err
	}
	if hasIndexName(names, TimelineIndexName) && hasIndexName(names, DirtyIndexName) {
		return names, nil
	}
	if err := k.refreshSnapshotGeneratedIndexes(ctx); err != nil {
		return nil, fmt.Errorf("failed to refresh snapshot indexes: %w", err)
	}
	return k.Repo.ListIndexes(ctx)
}

// ReadIndex returns a raw index artifact by name (e.g. "nodes.tsv").
func (k *LocalKeg) ReadIndex(ctx context.Context, name string) ([]byte, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to read index: %w", err)
	}
	data, err := k.Repo.GetIndex(ctx, name)
	if err == nil || !isSnapshotGeneratedIndex(name) || !errors.Is(err, ErrNotExist) {
		return data, err
	}
	if err := k.refreshSnapshotGeneratedIndexes(ctx); err != nil {
		return nil, fmt.Errorf("failed to refresh snapshot indexes: %w", err)
	}
	return k.Repo.GetIndex(ctx, name)
}

// Summary returns keg-level diagnostics: node count plus per-kind asset
// totals. Asset kinds report Supported=false when the backend lacks the
// capability.
func (k *LocalKeg) Summary(ctx context.Context) (*KegSummary, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to summarize keg: %w", err)
	}

	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to list nodes: %w", err)
	}

	out := &KegSummary{NodeCount: len(ids)}

	if files, ok := k.Repo.(RepositoryFiles); ok {
		out.Files.Supported = true
		for _, id := range ids {
			names, err := files.ListFiles(ctx, id)
			if err != nil {
				if errors.Is(err, ErrNotExist) {
					continue
				}
				return nil, fmt.Errorf("unable to list files for node %s: %w", id.Path(), err)
			}
			if len(names) > 0 {
				out.Files.NodesWithAssets++
			}
			out.Files.TotalAssets += len(names)
		}
	}

	if images, ok := k.Repo.(RepositoryImages); ok {
		out.Images.Supported = true
		for _, id := range ids {
			names, err := images.ListImages(ctx, id)
			if err != nil {
				if errors.Is(err, ErrNotExist) {
					continue
				}
				return nil, fmt.Errorf("unable to list images for node %s: %w", id.Path(), err)
			}
			if len(names) > 0 {
				out.Images.NodesWithAssets++
			}
			out.Images.TotalAssets += len(names)
		}
	}

	return out, nil
}
