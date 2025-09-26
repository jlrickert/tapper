package keg_test

// import (
// 	"context"
// 	"errors"
// 	"slices"
// 	"testing"
//
// 	terrs "github.com/jlrickert/tapper/pkg/errors"
// 	"github.com/jlrickert/tapper/pkg/keg"
// )
//
// func TestKeg_Create_Get_UpdateMeta_NotFound(t *testing.T) {
// 	ctx := context.Background()
//
// 	// Prepare an in-memory repo and service.
// 	mem := keg.NewMemoryRepo()
// 	k := keg.NewKegWithDeps(mem, deps)
//
// 	// 1) Initiate the repo
// 	err := k.Init(t.Context())
// 	if err != nil {
// 		t.Fatalf("Init failed: %v", err)
// 	}
//
// 	// 1) Create a node with initial meta and content.
// 	content := []byte("# Test Node\n\nLead paragraph.")
//
// 	id, err := k.CreateNode(ctx, keg.NodeCreateOptions{
// 		Content: content,
// 	})
// 	if err != nil {
// 		t.Fatalf("CreateNode failed: %v", err)
// 	}
//
// 	// 2) Retrieve the node and check basic fields.
// 	n, err := k.GetNode(ctx, id)
// 	if err != nil {
// 		t.Fatalf("GetNode(%d) failed: %v", id, err)
// 	}
// 	if n.ID != id {
// 		t.Fatalf("got node id %d, want %d", n.ID, id)
// 	}
// 	if got := n.Meta.GetTitle(); got != "Test Node" {
// 		t.Fatalf("meta title = %q, want %q", got, "Test Node")
// 	}
//
// 	// 3) Update meta to add a tag via UpdateMeta and verify it was applied.
// 	err = k.UpdateMeta(ctx, id, func(m *keg.Meta) error {
// 		return m.AddTag("Example")
// 	})
// 	if err != nil {
// 		t.Fatalf("UpdateMeta failed: %v", err)
// 	}
//
// 	updatedMeta, err := k.ReadMeta(ctx, id)
// 	if err != nil {
// 		t.Fatalf("ReadMeta failed: %v", err)
// 	}
// 	found := slices.Contains(updatedMeta.Tags(), "example")
// 	if !found {
// 		t.Fatalf("expected tag %q present in meta.Tags(): %#v", "example", updatedMeta.Tags())
// 	}
//
// 	// 4) Request a non-existent node and assert not-found semantics.
// 	const missingID = keg.NodeID(99999)
// 	_, err = k.GetNode(ctx, missingID)
// 	if err == nil {
// 		t.Fatalf("expected error for missing node %d, got nil", missingID)
// 	}
// 	if !errors.Is(err, terrs.ErrNodeNotFound) {
// 		t.Fatalf("expected errors.Is(err, ErrNodeNotFound) true; got err=%v", err)
// 	}
// }
