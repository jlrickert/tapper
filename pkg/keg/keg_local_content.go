package keg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GetContent retrieves the raw markdown content for a node.
func (k *LocalKeg) GetContent(ctx context.Context, id NodeId) ([]byte, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) ([]byte, error) { return k.getContentBytes(ctx, id) })
}

func (k *LocalKeg) getContentBytes(ctx context.Context, id NodeId) ([]byte, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to retrieve node content: %w", err)
	}

	b, err := k.Repo.ReadContent(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to read content: %w", err)
	}
	return b, nil
}

// setContentNoDex writes content for a node and updates its metadata by
// re-indexing the node locally, but does NOT write the dex to disk. This
// allows callers (e.g. Move, Remove) to batch multiple content changes and
// perform a single dex write at the end. Returns the updated NodeData if
// content changed, nil if content was identical, and any error encountered.
func (k *LocalKeg) setContentNoDex(ctx context.Context, id NodeId, data []byte) (*NodeData, error) {
	return k.setContentNoDexWithOptions(ctx, id, data, NodeWriteOptions{})
}

func (k *LocalKeg) setContentNoDexWithOptions(ctx context.Context, id NodeId, data []byte, opts NodeWriteOptions) (*NodeData, error) {
	var nodeData *NodeData
	err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		// Verify the node truly exists (has content) under the lock to
		// prevent resurrecting a concurrently removed node. HasNode
		// alone is not enough for FsRepo because WithNodeLock creates
		// the node directory as a side effect.
		exists, err := k.nodeExistsWithContent(lockCtx, id)
		if err != nil {
			return fmt.Errorf("unable to check node existence: %w", err)
		}
		if !exists {
			return fmt.Errorf("node %s not found: %w", id.Path(), ErrNotExist)
		}
		// Compare new content with existing on-disk content. Skip the
		// write entirely when bytes are identical.
		existing, readErr := k.Repo.ReadContent(lockCtx, id)
		if strings.TrimSpace(opts.Schema) == "" && readErr == nil && bytes.Equal(existing, data) {
			return nil
		}

		content, err := ParseContent(k.Runtime, data, MarkdownContentFilename)
		if err != nil {
			return fmt.Errorf("invalid content: %w", err)
		}
		meta, stats, err := k.getMetaAndStats(lockCtx, id)
		if err != nil {
			return fmt.Errorf("failed to read node metadata: %w", err)
		}
		if stats == nil {
			stats = &NodeStats{}
		}
		now := k.Runtime.Clock().Now()
		proposed := &NodeData{ID: id, Content: content, Meta: meta, Stats: stats}
		if err := proposed.updateMeta(lockCtx, k.Runtime, &now); err != nil {
			return fmt.Errorf("failed to update node metadata from content: %w", err)
		}
		if _, err := k.validateNodeWrite(lockCtx, schemaWriteUpdate, id, proposed, opts.Schema,
			schemaTypeCandidateFromFrontmatter("frontmatter", content)); err != nil {
			return err
		}

		if strings.TrimSpace(opts.Schema) != "" {
			if err := k.Repo.WriteMeta(lockCtx, id, []byte(proposed.Meta.ToYAML())); err != nil {
				return fmt.Errorf("unable to write metadata: %w", err)
			}
		}
		if err := k.Repo.WriteContent(lockCtx, id, data); err != nil {
			return fmt.Errorf("unable to write content: %w", err)
		}
		updated, changed, err := k.indexNodeLocked(lockCtx, id)
		if err != nil {
			return err
		}
		if changed {
			nodeData = updated
		}
		return nil
	})
	return nodeData, err
}

// SetContent writes content for a node and updates its metadata by re-indexing.
// This ensures the node's title, lead, and other metadata are kept in sync with content changes.
func (k *LocalKeg) SetContent(ctx context.Context, id NodeId, data []byte) error {
	return k.SetContentWithOptions(ctx, id, data, NodeWriteOptions{})
}

