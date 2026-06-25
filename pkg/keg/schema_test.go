package keg_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	kegpkg "github.com/jlrickert/tapper/pkg/keg"
)

func TestSchemaValidationCreatePolicy(t *testing.T) {
	f := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	ctx := context.Background()
	k := kegpkg.NewLocalKeg(kegpkg.NewMemoryRepo(f.Runtime()), f.Runtime())
	if err := k.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	schema := []byte(`type: task
meta:
  type: object
  required: ["type"]
  properties:
    type:
      const: task
markdown:
  requireTitle: true
  sections:
    - heading: Context
      level: 2
      required: true
`)
	if err := k.WriteSchema(ctx, "task", schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}

	_, err := k.Create(ctx, &kegpkg.CreateOptions{Body: []byte("# Missing Type\n\n## Context\n")})
	if !errors.Is(err, kegpkg.ErrSchemaInvalid) {
		t.Fatalf("Create missing type error = %v, want ErrSchemaInvalid", err)
	}

	humanCtx := kegpkg.WithValidationActor(ctx, kegpkg.ValidationActorHuman)
	id, err := k.Create(humanCtx, &kegpkg.CreateOptions{Body: []byte("# Warn Only\n\n## Context\n")})
	if err != nil {
		t.Fatalf("human Create should warn, not block: %v", err)
	}
	result, err := k.ValidateNode(ctx, id)
	if err != nil {
		t.Fatalf("ValidateNode: %v", err)
	}
	if result.Valid {
		t.Fatalf("human-created invalid node validated true; result=%#v", result)
	}

	id, err = k.Create(ctx, &kegpkg.CreateOptions{Body: []byte("---\ntype: task\n---\n# Typed\n\n## Context\n")})
	if err != nil {
		t.Fatalf("typed Create: %v", err)
	}
	result, err = k.ValidateNode(ctx, id)
	if err != nil {
		t.Fatalf("ValidateNode typed: %v", err)
	}
	if !result.Valid || result.Type != "task" {
		t.Fatalf("typed node result=%#v, want valid task", result)
	}
}
