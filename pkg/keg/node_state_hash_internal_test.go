package keg

import (
	"context"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/stretchr/testify/require"
)

func TestNodeStateHashIncludesContentAndManualMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt, err := toolkit.NewTestRuntime(t.TempDir(), "/home/testuser", "testuser")
	require.NoError(t, err)

	contentA, err := ParseContent(rt, []byte("# Title\n\nbody\n"), FormatMarkdown)
	require.NoError(t, err)
	contentB, err := ParseContent(rt, []byte("# Title\n\nchanged\n"), FormatMarkdown)
	require.NoError(t, err)
	metaA, err := ParseMeta(ctx, []byte("status: draft\n"))
	require.NoError(t, err)
	metaB, err := ParseMeta(ctx, []byte("status: ready\n"))
	require.NoError(t, err)

	base := nodeStateHash(rt, contentA.Hash, metaA)
	require.NotEmpty(t, base)
	require.NotEqual(t, base, contentA.Hash)
	require.NotEqual(t, base, nodeStateHash(rt, contentB.Hash, metaA))
	require.NotEqual(t, base, nodeStateHash(rt, contentA.Hash, metaB))
	require.Equal(t, base, nodeStateHash(rt, contentA.Hash, metaA))
}

func TestNodeStatsHashIgnoresStatsOnlyFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	later := now.Add(time.Hour)
	rt, err := toolkit.NewTestRuntime(t.TempDir(), "/home/testuser", "testuser")
	require.NoError(t, err)

	content, err := ParseContent(rt, []byte("# Title\n\nbody\n"), FormatMarkdown)
	require.NoError(t, err)
	meta, err := ParseMeta(ctx, []byte("status: ready\n"))
	require.NoError(t, err)

	stats := NewStats(now)
	stats.UpdateFromSource(rt, content, meta, &now)
	hash := stats.Hash()
	updated := stats.Updated()

	stats.SetOmega(0.75)
	stats.SetAccessed(later)
	stats.SetAccessCount(5)
	stats.UpdateFromSource(rt, content, meta, &later)

	require.Equal(t, hash, stats.Hash())
	require.Equal(t, updated, stats.Updated())
}
