package keg_test

import (
	"context"
	"embed"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/keg"
)

//go:embed data/**
var testdata embed.FS

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
	cfg, err := k.Config(ctx)
	if err != nil {
		t.Fatalf("read test keg config: %v", err)
	}
	if cfg.SchemaPolicy == nil {
		cfg.SchemaPolicy = &keg.SchemaPolicy{}
	}
	cfg.SchemaPolicy.Strict = false
	if err := k.SetConfig(ctx, []byte(cfg.String())); err != nil {
		t.Fatalf("disable strict test policy: %v", err)
	}
}
