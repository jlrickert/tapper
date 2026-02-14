package keg_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestMemoryRepo_WriteReadMetaAndContent(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	r := keg.NewMemoryRepo()
	ctx := fx.Context()

	id := keg.NodeId{ID: 10}
	content := []byte("# hello\n")
	meta := []byte("title: test\nupdated: 2025-08-11 00:00:00Z\n")

	require.NoError(t, r.WriteContent(ctx, id, content))
	require.NoError(t, r.WriteMeta(ctx, id, meta))

	gotMeta, err := r.ReadMeta(ctx, id)
	require.NoError(t, err)
	require.Equal(t, meta, gotMeta, "meta bytes should match")

	gotContent, err := r.ReadContent(ctx, id)
	require.NoError(t, err)
	require.Equal(t, content, gotContent, "content bytes should match")

	ids, err := r.ListNodes(ctx)
	require.NoError(t, err)
	require.Contains(t, ids, id, "expected ListNodes to contain written id")
}

func TestMemoryRepo_WriteReadStats(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	r := keg.NewMemoryRepo()
	ctx := fx.Context()
	id := keg.NodeId{ID: 77}

	require.NoError(t, r.WriteMeta(ctx, id, []byte("title: keep-me\nfoo: bar\n")))

	now := time.Date(2026, 2, 14, 12, 0, 0, 0, time.UTC)
	stats := keg.NewStats(now)
	stats.SetHash("h1", &now)
	stats.SetLead("lead text")
	stats.SetLinks([]keg.NodeId{{ID: 1}, {ID: 2}})
	stats.SetAccessed(now)

	require.NoError(t, r.WriteStats(ctx, id, stats))

	gotStats, err := r.ReadStats(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "h1", gotStats.Hash())
	require.Equal(t, "lead text", gotStats.Lead())
	require.Len(t, gotStats.Links(), 2)

	gotMeta, err := r.ReadMeta(ctx, id)
	require.NoError(t, err)
	require.Contains(t, string(gotMeta), "title: keep-me")
	require.Contains(t, string(gotMeta), "foo: bar")
	require.Contains(t, string(gotMeta), "hash: h1")
}

func TestMemoryRepo_ReadMissingReturnsNotFound(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	r := keg.NewMemoryRepo()
	ctx := fx.Context()

	missing := keg.NodeId{ID: 9999}

	_, err := r.ReadContent(ctx, missing)
	require.Error(t, err)
	require.ErrorIs(t, err, keg.ErrNotExist)
}

func TestMemoryRepo_WriteAndListIndexes_GetIndex(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	r := keg.NewMemoryRepo()
	ctx := fx.Context()

	name := "dex/nodes.tsv"
	data := []byte("1\t2025-08-11 00:00:00Z\tTitle\n")
	require.NoError(t, r.WriteIndex(ctx, name, data))

	got, err := r.GetIndex(ctx, name)
	require.NoError(t, err)
	require.Equal(t, data, got, "index data mismatch")

	list, err := r.ListIndexes(ctx)
	require.NoError(t, err)
	require.Contains(t, list, name, "expected ListIndexes to include written index name")
}

func TestMemoryRepo_MoveNodeAndDestinationExists(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	r := keg.NewMemoryRepo()
	ctx := fx.Context()

	src := keg.NodeId{ID: 20}
	dst := keg.NodeId{ID: 30}
	other := keg.NodeId{ID: 31}
	content := []byte("content")

	// prepare src node
	require.NoError(t, r.WriteContent(ctx, src, content))
	require.NoError(t, r.WriteMeta(ctx, src, []byte("title: src\n")))

	// moving to an unused dst should succeed
	require.NoError(t, r.MoveNode(ctx, src, dst))

	// src should no longer exist
	_, err := r.ReadContent(ctx, src)
	require.ErrorIs(t, err, keg.ErrNotExist)

	// dst should exist with same content
	got, err := r.ReadContent(ctx, dst)
	require.NoError(t, err)
	require.Equal(t, content, got, "moved content mismatch")

	// create another node at 'other' and attempt to move dst -> other to force destination-exists
	require.NoError(t, r.WriteContent(ctx, other, []byte("x")))
	require.NoError(t, r.WriteMeta(ctx, other, []byte("title: other\n")))

	err = r.MoveNode(ctx, dst, other)
	require.Error(t, err)
	require.ErrorIs(t, err, keg.ErrDestinationExists)
}

func TestMemoryRepo_NextProducesIncreasingIDs(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	r := keg.NewMemoryRepo()
	ctx := fx.Context()

	// Obtain the next available ID.
	first, err := r.Next(ctx)
	require.NoError(t, err)

	// Allocate the first ID by writing content to it so subsequent Next() reflects the updated state.
	require.NoError(t, r.WriteContent(ctx, first, []byte("first")))

	// Now Next should return a strictly larger id.
	second, err := r.Next(ctx)
	require.NoError(t, err)
	require.Greater(t, int(second.ID), int(first.ID), "expected second Next() > first Next()")

	// Write content at the second id and ensure the node exists afterwards.
	content := []byte("next-test")
	require.NoError(t, r.WriteContent(ctx, second, content))
	got, err := r.ReadContent(ctx, second)
	require.NoError(t, err)
	require.Equal(t, content, got, "content mismatch for Next id")

	// Ensure ListNodes includes the written IDs.
	ids, err := r.ListNodes(ctx)
	require.NoError(t, err)
	require.Contains(t, ids, first)
	require.Contains(t, ids, second)

	// sanity: ensure bytes.Equal works as expected for content comparisons used earlier
	require.True(t, bytes.Equal(content, got))
}
