package keg_test

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	kegpkg "github.com/jlrickert/tapper/pkg/keg"
)

func TestCreateSchemaConcurrentExactlyOneWinner(t *testing.T) {
	for _, tc := range []struct {
		name string
		repo func(*sandbox.Sandbox) kegpkg.Repository
	}{
		{name: "memory", repo: func(f *sandbox.Sandbox) kegpkg.Repository { return kegpkg.NewMemoryRepo(f.Runtime()) }},
		{name: "filesystem", repo: func(f *sandbox.Sandbox) kegpkg.Repository {
			return kegpkg.NewFsRepo("~/schema-concurrent", f.Runtime())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
			k := kegpkg.NewLocalKeg(tc.repo(f), f.Runtime())
			var successes atomic.Int32
			errCh := make(chan error, 16)
			var wg sync.WaitGroup
			for i := 0; i < 16; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					err := k.CreateSchema(context.Background(), "task", []byte("type: task\n"))
					switch {
					case err == nil:
						successes.Add(1)
					case errors.Is(err, kegpkg.ErrExist):
					default:
						errCh <- err
					}
				}()
			}
			wg.Wait()
			close(errCh)
			for err := range errCh {
				t.Errorf("CreateSchema: %v", err)
			}
			if got := successes.Load(); got != 1 {
				t.Fatalf("successful creators = %d, want 1", got)
			}
		})
	}
}

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
	result, err := k.ValidateNode(ctx, id.ID)
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
	result, err = k.ValidateNode(ctx, id.ID)
	if err != nil {
		t.Fatalf("ValidateNode typed: %v", err)
	}
	if !result.Valid || result.Type != "task" {
		t.Fatalf("typed node result=%#v, want valid task", result)
	}
}

