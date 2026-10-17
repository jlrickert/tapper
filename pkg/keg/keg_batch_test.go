package keg_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jlrickert/tapper/internal/testkegrepo"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

type failingBatchRepo struct {
	*testkegrepo.MemoryRepository
	writes int
	failAt int
}

func (r *failingBatchRepo) WriteContent(ctx context.Context, id keg.NodeId, data []byte) error {
	r.writes++
	if r.failAt > 0 && r.writes == r.failAt {
		return errors.New("injected batch content failure")
	}
	return r.MemoryRepository.WriteContent(ctx, id, data)
}

const batchTaskSchema = `type: task
meta:
  type: object
  required: [type]
  properties:
    type: {const: task}
markdown:
  requireTitle: true
`

func newStrictBatchKeg(t *testing.T) (*keg.LocalKeg, context.Context) {
	t.Helper()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, k.Init(ctx))
	require.NoError(t, k.CreateSchema(ctx, "task", []byte(batchTaskSchema)))
	return k, ctx
}

func TestCreateNodesResolvesPlaceholdersAndPreservesOrder(t *testing.T) {
	k, ctx := newStrictBatchKeg(t)
	results, err := k.CreateNodes(ctx, []keg.NodeCreate{
		{Key: "first", Schema: "task", Body: []byte("# First\n\n[Second](../{{node:second}})\n"), Meta: []byte("type: task\n")},
		{Key: "second", Schema: "task", Body: []byte("# Second\n\n[First](../{{node:first}})\n"), Meta: []byte("type: task\n")},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, []string{results[0].Key, results[1].Key})
	require.Equal(t, []string{"1", "2"}, []string{results[0].ID.Path(), results[1].ID.Path()})
	one, err := k.GetContent(ctx, results[0].ID)
	require.NoError(t, err)
	require.Contains(t, string(one), "[Second](../2)")
	two, err := k.GetContent(ctx, results[1].ID)
	require.NoError(t, err)
	require.Contains(t, string(two), "[First](../1)")
}

// A placeholder written as a bare link target gains the ../ a node link needs,
// so the link registers; everywhere else it stays the bare id, so ../ and
// keg: references written in full are not prefixed twice.
func TestCreateNodesLinksBarePlaceholderTargets(t *testing.T) {
	k, ctx := newStrictBatchKeg(t)
	body := "# Source\n\n" +
		"[Inline]({{node:target}})\n" +
		"[Spaced]( {{node:target}} )\n" +
		"[Relative](../{{node:target}})\n" +
		"Cross: keg:@ns/k/{{node:target}}\n" +
		"See node {{node:target}}.\n\n" +
		"[ref]: {{node:target}}\n"
	results, err := k.CreateNodes(ctx, []keg.NodeCreate{
		{Key: "source", Schema: "task", Body: []byte(body), Meta: []byte("type: task\n")},
		{Key: "target", Schema: "task", Body: []byte("# Target\n"), Meta: []byte("type: task\n")},
	})
	require.NoError(t, err)
	raw, err := k.GetContent(ctx, results[0].ID)
	require.NoError(t, err)
	got := string(raw)
	require.Contains(t, got, "[Inline](../2)")
	require.Contains(t, got, "[Spaced](../2)")
	require.Contains(t, got, "[Relative](../2)")
	require.NotContains(t, got, "../../2")
	require.Contains(t, got, "Cross: keg:@ns/k/2\n")
	require.Contains(t, got, "See node 2.")
	require.Contains(t, got, "[ref]: ../2\n")

	content, err := keg.ParseContent(k.Runtime, raw, keg.MarkdownContentFilename)
	require.NoError(t, err)
	require.Contains(t, content.Links, results[1].ID)
}

// A titled inline link still gains its ../, and a footnote definition is
// prose, not a link target, so its placeholder stays the bare id.
func TestCreateNodesPlaceholderTitlesAndFootnotes(t *testing.T) {
	k, ctx := newStrictBatchKeg(t)
	body := "# Source\n\n" +
		"[Titled]({{node:target}} \"T\")\n" +
		"[Single]({{node:target}} 'T')\n" +
		"[Paren]( {{node:target}} (T) )\n" +
		"See[^1].\n\n" +
		"[^1]: {{node:target}}\n" +
		"[ref]: {{node:target}}\n"
	results, err := k.CreateNodes(ctx, []keg.NodeCreate{
		{Key: "source", Schema: "task", Body: []byte(body), Meta: []byte("type: task\n")},
		{Key: "target", Schema: "task", Body: []byte("# Target\n"), Meta: []byte("type: task\n")},
	})
	require.NoError(t, err)
	raw, err := k.GetContent(ctx, results[0].ID)
	require.NoError(t, err)
	got := string(raw)
	require.Contains(t, got, "[Titled](../2 \"T\")\n")
	require.Contains(t, got, "[Single](../2 'T')\n")
	require.Contains(t, got, "[Paren](../2 (T))\n")
	require.Contains(t, got, "[^1]: 2\n")
	require.Contains(t, got, "[ref]: ../2\n")
}

func TestCreateNodesRejectsUnknownPlaceholderLinkTarget(t *testing.T) {
	k, ctx := newStrictBatchKeg(t)
	_, err := k.CreateNodes(ctx, []keg.NodeCreate{{Key: "one", Schema: "task", Body: []byte("# One\n\n[Missing]({{node:nope}})\n"), Meta: []byte("type: task\n")}})
	require.ErrorIs(t, err, keg.ErrInvalid)
	require.ErrorContains(t, err, `unknown node placeholder "nope"`)
}

func TestCreateNodesPreflightFailureLeavesKegUnchanged(t *testing.T) {
	k, ctx := newStrictBatchKeg(t)
	_, err := k.CreateNodes(ctx, []keg.NodeCreate{{Key: "one", Schema: "task", Body: []byte("# One\n"), Meta: []byte("type: task\n")}, {Key: "two", Schema: "task", Body: []byte("# Two\n\n[Missing](../{{node:nope}})\n"), Meta: []byte("type: task\n")}})
	require.Error(t, err)
	ids, err := k.ListNodes(ctx)
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{{ID: 0}}, ids)
}

