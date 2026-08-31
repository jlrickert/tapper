package keg_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

const selectionSchemaTask = `type: task
meta:
  type: object
  required: [type]
  properties:
    type: {const: task}
markdown:
  requireTitle: true
`

const selectionSchemaNote = `type: note
meta:
  type: object
  required: [type]
  properties:
    type: {const: note}
markdown:
  requireTitle: true
`

func newSchemaSelectionKeg(t *testing.T) (*keg.LocalKeg, context.Context) {
	t.Helper()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, k.Init(ctx))
	require.NoError(t, k.CreateSchema(ctx, "task", []byte(selectionSchemaTask)))
	require.NoError(t, k.CreateSchema(ctx, "note", []byte(selectionSchemaNote)))
	return k, ctx
}

func TestExplicitSchemaSelectionStrictModeMatrix(t *testing.T) {
	actors := []keg.ValidationActor{keg.ValidationActorHuman, keg.ValidationActorAgent, keg.ValidationActorAPI}
	modes := []keg.ValidationMode{keg.ValidationModeBlock, keg.ValidationModeWarn, keg.ValidationModeOff}
	for _, strict := range []bool{false, true} {
		for _, actor := range actors {
			for _, mode := range modes {
				name := string(actor) + "/" + string(mode)
				if strict {
					name = "strict/" + name
				} else {
					name = "non-strict/" + name
				}
				t.Run(name, func(t *testing.T) {
					k, ctx := newSchemaSelectionKeg(t)
					require.NoError(t, k.UpdateSettings(ctx, func(cfg *keg.Settings) {
						cfg.SchemaPolicy.Strict = strict
						cfg.SchemaPolicy.Human = mode
						cfg.SchemaPolicy.Agent = mode
						cfg.SchemaPolicy.API = mode
					}))
					_, err := k.Create(keg.WithValidationActor(ctx, actor), &keg.CreateOptions{Body: []byte("# Untyped\n")})
					if strict && mode == keg.ValidationModeBlock {
						require.ErrorIs(t, err, keg.ErrSchemaInvalid)
						require.Contains(t, err.Error(), "schema: explicit schema selection is required")
					} else {
						require.NoError(t, err)
					}
				})
			}
		}
	}
}

func TestExplicitSchemaSelectionPersistsReplacesAndRejectsConflicts(t *testing.T) {
	k, ctx := newSchemaSelectionKeg(t)
	created, err := k.Create(ctx, &keg.CreateOptions{Schema: " task ", Body: []byte("# Selected\n")})
	require.NoError(t, err)
	meta, err := k.GetMeta(ctx, created.ID)
	require.NoError(t, err)
	typeName, ok := meta.Get("type")
	require.True(t, ok)
	require.Equal(t, "task", typeName)

	view, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	_, err = k.UpdateNode(ctx, keg.NodeUpdateOptions{ID: created.ID, Schema: "note", Content: []byte("# Reclassified\n"), ExpectedHash: view.Hash()})
	require.NoError(t, err)
	meta, err = k.GetMeta(ctx, created.ID)
	require.NoError(t, err)
	typeName, _ = meta.Get("type")
	require.Equal(t, "note", typeName)

	view, err = k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: created.ID, Schema: "note", Meta: []byte("type: task\n"), HasMeta: true, ExpectedHash: view.Hash(),
	}})
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
	require.Contains(t, err.Error(), "selected schema \"note\" conflicts with metadata type \"task\"")
	meta, err = k.GetMeta(ctx, created.ID)
	require.NoError(t, err)
	typeName, _ = meta.Get("type")
	require.Equal(t, "note", typeName)

	_, err = k.Create(ctx, &keg.CreateOptions{
		Schema: "task", Attrs: map[string]any{"type": "task"},
		Body: []byte("---\ntype: note\n---\n# Conflict\n"),
	})
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
	require.Contains(t, err.Error(), "conflicts with attributes type")

	_, err = k.Create(ctx, &keg.CreateOptions{Schema: "  ", Body: []byte("# Whitespace\n")})
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
	_, err = k.Create(ctx, &keg.CreateOptions{Schema: "missing", Body: []byte("# Unknown\n")})
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
	require.Contains(t, err.Error(), "schema: unknown schema \"missing\"")
}