// SetContentWithOptions replaces content while applying an explicit schema to
// persisted meta.type before validating the completed node.
func (k *LocalKeg) SetContentWithOptions(ctx context.Context, id NodeId, data []byte, opts NodeWriteOptions) error {
	return k.withKegWrite(ctx, func(ctx context.Context) error { return k.setContent(ctx, id, data, opts) })
}

func (k *LocalKeg) setContent(ctx context.Context, id NodeId, data []byte, opts NodeWriteOptions) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to set node content: %w", err)
	}

	nodeData, err := k.setContentNoDexWithOptions(ctx, id, data, opts)
	if err != nil {
		return err
	}
	if nodeData == nil {
		return nil
	}
	if err := k.writeNodeToDex(ctx, nodeData, time.Time{}); err != nil {
		return err
	}
	return k.refreshDirtyIndex(ctx)
}

// GetMeta retrieves the parsed metadata for a node.
func (k *LocalKeg) GetMeta(ctx context.Context, id NodeId) (*NodeMeta, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) (*NodeMeta, error) { return k.getMetaValue(ctx, id) })
}

func (k *LocalKeg) getMetaValue(ctx context.Context, id NodeId) (*NodeMeta, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to get node meta: %w", err)
	}
	return k.getMeta(ctx, id)
}

// GetStats retrieves programmatic node stats for a node.
func (k *LocalKeg) GetStats(ctx context.Context, id NodeId) (*NodeStats, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) (*NodeStats, error) { return k.getStatsValue(ctx, id) })
}

func (k *LocalKeg) getStatsValue(ctx context.Context, id NodeId) (*NodeStats, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, fmt.Errorf("failed to get node stats: %w", err)
	}
	stats, err := k.getStats(ctx, id)
	if err != nil {
		return nil, err
	}
	if stats == nil {
		stats = &NodeStats{}
	}
	return stats, nil
}

// SetMeta writes metadata for a node and updates the dex.
// If the new meta bytes are identical to the existing on-disk meta,
// the write and dex/config update are skipped entirely.
func (k *LocalKeg) SetMeta(ctx context.Context, id NodeId, meta *NodeMeta) error {
	return k.SetMetaWithOptions(ctx, id, meta, NodeWriteOptions{})
}

// SetMetaWithOptions replaces metadata while applying an explicit schema to
// persisted meta.type before validating the completed node.
func (k *LocalKeg) SetMetaWithOptions(ctx context.Context, id NodeId, meta *NodeMeta, opts NodeWriteOptions) error {
	return k.withKegWrite(ctx, func(ctx context.Context) error { return k.setMeta(ctx, id, meta, opts) })
}