// A malformed metadata document is the caller's mistake, so it has to classify
// as ErrInvalid — a transport mapping it by error kind (the hub maps ErrInvalid
// to 400) otherwise answers a client typo with a server error. The same holds
// for content opening with a frontmatter block, which is rejected outright now
// that content and meta are separate inputs.
func TestMutationBatchesRejectMalformedMetaAndContentFrontmatter(t *testing.T) {
	metas := []struct {
		name string
		meta string
	}{
		{"sequence", "- one\n- two\n"},
		{"scalar", "just a string"},
		{"unterminated flow", "a: [1, 2\n"},
		{"tab indent", "a:\n\t- b\n"},
	}
	for _, tc := range metas {
		t.Run("create meta "+tc.name, func(t *testing.T) {
			k, ctx := newStrictBatchKeg(t)
			_, err := k.CreateNodes(ctx, []keg.NodeCreate{{
				Key: "n", Schema: "task", Body: []byte("# One\n"), Meta: []byte(tc.meta),
			}})
			require.ErrorIs(t, err, keg.ErrInvalid)
			// No id is consumed by a rejected create.
			ids, err := k.ListNodes(ctx)
			require.NoError(t, err)
			require.Equal(t, []keg.NodeId{{ID: 0}}, ids)
		})
	}

	t.Run("update meta", func(t *testing.T) {
		k, ctx := newStrictBatchKeg(t)
		created, err := k.CreateNodes(ctx, []keg.NodeCreate{{
			Key: "n", Schema: "task", Body: []byte("# One\n"), Meta: []byte("type: task\n"),
		}})
		require.NoError(t, err)
		_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
			ID: created[0].ID, Schema: "task", Meta: []byte("- one\n"), HasMeta: true,
			ExpectedHash: created[0].Hash,
		}})
		require.ErrorIs(t, err, keg.ErrInvalid)
	})

	t.Run("update content frontmatter", func(t *testing.T) {
		k, ctx := newStrictBatchKeg(t)
		created, err := k.CreateNodes(ctx, []keg.NodeCreate{{
			Key: "n", Schema: "task", Body: []byte("# One\n"), Meta: []byte("type: task\n"),
		}})
		require.NoError(t, err)
		_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
			ID: created[0].ID, Schema: "task", HasContent: true,
			Content:      []byte("---\ntype: task\n---\n# One\n"),
			ExpectedHash: created[0].Hash,
		}})
		require.ErrorIs(t, err, keg.ErrInvalid)
		require.Contains(t, err.Error(), "must not start with a YAML frontmatter block")
	})

	// An update that does not carry content must not be judged on the stored
	// body: a node written before the rule existed can legitimately have one
	// that opens with `---`, and it still has to be editable.
	t.Run("stored frontmatter stays updatable", func(t *testing.T) {
		k, ctx := newStrictBatchKeg(t)
		created, err := k.CreateNodes(ctx, []keg.NodeCreate{{
			Key: "n", Schema: "task", Body: []byte("# One\n"), Meta: []byte("type: task\n"),
		}})
		require.NoError(t, err)
		// Write the legacy shape past the create guard, the way a node
		// predating this rule would already be stored.
		require.NoError(t, k.Repo.WriteContent(ctx, created[0].ID, []byte("---\ntype: task\n---\n# One\n")))
		view, err := k.ReadNode(ctx, created[0].ID)
		require.NoError(t, err)
		_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
			ID: created[0].ID, Schema: "task", Meta: []byte("type: task\n"), HasMeta: true,
			ExpectedHash: view.Hash(),
		}})
		require.NoError(t, err)
	})
}

