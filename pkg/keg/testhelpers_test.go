package keg_test

import (
	"context"
	"embed"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/internal/testkegrepo"
	"github.com/jlrickert/tapper/pkg/keg"
)

//go:embed data/**
var testdata embed.FS

func newTestMemoryRepo(rt *toolkit.Runtime) *testkegrepo.MemoryRepository {
	return testkegrepo.NewMemoryRepository(rt)
}

func memoryTarget(_ string, opts ...keg.TargetOption) keg.Target {
	target := keg.Target{}
	for _, apply := range opts {
		apply(&target)
	}
	return target
}

func newMemoryKegFromTarget(_ context.Context, target keg.Target, rt *toolkit.Runtime, _ ...keg.KegOption) (keg.Keg, error) {
	k := keg.NewLocalKeg(newTestMemoryRepo(rt), rt)
	k.SetTarget(&target)
	return k, nil
}

func NewSandbox(t *testing.T, opts ...sandbox.Option) *sandbox.Sandbox {
	return sandbox.NewSandbox(t, &sandbox.Options{
		Data: testdata,
		Home: "/home/testuser",
		User: "testuser",
	}, opts...)
}

// initNonStrictTestKeg preserves the legacy permissive fixture assumed by
// tests unrelated to schema enforcement. Production Init defaults to strict;
// strict behavior has dedicated tests in keg_batch_test.go.
func initNonStrictTestKeg(t *testing.T, k keg.Keg, ctx context.Context) {
	t.Helper()
	if err := k.Init(ctx); err != nil {
		t.Fatalf("init test keg: %v", err)
	}
	cfg, err := k.Settings(ctx)
	if err != nil {
		t.Fatalf("read test keg settings: %v", err)
	}
	if cfg.SchemaPolicy == nil {
		cfg.SchemaPolicy = &keg.SchemaPolicy{}
	}
	cfg.SchemaPolicy.Strict = false
	if err := k.SetSettings(ctx, []byte(cfg.String()), keg.SettingsWriteOptions{ExpectedHash: cfg.Hash()}); err != nil {
		t.Fatalf("disable strict test policy: %v", err)
	}
}

func removeOptions(t *testing.T, ctx context.Context, k keg.Keg, id keg.NodeId) keg.NodeRemoveOptions {
	t.Helper()
	view, err := k.ReadNode(ctx, id)
	if err != nil {
		t.Fatalf("read node %s before remove: %v", id.Path(), err)
	}
	return keg.NodeRemoveOptions{ID: id, ExpectedHash: view.Hash()}
}

func moveOptions(t *testing.T, ctx context.Context, k keg.Keg, src, dst keg.NodeId) keg.NodeMoveOptions {
	t.Helper()
	view, err := k.ReadNode(ctx, src)
	if err != nil {
		t.Fatalf("read node %s before move: %v", src.Path(), err)
	}
	return keg.NodeMoveOptions{Source: src, Destination: dst, ExpectedHash: view.Hash()}
}
