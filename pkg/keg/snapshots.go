package keg

import (
	"context"
	"fmt"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

func cloneBytes(data []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out
}

func hashSnapshotBytes(rt *toolkit.Runtime, data []byte) string {
	if rt == nil || len(data) == 0 {
		return ""
	}
	return rt.Hasher().Hash(data)
}

func snapshotStatsToBytes(stats *NodeStats) ([]byte, error) {
	if stats == nil {
		return nil, nil
	}
	out, err := stats.ToJSON()
	if err != nil {
		return nil, err
	}
	return out, nil
}

func snapshotStatsFromBytes(ctx context.Context, raw []byte) (*NodeStats, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	stats, err := ParseStats(ctx, raw)
	if err != nil {
		return nil, err
	}
	return stats, nil
}

func snapshotWriteHashes(rt *toolkit.Runtime, content []byte, meta []byte, stats []byte) (contentHash string, metaHash string, statsHash string) {
	return hashSnapshotBytes(rt, content), hashSnapshotBytes(rt, meta), hashSnapshotBytes(rt, stats)
}

func normalizeSnapshotWrite(ctx context.Context, rt *toolkit.Runtime, in SnapshotWrite) (content []byte, meta []byte, statsBytes []byte, err error) {
	content = cloneBytes(in.Content.Data)
	meta = cloneBytes(in.Meta)
	statsBytes, err = snapshotStatsToBytes(in.Stats)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode snapshot stats: %w", err)
	}

	// Preserve existing stats bytes if callers passed nil stats and explicit hashes
	// are not available. Current callers provide structured stats.
	if in.Stats == nil && len(statsBytes) == 0 {
		statsBytes = nil
	}

	_ = ctx
	_ = rt
	return content, meta, statsBytes, nil
}