func TestSchemaSelectionBatchFailureIsAtomic(t *testing.T) {
	k, ctx := newSchemaSelectionKeg(t)
	created, err := k.CreateNodes(ctx, []keg.NodeCreate{
		{Key: "one", Schema: "task", Body: []byte("# One\n")},
		{Key: "two", Schema: "task", Body: []byte("# Two\n")},
	})
	require.NoError(t, err)
	before, err := k.DexArtifacts(ctx)
	require.NoError(t, err)
	one, err := k.ReadNode(ctx, created[0].ID)
	require.NoError(t, err)
	two, err := k.ReadNode(ctx, created[1].ID)
	require.NoError(t, err)
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{
		{ID: created[0].ID, Schema: "task", Content: []byte("# Changed\n"), HasContent: true, SnapshotBefore: true, ExpectedHash: one.Hash()},
		{ID: created[1].ID, Content: []byte("# Missing selection\n"), HasContent: true, SnapshotBefore: true, ExpectedHash: two.Hash()},
	})
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
	var batchErr *keg.BatchMutationError
	require.True(t, errors.As(err, &batchErr))
	require.Equal(t, 1, batchErr.Index)
	content, err := k.GetContent(ctx, created[0].ID)
	require.NoError(t, err)
	require.Equal(t, "# One\n", string(content))
	history, err := k.ListSnapshots(ctx, created[0].ID)
	require.NoError(t, err)
	require.Empty(t, history)
	after, err := k.DexArtifacts(ctx)
	require.NoError(t, err)
	require.Equal(t, before.Indexes, after.Indexes)
}

func TestStrictSchemaSelectionExemptsMoveAndRemove(t *testing.T) {
	k, ctx := newSchemaSelectionKeg(t)
	require.NoError(t, k.UpdateSettings(ctx, func(cfg *keg.Settings) {
		cfg.SchemaPolicy = &keg.SchemaPolicy{Strict: true, Human: keg.ValidationModeBlock}
	}))
	created, err := k.CreateNodes(ctx, []keg.NodeCreate{
		{Key: "source", Schema: "note", Body: []byte("# Source\n\n[Target](../{{node:target}})\n")},
		{Key: "target", Schema: "note", Body: []byte("# Target\n")},
	})
	require.NoError(t, err)

	moved := keg.NodeId{ID: 20}
	_, err = k.Move(ctx, moveOptions(t, ctx, k, created[1].ID, moved))
	require.NoError(t, err)
	content, err := k.GetContent(ctx, created[0].ID)
	require.NoError(t, err)
	require.Contains(t, string(content), "../20")

	_, err = k.Remove(ctx, removeOptions(t, ctx, k, moved))
	require.NoError(t, err)
}

func TestStrictSchemaSelectionRejectsLegacyMetadataMutation(t *testing.T) {
	k, ctx := newSchemaSelectionKeg(t)
	created, err := k.Create(ctx, &keg.CreateOptions{Schema: "note", Body: []byte("# Typed\n")})
	require.NoError(t, err)
	require.NoError(t, k.UpdateSettings(ctx, func(cfg *keg.Settings) {
		cfg.SchemaPolicy = &keg.SchemaPolicy{Strict: true, Human: keg.ValidationModeBlock}
	}))

	err = k.UpdateMeta(ctx, created.ID, func(meta *keg.NodeMeta) { meta.SetTags([]string{"blocked"}) })
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
	require.Contains(t, err.Error(), "schema: explicit schema selection is required")
	meta, err := k.GetMeta(ctx, created.ID)
	require.NoError(t, err)
	require.Empty(t, meta.Tags())
}

func TestSchemaAwareDirectWritesPersistReplaceAndMutateAtomically(t *testing.T) {
	k, ctx := newSchemaSelectionKeg(t)
	humanCtx := keg.WithValidationActor(ctx, keg.ValidationActorHuman)
	require.NoError(t, k.UpdateSettings(ctx, func(cfg *keg.Settings) {
		cfg.SchemaPolicy = &keg.SchemaPolicy{Strict: true, Human: keg.ValidationModeBlock}
	}))
	created, err := k.Create(humanCtx, &keg.CreateOptions{Schema: "note", Body: []byte("# Typed\n")})
	require.NoError(t, err)

	err = k.SetContent(humanCtx, created.ID, []byte("# Missing selection\n"))
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
	content, readErr := k.GetContent(ctx, created.ID)
	require.NoError(t, readErr)
	require.Equal(t, "# Typed\n", string(content))

	require.NoError(t, k.SetContentWithOptions(humanCtx, created.ID, []byte("# Reclassified\n"), keg.NodeWriteOptions{Schema: "task"}))
	meta, err := k.GetMeta(ctx, created.ID)
	require.NoError(t, err)
	typeName, _ := meta.Get("type")
	require.Equal(t, "task", typeName)

	err = k.SetContentWithOptions(humanCtx, created.ID, []byte("---\ntype: note\n---\n# Conflict\n"), keg.NodeWriteOptions{Schema: "task"})
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
	require.Contains(t, err.Error(), "conflicts with frontmatter type")

	conflicting, err := keg.ParseMeta(ctx, []byte("type: task\nowner: alice\n"))
	require.NoError(t, err)
	err = k.SetMetaWithOptions(humanCtx, created.ID, conflicting, keg.NodeWriteOptions{Schema: "note"})
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)

	base, err := keg.ParseMeta(ctx, []byte("type: task\nowner: alice\n"))
	require.NoError(t, err)
	require.NoError(t, k.SetMetaWithOptions(humanCtx, created.ID, base, keg.NodeWriteOptions{Schema: "task"}))
	require.NoError(t, k.UpdateMetaWithOptions(humanCtx, created.ID, func(m *keg.NodeMeta) {
		m.SetTags([]string{"one", "two"})
	}, keg.NodeWriteOptions{Schema: "note"}))
	meta, err = k.GetMeta(ctx, created.ID)
	require.NoError(t, err)
	typeName, _ = meta.Get("type")
	owner, _ := meta.Get("owner")
	require.Equal(t, "note", typeName)
	require.Equal(t, "alice", owner)
	require.Equal(t, []string{"one", "two"}, meta.Tags())

	err = k.UpdateMetaWithOptions(humanCtx, created.ID, func(m *keg.NodeMeta) {
		require.NoError(t, m.Set(ctx, "type", "task"))
	}, keg.NodeWriteOptions{Schema: "note"})
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
}