func (k *LocalKeg) setMeta(ctx context.Context, id NodeId, meta *NodeMeta, opts NodeWriteOptions) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to update node meta: %w", err)
	}

	var updatedAt time.Time
	var nodeData *NodeData
	err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		// Verify the node truly exists (has content) under the lock to
		// prevent resurrecting a concurrently removed node.
		exists, err := k.nodeExistsWithContent(lockCtx, id)
		if err != nil {
			return fmt.Errorf("unable to check node existence: %w", err)
		}
		if !exists {
			return fmt.Errorf("node %s not found: %w", id.Path(), ErrNotExist)
		}
		stats, err := k.getStats(lockCtx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return fmt.Errorf("failed to read node stats: %w", err)
		}
		if stats == nil {
			stats = &NodeStats{}
		}

		if meta == nil {
			meta = NewMeta(lockCtx, time.Time{})
		}
		submittedType := schemaTypeCandidateFromMeta("metadata", meta)
		if selected := strings.TrimSpace(opts.Schema); selected != "" {
			if err := meta.Set(lockCtx, "type", selected); err != nil {
				return fmt.Errorf("failed to apply schema: %w", err)
			}
		}
		newMetaBytes := []byte(meta.ToYAML())

		content, err := k.getContent(lockCtx, id)
		if err != nil {
			return fmt.Errorf("failed to read node content: %w", err)
		}
		validationMeta, err := ParseMeta(lockCtx, newMetaBytes)
		if err != nil {
			return fmt.Errorf("failed to parse node metadata: %w", err)
		}
		updatedAt = k.Runtime.Clock().Now()
		proposed := &NodeData{ID: id, Content: content, Meta: validationMeta, Stats: stats}
		if err := proposed.updateMeta(lockCtx, k.Runtime, &updatedAt); err != nil {
			return fmt.Errorf("failed to update node metadata from content: %w", err)
		}
		if _, err := k.validateNodeWrite(lockCtx, schemaWriteUpdate, id, proposed, opts.Schema, submittedType); err != nil {
			return err
		}

		// Compare after applying and validating schema selection. A strict
		// blocking write with no selection must not bypass validation merely
		// because the submitted bytes happen to match storage.
		existingMeta, readErr := k.Repo.ReadMeta(lockCtx, id)
		if readErr == nil && bytes.Equal(bytes.TrimSpace(existingMeta), bytes.TrimSpace(newMetaBytes)) {
			return nil
		}
		stats.SetUpdated(updatedAt)

		if err := k.Repo.WriteMeta(lockCtx, id, newMetaBytes); err != nil {
			return fmt.Errorf("UpdateMeta: write meta to backend %s: %w", k.Repo.Name(), err)
		}
		if err := k.Repo.WriteStats(lockCtx, id, stats); err != nil {
			return fmt.Errorf("UpdateMeta: write stats to backend %s: %w", k.Repo.Name(), err)
		}

		// Read content so the dex entry has complete data (links, title
		// fallback). Without content, link indexes would be lost when
		// SetContent later skips due to unchanged hash.
		content, _ = k.getContent(lockCtx, id)

		nodeData = &NodeData{ID: id, Content: content, Meta: meta, Stats: stats}
		return nil
	})
	if err != nil {
		return err
	}

	// nodeData is nil when meta was unchanged — skip dex and config update.
	if nodeData == nil {
		return nil
	}

	if err := k.writeNodeToDex(ctx, nodeData, updatedAt); err != nil {
		return err
	}
	return k.refreshDirtyIndex(ctx)
}

// UpdateMeta reads the node's metadata, applies the provided mutation function,
// and writes the result back to the repository with dex updates.
func (k *LocalKeg) UpdateMeta(ctx context.Context, id NodeId, f func(*NodeMeta)) error {
	return k.UpdateMetaWithOptions(ctx, id, f, NodeWriteOptions{})
}

// UpdateMetaWithOptions atomically reads, mutates, validates, and writes
// metadata while applying an explicit schema to persisted meta.type.
func (k *LocalKeg) UpdateMetaWithOptions(ctx context.Context, id NodeId, f func(*NodeMeta), opts NodeWriteOptions) error {
	return k.withKegWrite(ctx, func(ctx context.Context) error { return k.updateMeta(ctx, id, f, opts) })
}

