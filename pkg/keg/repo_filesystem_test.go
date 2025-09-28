package keg_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestFsRepo_WriteReadMetaAndContent(t *testing.T) {
	t.Parallel()
	fx := NewFixture(t)
	ctx := fx.ctx

	tmp := t.TempDir()
	// ensure root exists
	require.NoError(t, os.MkdirAll(tmp, 0o755))

	r := &keg.FsRepo{
		Root:            tmp,
		ContentFilename: keg.MarkdownContentFilename,
		MetaFilename:    keg.YAMLMetaFilename,
	}

	id := keg.Node{ID: 10}
	content := []byte("# hello\n")
	meta := []byte("title: test\nupdated: 2025-08-11 00:00:00Z\n")

	// Write content (creates node dir)
	require.NoError(t, r.WriteContent(ctx, id, content))

	// WriteMeta expects node dir to exist (WriteContent created it).
	require.NoError(t, r.WriteMeta(ctx, id, meta))

	gotMeta, err := r.ReadMeta(ctx, id)
	require.NoError(t, err)
	require.Equal(t, meta, gotMeta)

	gotContent, err := r.ReadContent(ctx, id)
	require.NoError(t, err)
	require.Equal(t, content, gotContent)
}

func TestFsRepo_NextAndListNodes(t *testing.T) {
	t.Parallel()
	fx := NewFixture(t)
	ctx := fx.ctx

	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(tmp, 0o755))

	// create some numeric node dirs to exercise Next/ListNodes
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "0"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "5"), 0o755))

	r := &keg.FsRepo{
		Root:            tmp,
		ContentFilename: keg.MarkdownContentFilename,
		MetaFilename:    keg.YAMLMetaFilename,
	}

	next, err := r.Next(ctx)
	require.NoError(t, err)
	// highest existing id is 5, so next should be 6
	require.GreaterOrEqual(t, int(next.ID), 6)

	ids, err := r.ListNodes(ctx)
	require.NoError(t, err)

	// expect to contain 0 and 5
	found0 := false
	found5 := false
	for _, n := range ids {
		if n.ID == 0 {
			found0 = true
		}
		if n.ID == 5 {
			found5 = true
		}
	}
	require.True(t, found0, "expected to find node 0")
	require.True(t, found5, "expected to find node 5")
}

func TestFsRepo_MoveDeleteNodeAndDestinationExists(t *testing.T) {
	t.Parallel()
	fx := NewFixture(t)
	ctx := fx.ctx

	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(tmp, 0o755))

	r := &keg.FsRepo{
		Root:            tmp,
		ContentFilename: keg.MarkdownContentFilename,
		MetaFilename:    keg.YAMLMetaFilename,
	}

	src := keg.Node{ID: 20}
	dst := keg.Node{ID: 30}
	other := keg.Node{ID: 31}
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
	fx := NewFixture(t)
	ctx := fx.ctx

	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(tmp, 0o755))

	r := &keg.FsRepo{
		Root:            tmp,
		ContentFilename: keg.MarkdownContentFilename,
		MetaFilename:    keg.YAMLMetaFilename,
	}

	id := keg.Node{ID: 40}
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
	fx := NewFixture(t)
	ctx := fx.ctx

	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(tmp, 0o755))

	r := &keg.FsRepo{
		Root:            tmp,
		ContentFilename: keg.MarkdownContentFilename,
		MetaFilename:    keg.YAMLMetaFilename,
	}

	name := "nodes.tsv"
	data := []byte("1\t2025-08-11 00:00:00Z\tTitle\n")

	require.NoError(t, r.WriteIndex(ctx, name, data))

	got, err := r.GetIndex(ctx, name)
	require.NoError(t, err)
	require.Equal(t, data, got)

	list, err := r.ListIndexes(ctx)
	require.NoError(t, err)
	require.Contains(t, list, "nodes.tsv")
}

func TestFsRepo_ClearLocksAndNodeLockBehavior(t *testing.T) {
	t.Parallel()
	fx := NewFixture(t)
	ctx := fx.ctx

	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(tmp, 0o755))

	r := &keg.FsRepo{
		Root:            tmp,
		ContentFilename: keg.MarkdownContentFilename,
		MetaFilename:    keg.YAMLMetaFilename,
	}

	// create a repo-level lock file and a per-node lock file
	rootLock := filepath.Join(tmp, keg.KegLockFile)
	require.NoError(t, os.WriteFile(rootLock, []byte("lock"), 0o600))

	nodeDir := filepath.Join(tmp, "50")
	require.NoError(t, os.MkdirAll(nodeDir, 0o755))
	nodeLock := filepath.Join(nodeDir, keg.KegLockFile)
	require.NoError(t, os.WriteFile(nodeLock, []byte("l"), 0o600))

	// ClearLocks should remove both
	require.NoError(t, r.ClearLocks())
	_, err := os.Stat(rootLock)
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(nodeLock)
	require.True(t, os.IsNotExist(err))

	// Test LockNode behavior when lock is held by other process (simulate by
	// creating the lock file)
	require.NoError(t, os.WriteFile(nodeLock, []byte("held"), 0o600))
	// LockNode will attempt and eventually time out when context cancels.
	ctxTimeout, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	_, err = r.LockNode(ctxTimeout, keg.Node{ID: 50}, 50*time.Millisecond)
	require.Error(t, err)
	// underlying error should wrap ErrLockTimeout
	require.True(t, errors.Is(err, keg.ErrLockTimeout))

	// Clear the node lock via ClearNodeLock and ensure file removed
	require.NoError(t, r.ClearNodeLock(ctx, keg.Node{ID: 50}))
	_, err = os.Stat(nodeLock)
	require.True(t, os.IsNotExist(err))
}
