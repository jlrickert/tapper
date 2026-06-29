package keg_test

import (
	"context"
	"errors"
	"math"
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

func TestGetStatsComputesOmegaFromRelationWeights(t *testing.T) {
	f := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	ctx := context.Background()
	k := kegpkg.NewLocalKeg(kegpkg.NewMemoryRepo(f.Runtime()), f.Runtime())
	if err := k.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	evidenceSchema := []byte(`type: evidence
meta:
  type: object
  required: ["type"]
  properties:
    type:
      const: evidence
    status:
      type: string
      enum: [draft, ready]
markdown:
  requireTitle: true
`)
	if err := k.WriteSchema(ctx, "evidence", evidenceSchema); err != nil {
		t.Fatalf("WriteSchema evidence: %v", err)
	}
	noteSchema := []byte(`type: note
meta:
  type: object
  required: ["type"]
  properties:
    type:
      const: note
relations:
  - name: support
    type: evidence
    direction: links
    attribute: status
    weight: 2
    enum:
      draft: 0.25
      ready: 1
  - name: review
    type: evidence
    direction: backlinks
    attribute: certainty
    weight: 1
markdown:
  requireTitle: true
`)
	if err := k.WriteSchema(ctx, "note", noteSchema); err != nil {
		t.Fatalf("WriteSchema note: %v", err)
	}

	evidenceID, err := k.Create(ctx, &kegpkg.CreateOptions{
		Title: "Evidence",
		Attrs: map[string]any{"type": "evidence", "status": "ready"},
	})
	if err != nil {
		t.Fatalf("Create evidence: %v", err)
	}
	sourceID, err := k.Create(ctx, &kegpkg.CreateOptions{
		Body:  []byte("# Source\n\n[Evidence](../" + evidenceID.Path() + ")\n"),
		Attrs: map[string]any{"type": "note"},
	})
	if err != nil {
		t.Fatalf("Create source: %v", err)
	}
	_, err = k.Create(ctx, &kegpkg.CreateOptions{
		Body:  []byte("# Review\n\n[Source](../" + sourceID.Path() + ")\n"),
		Attrs: map[string]any{"type": "evidence", "certainty": 0.5},
	})
	if err != nil {
		t.Fatalf("Create backlink evidence: %v", err)
	}

	stats, err := k.GetStats(ctx, sourceID)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	omega, ok := stats.Omega()
	if !ok {
		t.Fatalf("GetStats did not compute omega")
	}
	if math.Abs(omega-(2.5/3)) > 0.000001 {
		t.Fatalf("omega = %v, want %v", omega, 2.5/3)
	}

	view, err := k.ReadNode(ctx, sourceID)
	if err != nil {
		t.Fatalf("ReadNode: %v", err)
	}
	readOmega, ok := view.Stats.Omega()
	if !ok || math.Abs(readOmega-omega) > 0.000001 {
		t.Fatalf("ReadNode omega = %v, %v; want %v, true", readOmega, ok, omega)
	}

	matches, err := k.Query(ctx, kegpkg.QueryOptions{Expr: ".omega>=0.8"})
	if err != nil {
		t.Fatalf("Query .omega: %v", err)
	}
	found := false
	for _, match := range matches {
		if match.ID == sourceID.Path() {
			found = true
		}
	}
	if !found {
		t.Fatalf(".omega query did not match source node; matches=%+v", matches)
	}
}
