package keg_test

import (
	"context"
	"errors"
	"regexp"
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

	// Content is never a second place to declare a type: a body opening with
	// frontmatter is rejected outright, before schema selection is considered.
	// That makes it a malformed request (ErrInvalid), not a schema violation.
	_, err = k.Create(ctx, &keg.CreateOptions{Schema: "task", Body: []byte("---\ntype: note\n---\n# Conflict\n"), Meta: []byte("type: task\n")})
	require.ErrorIs(t, err, keg.ErrInvalid)
	require.Contains(t, err.Error(), "must not start with a YAML frontmatter block")

	// A schema selection disagreeing with the type declared in meta stays a
	// schema error, and is still never resolved by precedence.
	_, err = k.Create(ctx, &keg.CreateOptions{Schema: "task", Body: []byte("# Conflict\n"), Meta: []byte("type: note\n")})
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
	require.Contains(t, err.Error(), "conflicts with metadata type")

	_, err = k.Create(ctx, &keg.CreateOptions{Schema: "  ", Body: []byte("# Whitespace\n")})
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
	_, err = k.Create(ctx, &keg.CreateOptions{Schema: "missing", Body: []byte("# Unknown\n")})
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
	require.Contains(t, err.Error(), "schema: unknown schema \"missing\"")
	require.Contains(t, err.Error(), "this keg defines:", "an unknown schema names the keg's types")

	// A selection that cannot be a type name is refused by the keg itself,
	// so a backend without an input schema in front behaves the same.
	_, err = k.Create(ctx, &keg.CreateOptions{Schema: "a note about planning", Body: []byte("# Prose\n")})
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
	require.Contains(t, err.Error(), "one word with no spaces")
	require.Contains(t, err.Error(), "this keg defines:")
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
		// A metadata edit needs a selection; a content-only edit would not.
		{ID: created[1].ID, Meta: []byte("type: task\nowner: alice\n"), HasMeta: true, SnapshotBefore: true, ExpectedHash: two.Hash()},
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

// TestStrictSchemaSelectionEditRules pins the three strict edit cases for an
// agent: a content-only edit keeps the node's type, while a metadata-only edit
// and an edit of both parts need an explicit selection.
func TestStrictSchemaSelectionEditRules(t *testing.T) {
	k, ctx := newSchemaSelectionKeg(t)
	agentCtx := keg.WithValidationActor(ctx, keg.ValidationActorAgent)
	created, err := k.Create(agentCtx, &keg.CreateOptions{Schema: "note", Body: []byte("# Typed\n")})
	require.NoError(t, err)

	update := func(opts keg.NodeUpdateOptions) error {
		t.Helper()
		view, err := k.ReadNode(ctx, created.ID)
		require.NoError(t, err)
		opts.ID, opts.ExpectedHash = created.ID, view.Hash()
		_, err = k.UpdateNodes(agentCtx, []keg.NodeUpdateOptions{opts})
		return err
	}
	typeOf := func() string {
		t.Helper()
		meta, err := k.GetMeta(ctx, created.ID)
		require.NoError(t, err)
		typeName, _ := meta.Get("type")
		return typeName
	}

	t.Run("content only keeps the type", func(t *testing.T) {
		require.NoError(t, update(keg.NodeUpdateOptions{Content: []byte("# Body edit\n"), HasContent: true}))
		require.Equal(t, "note", typeOf())
		content, err := k.GetContent(ctx, created.ID)
		require.NoError(t, err)
		require.Equal(t, "# Body edit\n", string(content))
	})

	t.Run("content only is validated against the kept type", func(t *testing.T) {
		err := update(keg.NodeUpdateOptions{Content: []byte("\n"), HasContent: true})
		require.ErrorIs(t, err, keg.ErrSchemaInvalid)
		require.Contains(t, err.Error(), "title")
	})

	t.Run("meta only needs a selection", func(t *testing.T) {
		err := update(keg.NodeUpdateOptions{Meta: []byte("type: note\nowner: alice\n"), HasMeta: true})
		require.ErrorIs(t, err, keg.ErrSchemaInvalid)
		require.Contains(t, err.Error(), "explicit schema selection is required: pass the schema field")
		require.Contains(t, err.Error(), "this keg defines: note, task")
		require.NoError(t, update(keg.NodeUpdateOptions{Schema: "note", Meta: []byte("type: note\nowner: alice\n"), HasMeta: true}))
	})

	t.Run("both parts need a selection and follow it", func(t *testing.T) {
		err := update(keg.NodeUpdateOptions{Content: []byte("# Both\n"), HasContent: true, Meta: []byte("type: note\n"), HasMeta: true})
		require.ErrorIs(t, err, keg.ErrSchemaInvalid)
		require.Contains(t, err.Error(), "explicit schema selection is required")

		err = update(keg.NodeUpdateOptions{Schema: "note", Content: []byte("\n"), HasContent: true, Meta: []byte("type: note\n"), HasMeta: true})
		require.ErrorIs(t, err, keg.ErrSchemaInvalid, "content must satisfy the selected schema")
		require.Contains(t, err.Error(), "title")

		err = update(keg.NodeUpdateOptions{Schema: "note", Content: []byte("# Both\n"), HasContent: true, Meta: []byte("type: task\n"), HasMeta: true})
		require.ErrorIs(t, err, keg.ErrSchemaInvalid, "meta must agree with the selected schema")

		require.NoError(t, update(keg.NodeUpdateOptions{Schema: "task", Content: []byte("# Both\n"), HasContent: true, Meta: []byte("type: task\n"), HasMeta: true}))
		require.Equal(t, "task", typeOf())
	})

	t.Run("preview agrees with the write", func(t *testing.T) {
		result, err := k.ValidateNodePayload(agentCtx, keg.NodeValidationPayload{ID: created.ID, Content: []byte("# Preview\n"), HasContent: true})
		require.NoError(t, err)
		require.True(t, result.Valid, "%+v", result.Issues)
	})

	t.Run("untyped node has no type to keep", func(t *testing.T) {
		require.NoError(t, k.UpdateSettings(ctx, func(cfg *keg.Settings) { cfg.SchemaPolicy.Strict = false }))
		untyped, err := k.Create(agentCtx, &keg.CreateOptions{Body: []byte("# Untyped\n")})
		require.NoError(t, err)
		require.NoError(t, k.UpdateSettings(ctx, func(cfg *keg.Settings) { cfg.SchemaPolicy.Strict = true }))
		view, err := k.ReadNode(ctx, untyped.ID)
		require.NoError(t, err)
		_, err = k.UpdateNodes(agentCtx, []keg.NodeUpdateOptions{{ID: untyped.ID, Content: []byte("# Still untyped\n"), HasContent: true, ExpectedHash: view.Hash()}})
		require.ErrorIs(t, err, keg.ErrSchemaInvalid)
		require.Contains(t, err.Error(), "explicit schema selection is required")
	})

	t.Run("create with only meta.type needs a selection", func(t *testing.T) {
		_, err := k.Create(agentCtx, &keg.CreateOptions{Body: []byte("# Declared\n"), Meta: []byte("type: note\n")})
		require.ErrorIs(t, err, keg.ErrSchemaInvalid)
		require.Contains(t, err.Error(), "this keg defines: note, task")
		require.NotContains(t, err.Error(), "keeps the node's type", "the edit hint is for updates only")
	})
}

func TestSchemaAwareDirectWritesPersistReplaceAndMutateAtomically(t *testing.T) {
	k, ctx := newSchemaSelectionKeg(t)
	humanCtx := keg.WithValidationActor(ctx, keg.ValidationActorHuman)
	require.NoError(t, k.UpdateSettings(ctx, func(cfg *keg.Settings) {
		cfg.SchemaPolicy = &keg.SchemaPolicy{Strict: true, Human: keg.ValidationModeBlock}
	}))
	created, err := k.Create(humanCtx, &keg.CreateOptions{Schema: "note", Body: []byte("# Typed\n")})
	require.NoError(t, err)

	// Content whose frontmatter declares another type changes the node's
	// type, so it needs a selection even though metadata is untouched.
	err = k.SetContent(humanCtx, created.ID, []byte("---\ntype: task\n---\n# Missing selection\n"))
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
		{"missing", keg.NodeValidationPayload{ID: created.ID, Meta: []byte("type: note\nowner: alice\n"), HasMeta: true}, "explicit schema selection is required"},
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

func TestValidSchemaTypeNameMatchesItsPattern(t *testing.T) {
	t.Parallel()
	pattern := regexp.MustCompile(keg.SchemaTypeNamePattern)
	for name, valid := range map[string]bool{
		"task": true, "meeting-note": true, "v2.task": true, "Task_2": true,
		"": false, ".": false, "..": false, "a..b": false, "a/b": false, `a\b`: false,
		"a note": false, "tab\tname": false, strings.Repeat("x", keg.SchemaTypeNameMaxLength+1): false,
	} {
		err := keg.ValidSchemaTypeName(name)
		if valid {
			require.NoError(t, err, name)
		} else {
			require.ErrorIs(t, err, keg.ErrInvalid, name)
		}
		if len(name) <= keg.SchemaTypeNameMaxLength && name != "" {
			require.Equal(t, valid, pattern.MatchString(name), "pattern and rule disagree on %q", name)
		}
	}
}
