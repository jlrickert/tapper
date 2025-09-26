package keg_test

import (
	"bytes"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestMemoryRepo_WriteReadMetaAndContent(t *testing.T) {
	t.Parallel()
	fx := NewFixture(t)

	r := keg.NewMemoryRepo()
	ctx := fx.ctx

	id := keg.Node{ID: 10}
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

func TestMemoryRepo_ReadMissingReturnsNotFound(t *testing.T) {
	t.Parallel()
	fx := NewFixture(t)

	r := keg.NewMemoryRepo()
	ctx := fx.ctx

	missing := keg.Node{ID: 9999}

	_, err := r.ReadContent(ctx, missing)
	require.Error(t, err)
	require.ErrorIs(t, err, keg.ErrNodeNotFound)
}

func TestMemoryRepo_WriteAndListIndexes_GetIndex(t *testing.T) {
	t.Parallel()
	fx := NewFixture(t)

	r := keg.NewMemoryRepo()
	ctx := fx.ctx

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
	fx := NewFixture(t)

	r := keg.NewMemoryRepo()
	ctx := fx.ctx

	src := keg.Node{ID: 20}
	dst := keg.Node{ID: 30}
	other := keg.Node{ID: 31}
	content := []byte("content")

	// prepare src node
	require.NoError(t, r.WriteContent(ctx, src, content))
	require.NoError(t, r.WriteMeta(ctx, src, []byte("title: src\n")))

	// moving to an unused dst should succeed
	require.NoError(t, r.MoveNode(ctx, src, dst))

	// src should no longer exist
	_, err := r.ReadContent(ctx, src)
	require.ErrorIs(t, err, keg.ErrNodeNotFound)

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
	fx := NewFixture(t)

	r := keg.NewMemoryRepo()
	ctx := fx.ctx

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
