package keg_test

import (
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	tookit "github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestFsRepo_WriteReadMetaAndContent(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, sandbox.WithFixture("empty", "~/empty"))
	ctx := fx.Context()

	r := &keg.FsRepo{
		Root:            "~/empty",
		ContentFilename: keg.MarkdownContentFilename,
		MetaFilename:    keg.YAMLMetaFilename,
	}

	fx.DumpJailTree(0)
	id := keg.NodeId{ID: 10}
	content := []byte("# hello\n")
	meta := []byte("title: test\nupdated: 2025-08-11 00:00:00Z\n")

	// Write content (creates node dir)
	require.NoError(t, r.WriteContent(ctx, id, content))
	// WriteMeta expects node dir to exist (WriteContent created it).
	require.NoError(t, r.WriteMeta(ctx, id, meta))

	gotContent, err := r.ReadContent(ctx, id)
	require.NoError(t, err)
	require.Equal(t, string(content), string(gotContent))

	gotMeta, err := r.ReadMeta(ctx, id)
	require.NoError(t, err)
	require.Equal(t, string(meta), string(gotMeta))
}

func TestFsRepo_NextAndListNodes(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t,
		sandbox.WithFixture("home", "/home"),
		sandbox.WithWd("~/repofs_fs"),
	)
	fx.DumpJailTree(0)
	ctx := fx.Context()

	r := &keg.FsRepo{
		Root:            "~/repofs_fs",
		ContentFilename: keg.MarkdownContentFilename,
		MetaFilename:    keg.YAMLMetaFilename,
	}

	next, err := r.Next(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, int(next.ID), 1)

	ids, err := r.ListNodes(ctx)
	require.NoError(t, err)

	// expect to contain 0 and 1
	found0 := false
	found1 := false
	for _, n := range ids {
		if n.ID == 0 {
			found0 = true
		}
		if n.ID == 1 {
			found1 = true
		}
	}
	require.True(t, found0, "expected to find node 0")
	require.True(t, found1, "expected to find node 1")
}

func TestFsRepo_MoveDeleteNodeAndDestinationExists(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	tmp := t.TempDir()
	// Use std.Mkdir to avoid direct os package functions.
	require.NoError(t, tookit.Mkdir(ctx, tmp, 0o755, true))

	r := &keg.FsRepo{
		Root:            tmp,
		ContentFilename: keg.MarkdownContentFilename,
		MetaFilename:    keg.YAMLMetaFilename,
	}

	src := keg.NodeId{ID: 20}
	dst := keg.NodeId{ID: 30}
	other := keg.NodeId{ID: 31}
	content := []byte("content")

	// prepare src node
	require.NoError(t, r.WriteContent(ctx, src, content))
	require.NoError(t, r.WriteMeta(ctx, src, []byte("title: src\n")))

	// move to dst
	require.NoError(t, r.MoveNode(ctx, src, dst))

	// src should no longer exist
	_, err := r.ReadContent(ctx, src)
	require.Error(t, err)
	require.ErrorIs(t, err, keg.ErrNotExist)

	// dst should have content
	got, err := r.ReadContent(ctx, dst)
	require.NoError(t, err)
	require.Equal(t, content, got)

	// create other and attempt move dst -> other to force destination-exists
	require.NoError(t, r.WriteContent(ctx, other, []byte("x")))
	require.NoError(t, r.WriteMeta(ctx, other, []byte("title: other\n")))

	err = r.MoveNode(ctx, dst, other)
	require.Error(t, err)
	require.ErrorIs(t, err, keg.ErrDestinationExists)

	// DeleteNode should remove node
	require.NoError(t, r.DeleteNode(ctx, other))
	_, err = r.ReadContent(ctx, other)
	require.Error(t, err)
	require.ErrorIs(t, err, keg.ErrNotExist)
}

func TestFsRepo_UploadAndListImagesAndItems(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	tmp := t.TempDir()
	require.NoError(t, tookit.Mkdir(ctx, tmp, 0o755, true))

	r := &keg.FsRepo{
		Root:            tmp,
		ContentFilename: keg.MarkdownContentFilename,
		MetaFilename:    keg.YAMLMetaFilename,
	}

	id := keg.NodeId{ID: 40}
	// ensure node exists
	require.NoError(t, r.WriteContent(ctx, id, []byte("c")))
	require.NoError(t, r.WriteMeta(ctx, id, []byte("title: i\n")))

	// images
	require.NoError(t, r.UploadImage(ctx, id, "a.png", []byte("pngdata")))
	require.NoError(t, r.UploadImage(ctx, id, "b.jpg", []byte("jpgdata")))

	images, err := r.ListImages(ctx, id)
	require.NoError(t, err)
	require.Contains(t, images, "a.png")
	require.Contains(t, images, "b.jpg")

	// items
	require.NoError(t, r.UploadItem(ctx, id, "attach.txt", []byte("data")))
	items, err := r.ListItems(ctx, id)
	require.NoError(t, err)
	require.Contains(t, items, "attach.txt")
}

func TestFsRepo_WriteGetAndListIndexes(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, sandbox.WithFixture("example", "~/example"))
	ctx := fx.Context()

	r := &keg.FsRepo{
		Root:            "~/example",
		ContentFilename: keg.MarkdownContentFilename,
		MetaFilename:    keg.YAMLMetaFilename,
	}

	data, err := r.GetIndex(ctx, "nodes.tsv")
	require.NoError(t, err, "expect to be able to read nodes.tsv index")
	require.Equal(t, string(data), "0\t2025-10-04 18:30:01Z\tSorry, planned but not yet available\n")
}

func TestFsRepo_WriteReadStats(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	tmp := t.TempDir()
	require.NoError(t, tookit.Mkdir(ctx, tmp, 0o755, true))

	r := &keg.FsRepo{
		Root:            tmp,
		ContentFilename: keg.MarkdownContentFilename,
		MetaFilename:    keg.YAMLMetaFilename,
	}

	id := keg.NodeId{ID: 88}
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