func TestValidateNodePayloadProjectsSchemaWithoutPersistence(t *testing.T) {
	k, ctx := newSchemaSelectionKeg(t)
	humanCtx := keg.WithValidationActor(ctx, keg.ValidationActorHuman)
	require.NoError(t, k.UpdateSettings(ctx, func(cfg *keg.Settings) {
		cfg.SchemaPolicy = &keg.SchemaPolicy{Strict: true, Human: keg.ValidationModeBlock}
	}))
	created, err := k.Create(humanCtx, &keg.CreateOptions{Schema: "note", Body: []byte("# Stored\n")})
	require.NoError(t, err)

	result, err := k.ValidateNodePayload(humanCtx, keg.NodeValidationPayload{
		ID: created.ID, Schema: "task", Content: []byte("# Projected\n"), HasContent: true,
	})
	require.NoError(t, err)
	require.True(t, result.Valid)
	require.Equal(t, "task", result.Type)
	meta, err := k.GetMeta(ctx, created.ID)
	require.NoError(t, err)
	typeName, _ := meta.Get("type")
	require.Equal(t, "note", typeName, "validation preview persisted its projection")

	cases := []struct {
		name    string
		payload keg.NodeValidationPayload
		want    string
	}{
		{"missing", keg.NodeValidationPayload{ID: created.ID, Content: []byte("# Preview\n"), HasContent: true}, "explicit schema selection is required"},
		{"unknown", keg.NodeValidationPayload{ID: created.ID, Schema: "missing", Content: []byte("# Preview\n"), HasContent: true}, "unknown schema"},
		{"conflict", keg.NodeValidationPayload{ID: created.ID, Schema: "task", Meta: []byte("type: note\n"), HasMeta: true}, "conflicts with metadata type"},
		{"invalid", keg.NodeValidationPayload{ID: created.ID, Schema: "task", Content: []byte("\n"), HasContent: true}, "title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := k.ValidateNodePayload(humanCtx, tc.payload)
			require.NoError(t, err, "preview validation is advisory")
			require.False(t, result.Valid)
			var messages []string
			for _, issue := range result.Issues {
				messages = append(messages, issue.Field+": "+issue.Message)
			}
			require.Contains(t, strings.Join(messages, "\n"), tc.want)
		})
	}
}

func TestResolveValidationModeParity(t *testing.T) {
	policy := &keg.SchemaPolicy{
		Human: keg.ValidationModeBlock,
		Agent: keg.ValidationModeOff,
		API:   keg.ValidationModeWarn,
	}
	ctx := context.Background()
	require.Equal(t, keg.ValidationModeBlock, keg.ResolveValidationMode(keg.WithValidationActor(ctx, keg.ValidationActorHuman), policy))
	require.Equal(t, keg.ValidationModeOff, keg.ResolveValidationMode(keg.WithValidationActor(ctx, keg.ValidationActorAgent), policy))
	require.Equal(t, keg.ValidationModeWarn, keg.ResolveValidationMode(keg.WithValidationActor(ctx, keg.ValidationActorAPI), policy))
	require.Equal(t, keg.ValidationModeWarn, keg.ResolveValidationMode(keg.WithValidationActor(ctx, keg.ValidationActorHuman), nil))
	require.Equal(t, keg.ValidationModeOff, keg.ResolveValidationMode(keg.WithValidationMode(keg.WithValidationActor(ctx, keg.ValidationActorHuman), keg.ValidationModeOff), policy))
}