func TestUpdateNodesPreflightsHashesAndSnapshotsAtomically(t *testing.T) {
	k, ctx := newStrictBatchKeg(t)
	created, err := k.CreateNodes(ctx, []keg.NodeCreate{{Key: "one", Schema: "task", Body: []byte("# One\n"), Meta: []byte("type: task\n")}, {Key: "two", Schema: "task", Body: []byte("# Two\n"), Meta: []byte("type: task\n")}})
	require.NoError(t, err)
	before, err := k.ReadNode(ctx, created[0].ID)
	require.NoError(t, err)
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{ID: created[0].ID, Schema: "task", Content: []byte("# Changed\n"), HasContent: true, ExpectedHash: before.Hash()}, {ID: created[1].ID, Schema: "task", Content: []byte("# Never\n"), HasContent: true, ExpectedHash: "stale"}})
	require.ErrorIs(t, err, keg.ErrConflict)
	after, err := k.ReadNode(ctx, created[0].ID)
	require.NoError(t, err)
	require.Equal(t, before.Content, after.Content)
	results, err := k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{ID: created[0].ID, Schema: "task", Content: []byte("# Changed\n"), HasContent: true, SnapshotBefore: true, ExpectedHash: after.Hash()}})
	require.NoError(t, err)
	require.NotEmpty(t, results[0].Hash)
	history, err := k.ListSnapshots(ctx, created[0].ID)
	require.NoError(t, err)
	require.Len(t, history, 1)
}

func TestMutationBatchLimitsAndDuplicates(t *testing.T) {
	k, ctx := newStrictBatchKeg(t)
	nodes := make([]keg.NodeCreate, keg.MaxMutationBatchSize+1)
	for i := range nodes {
		nodes[i] = keg.NodeCreate{Key: fmt.Sprintf("n%d", i)}
	}
	_, err := k.CreateNodes(ctx, nodes)
	require.ErrorIs(t, err, keg.ErrInvalid)
	_, err = k.CreateNodes(ctx, []keg.NodeCreate{{Key: "same"}, {Key: "same"}})
	require.ErrorIs(t, err, keg.ErrInvalid)
	_, err = k.AppendSnapshots(ctx, []keg.NodeSnapshotRequest{{ID: keg.NodeId{ID: 0}}, {ID: keg.NodeId{ID: 0}}})
	require.ErrorIs(t, err, keg.ErrInvalid)
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{ID: keg.NodeId{ID: 0}}})
	require.ErrorIs(t, err, keg.ErrInvalid)
}

