package keg_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
)

func containsID(list []keg.NodeID, id keg.NodeID) bool {
	return slices.Contains(list, id)
}

func TestMemoryRepo_WriteReadMetaAndContent(t *testing.T) {
	t.Parallel()
	r := keg.NewMemoryRepo()
	ctx := context.Background()

	id := keg.NodeID(10)
	content := []byte("# hello\n")
	meta := []byte("title: test\nupdated: 2025-08-11 00:00:00Z\n")

	if err := r.WriteContent(ctx, id, content); err != nil {
		t.Fatalf("WriteContent failed: %v", err)
	}
	if err := r.WriteMeta(ctx, id, meta); err != nil {
		t.Fatalf("WriteMeta failed: %v", err)
	}

	gotMeta, err := r.ReadMeta(ctx, id)
	if err != nil {
		t.Fatalf("ReadMeta failed: %v", err)
	}
	if !bytes.Equal(gotMeta, meta) {
		t.Fatalf("meta mismatch: got=%q want=%q", string(gotMeta), string(meta))
	}

	gotContent, err := r.ReadContent(ctx, id)
	if err != nil {
		t.Fatalf("ReadContent failed: %v", err)
	}
	if !bytes.Equal(gotContent, content) {
		t.Fatalf("content mismatch: got=%q want=%q", string(gotContent), string(content))
	}

	ids, err := r.ListNodes(ctx)
	if err != nil {
		t.Fatalf("ListNodes failed: %v", err)
	}
	if !containsID(ids, id) {
		t.Fatalf("expected ListNodes to contain %v, got %v", id, ids)
	}
}

func TestMemoryRepo_ReadMissingReturnsNotFound(t *testing.T) {
	t.Parallel()
	r := keg.NewMemoryRepo()
	ctx := context.Background()

	missing := keg.NodeID(9999)

	if _, err := r.ReadContent(ctx, missing); err == nil {
		t.Fatalf("expected ReadContent to fail for missing id")
	} else {
		if !errors.Is(err, keg.ErrNodeNotFound) {
			t.Fatalf("expected errors.Is(err, ErrNodeNotFound) true; got err=%v", err)
		}
		if !errors.Is(err, keg.ErrNodeNotFound) {
			t.Fatalf("expected errors.As to extract *NodeNotFoundError; got err=%v", err)
		}
	}
}

func TestMemoryRepo_WriteAndListIndexes_GetIndex(t *testing.T) {
	t.Parallel()
	r := keg.NewMemoryRepo()
	ctx := context.Background()

	name := "dex/nodes.tsv"
	data := []byte("1\t2025-08-11 00:00:00Z\tTitle\n")
	if err := r.WriteIndex(ctx, name, data); err != nil {
		t.Fatalf("WriteIndex failed: %v", err)
	}

	got, err := r.GetIndex(ctx, name)
	if err != nil {
		t.Fatalf("GetIndex failed: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("index data mismatch")
	}

	list, err := r.ListIndexes(ctx)
	if err != nil {
		t.Fatalf("ListIndexes failed: %v", err)
	}
	found := slices.Contains(list, name)
	if !found {
		t.Fatalf("expected ListIndexes to include %q; got %v", name, list)
	}
}

func TestMemoryRepo_MoveNodeAndDestinationExists(t *testing.T) {
	t.Parallel()
	r := keg.NewMemoryRepo()
	ctx := context.Background()

	src := keg.NodeID(20)
	dst := keg.NodeID(30)
	other := keg.NodeID(31)
	content := []byte("content")

	// prepare src and other(dst exists) nodes
	if err := r.WriteContent(ctx, src, content); err != nil {
		t.Fatalf("WriteContent(src) failed: %v", err)
	}
	if err := r.WriteMeta(ctx, src, []byte("title: src\n")); err != nil {
		t.Fatalf("WriteMeta(src) failed: %v", err)
	}

	// moving to an unused dst should succeed
	if err := r.MoveNode(ctx, src, dst); err != nil {
		t.Fatalf("MoveNode(src->dst) failed: %v", err)
	}

	// src should no longer exist, dst should
	if _, err := r.ReadContent(ctx, src); err == nil {
		t.Fatalf("expected src to be gone after move")
	} else if !errors.Is(err, keg.ErrNodeNotFound) {
		t.Fatalf("unexpected error when reading moved-from src: %v", err)
	}

	got, err := r.ReadContent(ctx, dst)
	if err != nil {
		t.Fatalf("expected dst to exist after move: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("moved content mismatch")
	}

	// create another node at 'other' and attempt to move dst -> other to force destination-exists
	if err := r.WriteContent(ctx, other, []byte("x")); err != nil {
		t.Fatalf("WriteContent(other) failed: %v", err)
	}
	if err := r.WriteMeta(ctx, other, []byte("title: other\n")); err != nil {
		t.Fatalf("WriteMeta(other) failed: %v", err)
	}

	if err := r.MoveNode(ctx, dst, other); err == nil {
		t.Fatalf("expected MoveNode to fail when destination exists")
	} else {
		if !errors.Is(err, keg.ErrDestinationExists) {
			t.Fatalf("expected ErrDestinationExists; got %v", err)
		}
	}
}
