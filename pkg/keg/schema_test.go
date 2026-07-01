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

func TestSnapshotReplayPersistsOmegaFromRelationMaturity(t *testing.T) {
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
    maturity:
      - direction: links
        attribute: status
        weight: 2
        enum:
          draft: 0.25
          ready: 1
  - name: review
    type: evidence
    maturity:
      - direction: backlinks
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
	if _, err := k.AppendSnapshot(ctx, evidenceID, "evidence ready"); err != nil {
		t.Fatalf("AppendSnapshot evidence: %v", err)
	}
	if err := k.UpdateMeta(ctx, evidenceID, func(meta *kegpkg.NodeMeta) {
		_ = meta.Set(ctx, "status", "draft")
	}); err != nil {
		t.Fatalf("unsnapshotted evidence edit: %v", err)
	}
	sourceID, err := k.Create(ctx, &kegpkg.CreateOptions{
		Body:  []byte("# Source\n\n[Evidence](../" + evidenceID.Path() + ")\n"),
		Attrs: map[string]any{"type": "note"},
	})
	if err != nil {
		t.Fatalf("Create source: %v", err)
	}
	sourceSnap, err := k.AppendSnapshot(ctx, sourceID, "source")
	if err != nil {
		t.Fatalf("AppendSnapshot source: %v", err)
	}
	_, _, _, snapStats, err := k.GetSnapshot(ctx, sourceID, sourceSnap.ID, kegpkg.SnapshotReadOptions{ResolveContent: true})
	if err != nil {
		t.Fatalf("GetSnapshot source: %v", err)
	}
	snapshotOmega, ok := snapStats.Omega()
	if !ok {
		t.Fatalf("snapshot stats did not store omega")
	}
	if math.Abs(snapshotOmega-(2.0/3.0)) > 0.000001 {
		t.Fatalf("snapshot omega = %v, want %v", snapshotOmega, 2.0/3.0)
	}

	reviewID, err := k.Create(ctx, &kegpkg.CreateOptions{
		Body:  []byte("# Review\n\n[Source](../" + sourceID.Path() + ")\n"),
		Attrs: map[string]any{"type": "evidence", "certainty": 0.5},
	})
	if err != nil {
		t.Fatalf("Create backlink evidence: %v", err)
	}
	if _, err := k.AppendSnapshot(ctx, reviewID, "review"); err != nil {
		t.Fatalf("AppendSnapshot review: %v", err)
	}

	stats, err := k.GetStats(ctx, sourceID)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	omega, ok := stats.Omega()
	if !ok {
		t.Fatalf("GetStats did not read persisted omega")
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

func TestSnapshotReplayPersistsOmegaFromNestedMetadataMaturity(t *testing.T) {
	f := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	ctx := context.Background()
	k := kegpkg.NewLocalKeg(kegpkg.NewMemoryRepo(f.Runtime()), f.Runtime())
	if err := k.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	schema := []byte(`type: note
meta:
  type: object
  required: ["type"]
  properties:
    type:
      const: note
    status:
      type: string
      enum: [draft, review, ready]
      maturity:
        - weight: 1
          enum:
            draft: 0.25
            ready: 1
        - weight: 3
          enum:
            draft: 0
            review: 0.5
            ready: 1
markdown:
  requireTitle: true
`)
	if err := k.WriteSchema(ctx, "note", schema); err != nil {
		t.Fatalf("WriteSchema note: %v", err)
	}
	id, err := k.Create(ctx, &kegpkg.CreateOptions{
		Title: "Own Metadata",
		Attrs: map[string]any{"type": "note", "status": "review"},
	})
	if err != nil {
		t.Fatalf("Create note: %v", err)
	}
	snap, err := k.AppendSnapshot(ctx, id, "own metadata")
	if err != nil {
		t.Fatalf("AppendSnapshot note: %v", err)
	}
	_, _, _, snapStats, err := k.GetSnapshot(ctx, id, snap.ID, kegpkg.SnapshotReadOptions{ResolveContent: true})
	if err != nil {
		t.Fatalf("GetSnapshot note: %v", err)
	}
	snapshotOmega, ok := snapStats.Omega()
	if !ok {
		t.Fatalf("snapshot stats did not store omega")
	}
	wantOmega := 0.375
	if math.Abs(snapshotOmega-wantOmega) > 0.000001 {
		t.Fatalf("snapshot omega = %v, want %v", snapshotOmega, wantOmega)
	}
	stats, err := k.GetStats(ctx, id)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	omega, ok := stats.Omega()
	if !ok {
		t.Fatalf("GetStats did not read persisted omega")
	}
	if math.Abs(omega-wantOmega) > 0.000001 {
		t.Fatalf("omega = %v, want %v", omega, wantOmega)
	}
}

func TestSnapshotReplayPersistsOmegaFromLegacyTopLevelMetadataMaturity(t *testing.T) {
	f := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	ctx := context.Background()
	k := kegpkg.NewLocalKeg(kegpkg.NewMemoryRepo(f.Runtime()), f.Runtime())
	if err := k.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	schema := []byte(`type: note
maturity:
  - attribute: status
    weight: 1
    enum:
      draft: 0.25
      ready: 1
markdown:
  requireTitle: true
`)
	if err := k.WriteSchema(ctx, "note", schema); err != nil {
		t.Fatalf("WriteSchema note: %v", err)
	}
	id, err := k.Create(ctx, &kegpkg.CreateOptions{
		Title: "Legacy Metadata",
		Attrs: map[string]any{"type": "note", "status": "ready"},
	})
	if err != nil {
		t.Fatalf("Create note: %v", err)
	}
	if _, err := k.AppendSnapshot(ctx, id, "legacy own metadata"); err != nil {
		t.Fatalf("AppendSnapshot note: %v", err)
	}
	stats, err := k.GetStats(ctx, id)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	omega, ok := stats.Omega()
	if !ok {
		t.Fatalf("GetStats did not read persisted omega")
	}
	if math.Abs(omega-1) > 0.000001 {
		t.Fatalf("omega = %v, want 1", omega)
	}
}

func TestSnapshotReplayCombinesMetadataAndRelationMaturity(t *testing.T) {
	f := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	ctx := context.Background()
	k := kegpkg.NewLocalKeg(kegpkg.NewMemoryRepo(f.Runtime()), f.Runtime())
	if err := k.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	evidenceSchema := []byte(`type: evidence
markdown:
  requireTitle: true
`)
	if err := k.WriteSchema(ctx, "evidence", evidenceSchema); err != nil {
		t.Fatalf("WriteSchema evidence: %v", err)
	}
	noteSchema := []byte(`type: note
meta:
  type: object
  properties:
    status:
      type: string
      enum: [draft, ready]
      maturity:
        - weight: 1
          enum:
            draft: 0.25
            ready: 1
relations:
  - name: support
    type: evidence
    maturity:
      - attribute: confidence
        weight: 3
markdown:
  requireTitle: true
`)
	if err := k.WriteSchema(ctx, "note", noteSchema); err != nil {
		t.Fatalf("WriteSchema note: %v", err)
	}

	evidenceID, err := k.Create(ctx, &kegpkg.CreateOptions{
		Title: "Evidence",
		Attrs: map[string]any{"type": "evidence", "confidence": 1},
	})
	if err != nil {
		t.Fatalf("Create evidence: %v", err)
	}
	if _, err := k.AppendSnapshot(ctx, evidenceID, "evidence"); err != nil {
		t.Fatalf("AppendSnapshot evidence: %v", err)
	}
	noteID, err := k.Create(ctx, &kegpkg.CreateOptions{
		Body:  []byte("# Source\n\n[Evidence](../" + evidenceID.Path() + ")\n"),
		Attrs: map[string]any{"type": "note", "status": "draft"},
	})
	if err != nil {
		t.Fatalf("Create note: %v", err)
	}
	if _, err := k.AppendSnapshot(ctx, noteID, "note"); err != nil {
		t.Fatalf("AppendSnapshot note: %v", err)
	}

	stats, err := k.GetStats(ctx, noteID)
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	omega, ok := stats.Omega()
	if !ok {
		t.Fatalf("GetStats did not read persisted omega")
	}
	if math.Abs(omega-0.8125) > 0.000001 {
		t.Fatalf("omega = %v, want 0.8125", omega)
	}
}

func TestSchemaRelationMaturityValidation(t *testing.T) {
	f := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	ctx := context.Background()
	k := kegpkg.NewLocalKeg(kegpkg.NewMemoryRepo(f.Runtime()), f.Runtime())
	if err := k.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	valid := []byte(`type: note
relations:
  - name: support
    type: evidence
    description: Evidence that supports the note.
    required: true
    maturity:
      - direction: links
        attribute: status
        weight: 1
        enum:
          draft: 0
          ready: 1
markdown:
  requireTitle: true
`)
	if err := k.WriteSchema(ctx, "note", valid); err != nil {
		t.Fatalf("WriteSchema valid: %v", err)
	}
	parsed, err := kegpkg.ParseSchemaDefinition(valid)
	if err != nil {
		t.Fatalf("ParseSchemaDefinition: %v", err)
	}
	if got := parsed.Relations[0].Description; got != "Evidence that supports the note." {
		t.Fatalf("relation description = %q", got)
	}
	if len(parsed.Relations[0].Maturity) != 1 {
		t.Fatalf("maturity len = %d, want 1", len(parsed.Relations[0].Maturity))
	}

	oldShape := []byte(`type: note
relations:
  - name: support
    type: evidence
    direction: links
    attribute: status
    weight: 1
markdown:
  requireTitle: true
`)
	if err := k.WriteSchema(ctx, "note", oldShape); !errors.Is(err, kegpkg.ErrInvalid) {
		t.Fatalf("WriteSchema old relation shape error = %v, want ErrInvalid", err)
	}

	missingAttribute := []byte(`type: note
relations:
  - name: support
    type: evidence
    maturity:
      - weight: 1
markdown:
  requireTitle: true
`)
	if err := k.WriteSchema(ctx, "note", missingAttribute); !errors.Is(err, kegpkg.ErrInvalid) {
		t.Fatalf("WriteSchema missing attribute error = %v, want ErrInvalid", err)
	}
}

func TestSchemaTopLevelMaturityValidation(t *testing.T) {
	f := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	ctx := context.Background()
	k := kegpkg.NewLocalKeg(kegpkg.NewMemoryRepo(f.Runtime()), f.Runtime())
	if err := k.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	valid := []byte(`type: note
maturity:
  - attribute: status
    weight: 1
    enum:
      draft: 0.25
      ready: 1
markdown:
  requireTitle: true
`)
	if err := k.WriteSchema(ctx, "note", valid); err != nil {
		t.Fatalf("WriteSchema valid: %v", err)
	}
	parsed, err := kegpkg.ParseSchemaDefinition(valid)
	if err != nil {
		t.Fatalf("ParseSchemaDefinition: %v", err)
	}
	if len(parsed.Maturity) != 1 {
		t.Fatalf("maturity len = %d, want 1", len(parsed.Maturity))
	}
	if got := parsed.Maturity[0].Enum["draft"]; got != 0.25 {
		t.Fatalf("draft score = %v, want 0.25", got)
	}

	tests := []struct {
		name string
		body string
	}{
		{
			name: "missing attribute",
			body: `type: note
maturity:
  - weight: 1
markdown:
  requireTitle: true
`,
		},
		{
			name: "missing weight",
			body: `type: note
maturity:
  - attribute: status
markdown:
  requireTitle: true
`,
		},
		{
			name: "invalid weight",
			body: `type: note
maturity:
  - attribute: status
    weight: -1
markdown:
  requireTitle: true
`,
		},
		{
			name: "enum without attribute",
			body: `type: note
maturity:
  - weight: 1
    enum:
      draft: 0.25
markdown:
  requireTitle: true
`,
		},
		{
			name: "unsupported direction",
			body: `type: note
maturity:
  - direction: links
    attribute: status
    weight: 1
markdown:
  requireTitle: true
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := k.WriteSchema(ctx, "note", []byte(tc.body)); !errors.Is(err, kegpkg.ErrInvalid) {
				t.Fatalf("WriteSchema error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestSchemaNestedMetadataMaturityValidation(t *testing.T) {
	f := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	ctx := context.Background()
	k := kegpkg.NewLocalKeg(kegpkg.NewMemoryRepo(f.Runtime()), f.Runtime())
	if err := k.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	valid := []byte(`type: note
meta:
  type: object
  properties:
    status:
      type: string
      enum: [draft, ready]
      maturity:
        - weight: 1
          enum:
            draft: 0.25
            ready: 1
markdown:
  requireTitle: true
`)
	if err := k.WriteSchema(ctx, "note", valid); err != nil {
		t.Fatalf("WriteSchema valid: %v", err)
	}
	parsed, err := kegpkg.ParseSchemaDefinition(valid)
	if err != nil {
		t.Fatalf("ParseSchemaDefinition: %v", err)
	}
	weights := parsed.MetadataMaturityWeights()
	if len(weights) != 1 {
		t.Fatalf("nested maturity len = %d, want 1", len(weights))
	}
	if weights[0].Attribute != "status" || weights[0].Weight != 1 || weights[0].Enum["draft"] != 0.25 {
		t.Fatalf("nested maturity fields = %+v", weights[0])
	}

	tests := []struct {
		name string
		body string
	}{
		{
			name: "attribute unsupported",
			body: `type: note
meta:
  type: object
  properties:
    status:
      type: string
      maturity:
        - attribute: status
          weight: 1
markdown:
  requireTitle: true
`,
		},
		{
			name: "direction unsupported",
			body: `type: note
meta:
  type: object
  properties:
    status:
      type: string
      maturity:
        - direction: links
          weight: 1
markdown:
  requireTitle: true
`,
		},
		{
			name: "missing weight",
			body: `type: note
meta:
  type: object
  properties:
    status:
      type: string
      maturity:
        - enum:
            ready: 1
markdown:
  requireTitle: true
`,
		},
		{
			name: "zero weight",
			body: `type: note
meta:
  type: object
  properties:
    status:
      type: string
      maturity:
        - weight: 0
markdown:
  requireTitle: true
`,
		},
		{
			name: "negative score",
			body: `type: note
meta:
  type: object
  properties:
    status:
      type: string
      maturity:
        - weight: 1
          enum:
            ready: -0.1
markdown:
  requireTitle: true
`,
		},
		{
			name: "score above one",
			body: `type: note
meta:
  type: object
  properties:
    status:
      type: string
      maturity:
        - weight: 1
          enum:
            ready: 1.1
markdown:
  requireTitle: true
`,
		},
		{
			name: "unsupported field",
			body: `type: note
meta:
  type: object
  properties:
    status:
      type: string
      maturity:
        - weight: 1
          label: ready
markdown:
  requireTitle: true
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := k.WriteSchema(ctx, "note", []byte(tc.body)); !errors.Is(err, kegpkg.ErrInvalid) {
				t.Fatalf("WriteSchema error = %v, want ErrInvalid", err)
			}
		})
	}
}