func TestMutationBatchesRollbackCanonicalSnapshotsDexAndConfig(t *testing.T) {
	fx := NewSandbox(t)
	repo := &failingBatchRepo{MemoryRepository: newTestMemoryRepo(fx.Runtime())}
	failAt := func(n int) { repo.writes, repo.failAt = 0, n }
	k := keg.NewLocalKeg(repo, fx.Runtime())
	ctx := fx.Context()
	require.NoError(t, k.Init(ctx))
	require.NoError(t, k.UpdateSettings(ctx, func(cfg *keg.Settings) { cfg.SchemaPolicy.Strict = false }))

	beforeDex, err := k.DexArtifacts(ctx)
	require.NoError(t, err)
	beforeCfg, err := k.Settings(ctx)
	require.NoError(t, err)
	failAt(2)
	_, err = k.CreateNodes(ctx, []keg.NodeCreate{{Key: "one", Body: []byte("# One\n")}, {Key: "two", Body: []byte("# Two\n")}})
	require.ErrorContains(t, err, "injected batch content failure")
	ids, err := k.ListNodes(ctx)
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{{ID: 0}}, ids)
	afterDex, err := k.DexArtifacts(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeDex.Indexes, afterDex.Indexes)
	afterCfg, err := k.Settings(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeCfg.Updated, afterCfg.Updated)

	failAt(0)
	created, err := k.CreateNodes(ctx, []keg.NodeCreate{{Key: "one", Body: []byte("# One\n")}, {Key: "two", Body: []byte("# Two\n")}})
	require.NoError(t, err)
	beforeOne, err := k.ReadNode(ctx, created[0].ID)
	require.NoError(t, err)
	beforeTwo, err := k.ReadNode(ctx, created[1].ID)
	require.NoError(t, err)
	beforeDex, err = k.DexArtifacts(ctx)
	require.NoError(t, err)
	beforeCfg, err = k.Settings(ctx)
	require.NoError(t, err)

	failAt(2)
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{
		{ID: created[0].ID, Content: []byte("# Changed one\n"), HasContent: true, SnapshotBefore: true, ExpectedHash: beforeOne.Hash()},
		{ID: created[1].ID, Content: []byte("# Changed two\n"), HasContent: true, SnapshotBefore: true, ExpectedHash: beforeTwo.Hash()},
	})
	require.ErrorContains(t, err, "injected batch content failure")
	afterOne, err := k.ReadNode(ctx, created[0].ID)
	require.NoError(t, err)
	afterTwo, err := k.ReadNode(ctx, created[1].ID)
	require.NoError(t, err)
	require.Equal(t, beforeOne.Content, afterOne.Content)
	require.Equal(t, beforeTwo.Content, afterTwo.Content)
	for _, item := range created {
		history, historyErr := k.ListSnapshots(ctx, item.ID)
		require.NoError(t, historyErr)
		require.Empty(t, history)
	}
	afterDex, err = k.DexArtifacts(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeDex.Indexes, afterDex.Indexes)
	afterCfg, err = k.Settings(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeCfg.Updated, afterCfg.Updated)
}

func TestStrictPolicyUsesResolvedValidationMode(t *testing.T) {
	k, ctx := newStrictBatchKeg(t)
	cfg, err := k.Settings(ctx)
	require.NoError(t, err)
	require.NotNil(t, cfg.SchemaPolicy)
	require.True(t, cfg.SchemaPolicy.Strict)
	off := keg.WithValidationMode(ctx, keg.ValidationModeOff)
	_, err = k.Create(off, &keg.CreateOptions{Body: []byte("# Untyped\n")})
	require.NoError(t, err)
	_, err = k.Create(off, &keg.CreateOptions{Body: []byte("# Unknown\n"), Meta: []byte("type: missing\n")})
	require.NoError(t, err)
	_, err = k.Create(ctx, &keg.CreateOptions{Body: []byte("# Missing selection\n")})
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
	require.Contains(t, err.Error(), "schema: explicit schema selection is required")
	_, err = k.Create(ctx, &keg.CreateOptions{Schema: "task", Body: []byte("# Selected\n\n## Context\n")})
	require.NoError(t, err)
}