func TestSchemaValidationActorOverrides(t *testing.T) {
	f := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	ctx := context.Background()
	k := kegpkg.NewLocalKeg(kegpkg.NewMemoryRepo(f.Runtime()), f.Runtime())
	if err := k.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := k.WriteSchema(ctx, "task", []byte(`type: task
meta:
  type: object
  required: ["type"]
  properties:
    type:
      const: task
markdown:
  requireTitle: true
`)); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	if err := k.UpdateConfig(ctx, func(cfg *kegpkg.Config) {
		cfg.SchemaPolicy = &kegpkg.SchemaPolicy{
			Human: kegpkg.ValidationModeBlock,
			Agent: kegpkg.ValidationModeOff,
			API:   kegpkg.ValidationModeWarn,
		}
	}); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	invalid := &kegpkg.CreateOptions{Body: []byte("# Missing Type\n")}
	_, err := k.Create(kegpkg.WithValidationActor(ctx, kegpkg.ValidationActorHuman), invalid)
	if !errors.Is(err, kegpkg.ErrSchemaInvalid) {
		t.Fatalf("human override error = %v, want ErrSchemaInvalid", err)
	}
	if _, err := k.Create(kegpkg.WithValidationActor(ctx, kegpkg.ValidationActorAgent), invalid); err != nil {
		t.Fatalf("agent off override blocked: %v", err)
	}
	if _, err := k.Create(kegpkg.WithValidationActor(ctx, kegpkg.ValidationActorAPI), invalid); err != nil {
		t.Fatalf("API warn override blocked: %v", err)
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
	if _, err := k.AppendSnapshot(ctx, evidenceID.ID, "evidence ready"); err != nil {
		t.Fatalf("AppendSnapshot evidence: %v", err)
	}
	if err := k.UpdateMeta(ctx, evidenceID.ID, func(meta *kegpkg.NodeMeta) {
		_ = meta.Set(ctx, "status", "draft")
	}); err != nil {
		t.Fatalf("unsnapshotted evidence edit: %v", err)
	}
	sourceID, err := k.Create(ctx, &kegpkg.CreateOptions{
		Body:  []byte("# Source\n\n[Evidence](../" + evidenceID.ID.Path() + ")\n"),
		Attrs: map[string]any{"type": "note"},
	})
	if err != nil {
		t.Fatalf("Create source: %v", err)
	}
	sourceSnap, err := k.AppendSnapshot(ctx, sourceID.ID, "source")
	if err != nil {
		t.Fatalf("AppendSnapshot source: %v", err)
	}
	_, _, _, snapStats, err := k.GetSnapshot(ctx, sourceID.ID, sourceSnap.ID, kegpkg.SnapshotReadOptions{ResolveContent: true})
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
		Body:  []byte("# Review\n\n[Source](../" + sourceID.ID.Path() + ")\n"),
		Attrs: map[string]any{"type": "evidence", "certainty": 0.5},
	})
	if err != nil {
		t.Fatalf("Create backlink evidence: %v", err)
	}
	if _, err := k.AppendSnapshot(ctx, reviewID.ID, "review"); err != nil {
		t.Fatalf("AppendSnapshot review: %v", err)
	}

	stats, err := k.GetStats(ctx, sourceID.ID)
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

	view, err := k.ReadNode(ctx, sourceID.ID)
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
		if match.ID == sourceID.ID.Path() {
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
	snap, err := k.AppendSnapshot(ctx, id.ID, "own metadata")
	if err != nil {
		t.Fatalf("AppendSnapshot note: %v", err)
	}
	_, _, _, snapStats, err := k.GetSnapshot(ctx, id.ID, snap.ID, kegpkg.SnapshotReadOptions{ResolveContent: true})
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
	stats, err := k.GetStats(ctx, id.ID)
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
	if _, err := k.AppendSnapshot(ctx, id.ID, "legacy own metadata"); err != nil {
		t.Fatalf("AppendSnapshot note: %v", err)
	}
	stats, err := k.GetStats(ctx, id.ID)
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
	if _, err := k.AppendSnapshot(ctx, evidenceID.ID, "evidence"); err != nil {
		t.Fatalf("AppendSnapshot evidence: %v", err)
	}
	noteID, err := k.Create(ctx, &kegpkg.CreateOptions{
		Body:  []byte("# Source\n\n[Evidence](../" + evidenceID.ID.Path() + ")\n"),
		Attrs: map[string]any{"type": "note", "status": "draft"},
	})
	if err != nil {
		t.Fatalf("Create note: %v", err)
	}
	if _, err := k.AppendSnapshot(ctx, noteID.ID, "note"); err != nil {
		t.Fatalf("AppendSnapshot note: %v", err)
	}

	stats, err := k.GetStats(ctx, noteID.ID)
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

// TestZeroNodeExemptFromRequiredType covers the placeholder landing node. A keg
// with any schema requires meta.type on every node, but node 0 is created by
// Init with empty meta and can never satisfy that. Reporting it left a standing
// schema error on every schema-bearing keg — and the only fix the message
// suggests is to give node 0 a type and content, destroying the placeholder.
func TestZeroNodeExemptFromRequiredType(t *testing.T) {
	f := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	ctx := context.Background()
	k := kegpkg.NewLocalKeg(kegpkg.NewMemoryRepo(f.Runtime()), f.Runtime())
	if err := k.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	schema := []byte("type: task\nmeta:\n  type: object\n  required: [\"type\"]\n")
	if err := k.WriteSchema(ctx, "task", schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}

	zero := kegpkg.NodeId{ID: 0}
	result, err := k.ValidateNode(ctx, zero)
	if err != nil {
		t.Fatalf("ValidateNode zero: %v", err)
	}
	if !result.Valid || len(result.Issues) != 0 {
		t.Fatalf("untyped node 0 must validate clean; result=%#v", result)
	}

	// The exemption is one rule on one node, not a hole. A node 0 that declares
	// a type is still held to it, on read...
	result, err = k.ValidateNodePayload(ctx, kegpkg.NodeValidationPayload{
		ID: zero, Meta: []byte("type: nonexistent\n"), HasMeta: true,
	})
	if err != nil {
		t.Fatalf("ValidateNodePayload typed zero: %v", err)
	}
	if result.Valid {
		t.Fatalf("node 0 declaring an unknown type must still fail; result=%#v", result)
	}

	// ...and on write, where enforcement rejects it outright.
	if err := k.SetMeta(ctx, zero, metaWithType(t, ctx, k, zero, "nonexistent")); !errors.Is(err, kegpkg.ErrSchemaInvalid) {
		t.Fatalf("SetMeta with unknown type on node 0 = %v, want ErrSchemaInvalid", err)
	}

	// And an ordinary node with no type still fails, so the exemption did not
	// silently disable the rule for everyone.
	id, err := k.Create(kegpkg.WithValidationActor(ctx, kegpkg.ValidationActorHuman),
		&kegpkg.CreateOptions{Body: []byte("# Untyped\n")})
	if err != nil {
		t.Fatalf("Create untyped: %v", err)
	}
	result, err = k.ValidateNode(ctx, id.ID)
	if err != nil {
		t.Fatalf("ValidateNode untyped: %v", err)
	}
	if result.Valid {
		t.Fatalf("untyped non-zero node must still fail; result=%#v", result)
	}
}

func metaWithType(t *testing.T, ctx context.Context, k *kegpkg.LocalKeg, id kegpkg.NodeId, typeName string) *kegpkg.NodeMeta {
	t.Helper()
	meta, err := k.GetMeta(ctx, id)
	if err != nil {
		t.Fatalf("GetMeta: %v", err)
	}
	if err := meta.Set(ctx, "type", typeName); err != nil {
		t.Fatalf("meta.Set: %v", err)
	}
	return meta
}

// TestZeroNodeDoctorReportsRealProblemsOnly pins the other half: doctor stops
// reporting the schema error but keeps every other check live on node 0.
func TestZeroNodeDoctorReportsRealProblemsOnly(t *testing.T) {
	f := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	ctx := context.Background()
	k := kegpkg.NewLocalKeg(kegpkg.NewMemoryRepo(f.Runtime()), f.Runtime())
	if err := k.Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := k.WriteSchema(ctx, "task", []byte("type: task\nmeta:\n  type: object\n")); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}

	issues, err := k.Doctor(ctx)
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	for _, issue := range issues {
		if issue.NodeID == "0" && issue.Kind == "schema" {
			t.Fatalf("doctor still reports a schema issue on node 0: %+v", issue)
		}
	}

	// A genuine node-0 defect must still surface.
	if err := k.SetContent(ctx, kegpkg.NodeId{ID: 0}, []byte("# Zero\n\nLead.\n\n[gone](../4242)\n")); err != nil {
		t.Fatalf("SetContent zero: %v", err)
	}
	issues, err = k.Doctor(ctx)
	if err != nil {
		t.Fatalf("Doctor after edit: %v", err)
	}
	var sawBrokenLink bool
	for _, issue := range issues {
		if issue.NodeID == "0" && issue.Kind == "broken-link" {
			sawBrokenLink = true
		}
	}
	if !sawBrokenLink {
		t.Fatalf("doctor must still check node 0 for real defects; issues=%+v", issues)
	}
}