func (k *LocalKeg) updateMeta(ctx context.Context, id NodeId, f func(*NodeMeta), opts NodeWriteOptions) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to update node meta: %w", err)
	}

	now := k.Runtime.Clock().Now()

	var nodeData *NodeData
	err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		// Verify the node truly exists (has content) under the lock to
		// prevent resurrecting a concurrently removed node.
		exists, exErr := k.nodeExistsWithContent(lockCtx, id)
		if exErr != nil {
			return fmt.Errorf("unable to check node existence: %w", exErr)
		}
		if !exists {
			return fmt.Errorf("node %s not found: %w", id.Path(), ErrNotExist)
		}
		m, stats, err := k.getMetaAndStats(lockCtx, id)
		if errors.Is(err, ErrNotExist) {
			m = NewMeta(lockCtx, now)
			stats = NewStats(now)
		} else if err != nil {
			return fmt.Errorf("failed to read node metadata: %w", err)
		}
		if stats == nil {
			stats = &NodeStats{}
		}

		beforeMetaBytes := []byte(m.ToYAML())
		beforeType := schemaTypeCandidateFromMeta("metadata", m)
		f(m)
		afterType := schemaTypeCandidateFromMeta("metadata", m)
		var submittedType schemaTypeCandidate
		if afterType.Value != beforeType.Value {
			submittedType = afterType
		}
		if selected := strings.TrimSpace(opts.Schema); selected != "" {
			if err := m.Set(lockCtx, "type", selected); err != nil {
				return fmt.Errorf("failed to apply schema: %w", err)
			}
		}
		newMetaBytes := []byte(m.ToYAML())

		content, _ := k.getContent(lockCtx, id)
		validationMeta, err := ParseMeta(lockCtx, newMetaBytes)
		if err != nil {
			return fmt.Errorf("failed to parse node metadata: %w", err)
		}
		proposed := &NodeData{ID: id, Content: content, Meta: validationMeta, Stats: stats}
		if err := proposed.updateMeta(lockCtx, k.Runtime, &now); err != nil {
			return fmt.Errorf("failed to update node metadata from content: %w", err)
		}
		if _, err := k.validateNodeWrite(lockCtx, schemaWriteUpdate, id, proposed, opts.Schema, submittedType); err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(beforeMetaBytes), bytes.TrimSpace(newMetaBytes)) {
			return nil
		}
		stats.SetUpdated(now)

		if err := k.Repo.WriteMeta(lockCtx, id, newMetaBytes); err != nil {
			return fmt.Errorf("UpdateMeta: write meta to backend %s: %w", k.Repo.Name(), err)
		}
		if err := k.Repo.WriteStats(lockCtx, id, stats); err != nil {
			return fmt.Errorf("UpdateMeta: write stats to backend %s: %w", k.Repo.Name(), err)
		}

		// Read content so the dex entry has complete data (links, title).
		nodeData = &NodeData{ID: id, Content: content, Meta: m, Stats: stats}
		return nil
	})
	if err != nil {
		return err
	}
	if nodeData == nil {
		return nil
	}
	if err := k.writeNodeToDex(ctx, nodeData, now); err != nil {
		return err
	}
	return k.refreshDirtyIndex(ctx)
}

// Touch updates the access time of a node to the current time.
func (k *LocalKeg) Touch(ctx context.Context, id NodeId) error {
	return k.withKegWrite(ctx, func(ctx context.Context) error { return k.touch(ctx, id) })
}

func (k *LocalKeg) touch(ctx context.Context, id NodeId) error {
	if err := k.checkKegExists(ctx); err != nil {
		return fmt.Errorf("failed to touch node: %w", err)
	}

	now := k.Runtime.Clock().Now()

	if err := k.withNodeLock(ctx, id, func(lockCtx context.Context) error {
		// Verify the node truly exists (has content) under the lock.
		exists, exErr := k.nodeExistsWithContent(lockCtx, id)
		if exErr != nil {
			return fmt.Errorf("unable to check node existence: %w", exErr)
		}
		if !exists {
			return fmt.Errorf("node %s not found: %w", id.Path(), ErrNotExist)
		}

		meta, stats, err := k.getMetaAndStats(lockCtx, id)
		if errors.Is(err, ErrNotExist) {
			meta = NewMeta(lockCtx, now)
			stats = NewStats(now)
		} else if err != nil {
			return fmt.Errorf("failed to read node metadata: %w", err)
		}
		if stats == nil {
			stats = &NodeStats{}
		}

		stats.SetAccessed(now)
		stats.IncrementAccessCount()
		stats.EnsureTimes(now)
		if err := k.Repo.WriteMeta(lockCtx, id, []byte(meta.ToYAML())); err != nil {
			return err
		}
		return k.Repo.WriteStats(lockCtx, id, stats)
	}); err != nil {
		return err
	}
	return k.refreshDirtyIndex(ctx)
}

// Node returns a Node handle bound to this keg's repository and runtime for the
// given id. It performs no I/O; content, metadata, and stats are loaded lazily
// by the Node's own methods.
func (k *LocalKeg) Node(id NodeId) *Node {
	// id is already the correct local identifier (ID + optional Code). Do not
	// stamp the keg name onto it: a keg's own indexes store bare numeric IDs,
	// and the keg: prefix would leak into dex/nodes.tsv (and the tags/links/
	// backlinks/changes sub-indexes) via NodeId.Path(). Cross-keg addressing is
	// handled separately by NodeRef.
	return &Node{
		ID:      id,
		Repo:    k.Repo,
		Runtime: k.Runtime,
	}
}