func TestExistingConfigWithoutStrictRemainsNonStrict(t *testing.T) {
	cfg, err := keg.ParseKegSettingsStrict([]byte("kegv: 2025-07\nschemaPolicy:\n  human: warn\n"))
	require.NoError(t, err)
	require.NotNil(t, cfg.SchemaPolicy)
	require.False(t, cfg.SchemaPolicy.Strict)

	legacy, err := keg.ParseKegSettingsStrict([]byte("kegv: 2023-01\ntitle: Legacy\n"))
	require.NoError(t, err)
	require.True(t, legacy.SchemaPolicy == nil || !legacy.SchemaPolicy.Strict)
}

func TestEnablingStrictDoesNotScanExistingNodes(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, k.Init(ctx))
	require.NoError(t, k.UpdateSettings(ctx, func(cfg *keg.Settings) { cfg.SchemaPolicy.Strict = false }))
	_, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Legacy one\n")})
	require.NoError(t, err)
	_, err = k.Create(ctx, &keg.CreateOptions{Body: []byte("# Legacy two\n")})
	require.NoError(t, err)
	err = k.UpdateSettings(ctx, func(cfg *keg.Settings) { cfg.SchemaPolicy.Strict = true })
	require.NoError(t, err)
	cfg, err := k.Settings(ctx)
	require.NoError(t, err)
	require.True(t, cfg.SchemaPolicy.Strict)
}

func TestStrictSchemaChangeAndSnapshotRestoreRemainExempt(t *testing.T) {
	k, ctx := newStrictBatchKeg(t)
	created, err := k.Create(ctx, &keg.CreateOptions{Schema: "task", Body: []byte("# Valid\n\n## Context\n")})
	require.NoError(t, err)

	currentSchema, err := k.ReadSchema(ctx, "task")
	require.NoError(t, err)
	err = k.WriteSchema(ctx, "task", []byte(`type: task
meta:
  type: object
  required: [type]
markdown:
  requireTitle: true
  sections:
    - heading: Required
      level: 2
      required: true
`), keg.SchemaWriteOptions{ExpectedHash: keg.DocumentHash(currentSchema)})
	require.NoError(t, err)
	currentSchema, err = k.ReadSchema(ctx, "task")
	require.NoError(t, err)
	err = k.DeleteSchema(ctx, "task", keg.SchemaWriteOptions{ExpectedHash: keg.DocumentHash(currentSchema)})
	require.NoError(t, err)
	require.NoError(t, k.CreateSchema(ctx, "task", []byte(`type: task
meta:
  type: object
  required: [type]
  properties:
    type: {const: task}
markdown:
  requireTitle: true
  sections:
    - heading: Context
      level: 2
      required: true
`)))

	require.NoError(t, k.UpdateSettings(ctx, func(cfg *keg.Settings) { cfg.SchemaPolicy.Strict = false }))
	require.NoError(t, k.SetContent(keg.WithValidationMode(ctx, keg.ValidationModeOff), created.ID, []byte("# Missing context\n")))
	snapshot, err := k.AppendSnapshot(ctx, created.ID, "invalid legacy revision")
	require.NoError(t, err)
	require.NoError(t, k.SetContent(keg.WithValidationMode(ctx, keg.ValidationModeOff), created.ID, []byte("# Valid again\n\n## Context\n")))
	require.NoError(t, k.UpdateSettings(ctx, func(cfg *keg.Settings) { cfg.SchemaPolicy.Strict = true }))
	err = k.RestoreSnapshot(keg.WithValidationMode(ctx, keg.ValidationModeOff), created.ID, snapshot.ID)
	require.NoError(t, err)
	content, err := k.GetContent(ctx, created.ID)
	require.NoError(t, err)
	require.Contains(t, string(content), "# Missing context")
}

