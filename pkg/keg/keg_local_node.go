package keg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"
)

func (k *LocalKeg) withNodeLock(ctx context.Context, id NodeId, fn func(context.Context) error) error {
	if contextHasNodeLock(ctx, id) {
		return fn(ctx)
	}
	return k.Repo.WithNodeLock(ctx, id, fn)
}

// nodeExistsWithContent checks whether a node truly exists by verifying it has
// content (or at minimum an entry in the repo). For FsRepo, WithNodeLock
// creates a bare directory as a side effect of lock acquisition, so HasNode
// alone is insufficient — a bare directory without README.md is not a real
// node. This helper reads the content file to confirm the node was properly
// created via Create/Init and not merely a lock artifact.
func (k *LocalKeg) nodeExistsWithContent(ctx context.Context, id NodeId) (bool, error) {
	_, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (k *LocalKeg) nodeFilesMissing(ctx context.Context, id NodeId) (bool, bool, error) {
	exists, err := k.Repo.HasNode(ctx, id)
	if err != nil {
		return false, false, fmt.Errorf("failed to probe node existence for %s: %w", id.Path(), err)
	}
	if !exists {
		return true, true, nil
	}

	rawMeta, err := k.Repo.ReadMeta(ctx, id)
	metaMissing := err != nil || len(bytes.TrimSpace(rawMeta)) == 0

	_, statsErr := k.Repo.ReadStats(ctx, id)
	statsMissing := statsErr != nil

	return metaMissing, statsMissing, nil
}

// getContent retrieves and parses raw markdown content for a node.
func (k *LocalKeg) getContent(ctx context.Context, id NodeId) (*NodeContent, error) {
	raw, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		return nil, err
	}
	return ParseContent(k.Runtime, raw, FormatMarkdown)
}

// getMeta retrieves and parses YAML metadata for a node.
func (k *LocalKeg) getMeta(ctx context.Context, id NodeId) (*NodeMeta, error) {
	raw, err := k.Repo.ReadMeta(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return NewMeta(ctx, time.Time{}), nil
		}
		return nil, err
	}
	return ParseMeta(ctx, raw)
}

func (k *LocalKeg) getStats(ctx context.Context, id NodeId) (*NodeStats, error) {
	stats, err := k.Repo.ReadStats(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return &NodeStats{}, nil
		}
		return nil, err
	}
	return stats, nil
}

func (k *LocalKeg) getMetaAndStats(ctx context.Context, id NodeId) (*NodeMeta, *NodeStats, error) {
	meta, err := k.getMeta(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	stats, err := k.getStats(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return meta, stats, nil
}

// getNodeBestEffort builds a NodeData by loading each component independently.
// Unlike getNode, it does not abort on the first error. Whichever components
// load successfully are populated; failed components are left nil. The returned
// errors slice contains one wrapped error per failed component.
func (k *LocalKeg) getNodeBestEffort(ctx context.Context, n NodeId) (*NodeData, []error) {
	var errs []error
	data := &NodeData{ID: n}

	content, err := k.getContent(ctx, n)
	if err != nil {
		errs = append(errs, fmt.Errorf("node %s content: %w", n.Path(), err))
	} else {
		data.Content = content
	}

	meta, stats, err := k.getMetaAndStats(ctx, n)
	if err != nil {
		errs = append(errs, fmt.Errorf("node %s meta/stats: %w", n.Path(), err))
	} else {
		data.Meta = meta
		data.Stats = stats
	}

	items, err := repoListFiles(ctx, k.Repo, n)
	if err != nil {
		errs = append(errs, fmt.Errorf("node %s files: %w", n.Path(), err))
	} else {
		data.Items = items
	}

	images, err := repoListImages(ctx, k.Repo, n)
	if err != nil {
		errs = append(errs, fmt.Errorf("node %s images: %w", n.Path(), err))
	} else {
		data.Images = images
	}

	return data, errs
}

// getNode builds a complete NodeData from a node's content, metadata, and attachments.
func (k *LocalKeg) getNode(ctx context.Context, n NodeId) (*NodeData, error) {
	content, err := k.getContent(ctx, n)
	if err != nil {
		return nil, err
	}
	meta, stats, err := k.getMetaAndStats(ctx, n)
	if err != nil {
		return nil, err
	}

	items, err := repoListFiles(ctx, k.Repo, n)
	if err != nil {
		return nil, err
	}

	images, err := repoListImages(ctx, k.Repo, n)
	if err != nil {
		return nil, err
	}

	return &NodeData{
		ID:      n,
		Content: content,
		Meta:    meta,
		Stats:   stats,
		Items:   items,
		Images:  images,
	}, nil
}