func TestStrictArchiveImportRemainsExempt(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	source := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, source.Init(ctx))
	require.NoError(t, source.UpdateSettings(ctx, func(cfg *keg.Settings) { cfg.SchemaPolicy.Strict = false }))
	invalid, err := source.Create(keg.WithValidationMode(ctx, keg.ValidationModeOff), &keg.CreateOptions{Body: []byte("# Untyped legacy\n")})
	require.NoError(t, err)
	archive, err := source.ExportNodes(ctx, keg.ExportNodesOptions{NodeIDs: []keg.NodeId{invalid.ID}, SkipZeroNode: true})
	require.NoError(t, err)
	defer archive.Close()

	destination, strictCtx := newStrictBatchKeg(t)
	before, err := destination.ListNodes(strictCtx)
	require.NoError(t, err)
	_, err = destination.ImportNodes(strictCtx, archive, keg.ImportNodesOptions{AssignNewIDs: true})
	require.NoError(t, err)
	after, err := destination.ListNodes(strictCtx)
	require.NoError(t, err)
	require.Len(t, after, len(before)+1)
}

func TestUpdateKeepsALegacyFrontmatterBodyEditable(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	legacy, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# T\n")})
	require.NoError(t, err)
	// Store the legacy shape past the write guard, as a node predating the
	// no-frontmatter rule already is.
	require.NoError(t, k.Repo.WriteContent(ctx, legacy.ID, []byte("---\na: 1\n---\n# T\n")))
	view, err := k.ReadNode(ctx, legacy.ID)
	require.NoError(t, err)

	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: legacy.ID, Content: []byte("---\na: 1\n---\n# T\n\nMore.\n"), HasContent: true, ExpectedContentHash: view.ContentHash(),
	}})
	require.NoError(t, err, "a full-content edit keeping the stored block is allowed")
	got, err := k.ReadNode(ctx, legacy.ID)
	require.NoError(t, err)
	require.Equal(t, "---\na: 1\n---\n# T\n\nMore.\n", string(got.Content))

	normal, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# N\n")})
	require.NoError(t, err)
	nview, err := k.ReadNode(ctx, normal.ID)
	require.NoError(t, err)
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: normal.ID, Content: []byte("---\na: 1\n---\n# N\n"), HasContent: true, ExpectedContentHash: nview.ContentHash(),
	}})
	require.ErrorIs(t, err, keg.ErrInvalid, "content may not introduce a frontmatter block")
	unchanged, err := k.ReadNode(ctx, normal.ID)
	require.NoError(t, err)
	require.Equal(t, "# N\n", string(unchanged.Content))
}

func TestPlanContentUpdate(t *testing.T) {
	t.Parallel()
	got, has, err := keg.PlanContentUpdate([]byte("# A\n"), keg.NodeUpdateOptions{})
	require.NoError(t, err)
	require.False(t, has)
	require.Equal(t, "# A\n", string(got))

	got, has, err = keg.PlanContentUpdate([]byte("# A\nx\n"), keg.NodeUpdateOptions{ContentPatch: "@@ -2 +2 @@\n-x\n+y\n", HasContentPatch: true})
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, "# A\ny\n", string(got))

	_, _, err = keg.PlanContentUpdate([]byte("# A\n"), keg.NodeUpdateOptions{Content: []byte("# B\n"), HasContent: true, ContentPatch: "@@ -1 +1 @@\n-# A\n+# B\n", HasContentPatch: true})
	require.ErrorIs(t, err, keg.ErrInvalid)

	_, _, err = keg.PlanContentUpdate([]byte("# A\n"), keg.NodeUpdateOptions{ContentPatch: "@@ -1 +1,3 @@\n-# A\n+---\n+a: 1\n+---\n", HasContentPatch: true})
	require.ErrorIs(t, err, keg.ErrInvalid)
}
