package keg_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

type failingMemoryBatchRepo struct {
	*keg.MemoryRepo
	writes int
	failAt int
}

func (r *failingMemoryBatchRepo) WriteContent(ctx context.Context, id keg.NodeId, data []byte) error {
	r.writes++
	if r.failAt > 0 && r.writes == r.failAt {
		return errors.New("injected batch content failure")
	}
	return r.MemoryRepo.WriteContent(ctx, id, data)
}

type failingFSBatchRepo struct {
	*keg.FsRepo
	writes int
	failAt int
}

func (r *failingFSBatchRepo) WriteContent(ctx context.Context, id keg.NodeId, data []byte) error {
	r.writes++
	if r.failAt > 0 && r.writes == r.failAt {
		return errors.New("injected batch content failure")
	}
	return r.FsRepo.WriteContent(ctx, id, data)
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
	k := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, k.Init(ctx))
	require.NoError(t, k.CreateSchema(ctx, "task", []byte(batchTaskSchema)))
	return k, ctx
}

func TestCreateNodesResolvesPlaceholdersAndPreservesOrder(t *testing.T) {
	k, ctx := newStrictBatchKeg(t)
	results, err := k.CreateNodes(ctx, []keg.NodeCreate{
		{Key: "first", Schema: "task", Body: []byte("# First\n\n[Second](../{{node:second}})\n"), Attrs: map[string]any{"type": "task"}},
		{Key: "second", Schema: "task", Body: []byte("# Second\n\n[First](../{{node:first}})\n"), Attrs: map[string]any{"type": "task"}},
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

func TestCreateNodesPreflightFailureLeavesKegUnchanged(t *testing.T) {
	k, ctx := newStrictBatchKeg(t)
	_, err := k.CreateNodes(ctx, []keg.NodeCreate{{Key: "one", Schema: "task", Body: []byte("# One\n"), Attrs: map[string]any{"type": "task"}}, {Key: "two", Schema: "task", Body: []byte("# Two\n\n[Missing](../{{node:nope}})\n"), Attrs: map[string]any{"type": "task"}}})
	require.Error(t, err)
	ids, err := k.ListNodes(ctx)
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{{ID: 0}}, ids)
}

func TestUpdateNodesPreflightsHashesAndSnapshotsAtomically(t *testing.T) {
	k, ctx := newStrictBatchKeg(t)
	created, err := k.CreateNodes(ctx, []keg.NodeCreate{{Key: "one", Schema: "task", Body: []byte("# One\n"), Attrs: map[string]any{"type": "task"}}, {Key: "two", Schema: "task", Body: []byte("# Two\n"), Attrs: map[string]any{"type": "task"}}})
	require.NoError(t, err)
	before, err := k.ReadNode(ctx, created[0].ID)
	require.NoError(t, err)
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{ID: created[0].ID, Schema: "task", Content: []byte("# Changed\n"), HasContent: true}, {ID: created[1].ID, Schema: "task", Content: []byte("# Never\n"), HasContent: true, ExpectedHash: "stale"}})
	require.ErrorIs(t, err, keg.ErrConflict)
	after, err := k.ReadNode(ctx, created[0].ID)
	require.NoError(t, err)
	require.Equal(t, before.Content, after.Content)
	results, err := k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{ID: created[0].ID, Schema: "task", Content: []byte("# Changed\n"), HasContent: true, SnapshotBefore: true}})
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
	for _, name := range []string{"memory", "filesystem"} {
		t.Run(name, func(t *testing.T) {
			fx := NewSandbox(t)
			var repo keg.Repository
			var failAt func(int)
			if name == "memory" {
				r := &failingMemoryBatchRepo{MemoryRepo: keg.NewMemoryRepo(fx.Runtime())}
				repo = r
				failAt = func(n int) { r.writes, r.failAt = 0, n }
			} else {
				r := &failingFSBatchRepo{FsRepo: keg.NewFsRepo("~/batch-rollback", fx.Runtime())}
				repo = r
				failAt = func(n int) { r.writes, r.failAt = 0, n }
			}
			k := keg.NewLocalKeg(repo, fx.Runtime())
			ctx := fx.Context()
			require.NoError(t, k.Init(ctx))
			require.NoError(t, k.UpdateConfig(ctx, func(cfg *keg.Config) { cfg.SchemaPolicy.Strict = false }))

			beforeDex, err := k.DexArtifacts(ctx)
			require.NoError(t, err)
			beforeCfg, err := k.Config(ctx)
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
			afterCfg, err := k.Config(ctx)
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
			beforeCfg, err = k.Config(ctx)
			require.NoError(t, err)

			failAt(2)
			_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{
				{ID: created[0].ID, Content: []byte("# Changed one\n"), HasContent: true, SnapshotBefore: true},
				{ID: created[1].ID, Content: []byte("# Changed two\n"), HasContent: true, SnapshotBefore: true},
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
			afterCfg, err = k.Config(ctx)
			require.NoError(t, err)
			require.Equal(t, beforeCfg.Updated, afterCfg.Updated)
		})
	}
}

func TestStrictPolicyUsesResolvedValidationMode(t *testing.T) {
	k, ctx := newStrictBatchKeg(t)
	cfg, err := k.Config(ctx)
	require.NoError(t, err)
	require.NotNil(t, cfg.SchemaPolicy)
	require.True(t, cfg.SchemaPolicy.Strict)
	off := keg.WithValidationMode(ctx, keg.ValidationModeOff)
	_, err = k.Create(off, &keg.CreateOptions{Body: []byte("# Untyped\n")})
	require.NoError(t, err)
	_, err = k.Create(off, &keg.CreateOptions{Body: []byte("---\ntype: missing\n---\n# Unknown\n")})
	require.NoError(t, err)
	_, err = k.Create(ctx, &keg.CreateOptions{Body: []byte("# Missing selection\n")})
	require.ErrorIs(t, err, keg.ErrSchemaInvalid)
	require.Contains(t, err.Error(), "schema: explicit schema selection is required")
	_, err = k.Create(ctx, &keg.CreateOptions{Schema: "task", Body: []byte("# Selected\n\n## Context\n")})
	require.NoError(t, err)
}

func TestExistingConfigWithoutStrictRemainsNonStrict(t *testing.T) {
	cfg, err := keg.ParseKegConfigStrict([]byte("kegv: 2025-07\nschemaPolicy:\n  human: warn\n"))
	require.NoError(t, err)
	require.NotNil(t, cfg.SchemaPolicy)
	require.False(t, cfg.SchemaPolicy.Strict)

	legacy, err := keg.ParseKegConfigStrict([]byte("kegv: 2023-01\ntitle: Legacy\n"))
	require.NoError(t, err)
	require.True(t, legacy.SchemaPolicy == nil || !legacy.SchemaPolicy.Strict)
}

func TestEnablingStrictDoesNotScanExistingNodes(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, k.Init(ctx))
	require.NoError(t, k.UpdateConfig(ctx, func(cfg *keg.Config) { cfg.SchemaPolicy.Strict = false }))
	_, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Legacy one\n")})
	require.NoError(t, err)
	_, err = k.Create(ctx, &keg.CreateOptions{Body: []byte("# Legacy two\n")})
	require.NoError(t, err)
	err = k.UpdateConfig(ctx, func(cfg *keg.Config) { cfg.SchemaPolicy.Strict = true })
	require.NoError(t, err)
	cfg, err := k.Config(ctx)
	require.NoError(t, err)
	require.True(t, cfg.SchemaPolicy.Strict)
}

func TestStrictSchemaChangeAndSnapshotRestoreRemainExempt(t *testing.T) {
	k, ctx := newStrictBatchKeg(t)
	created, err := k.Create(ctx, &keg.CreateOptions{Schema: "task", Body: []byte("# Valid\n\n## Context\n")})
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
`))
	require.NoError(t, err)
	err = k.DeleteSchema(ctx, "task")
	require.NoError(t, err)
	require.NoError(t, k.WriteSchema(ctx, "task", []byte(`type: task
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

	require.NoError(t, k.UpdateConfig(ctx, func(cfg *keg.Config) { cfg.SchemaPolicy.Strict = false }))
	require.NoError(t, k.SetContent(keg.WithValidationMode(ctx, keg.ValidationModeOff), created.ID, []byte("# Missing context\n")))
	snapshot, err := k.AppendSnapshot(ctx, created.ID, "invalid legacy revision")
	require.NoError(t, err)
	require.NoError(t, k.SetContent(keg.WithValidationMode(ctx, keg.ValidationModeOff), created.ID, []byte("# Valid again\n\n## Context\n")))
	require.NoError(t, k.UpdateConfig(ctx, func(cfg *keg.Config) { cfg.SchemaPolicy.Strict = true }))
	err = k.RestoreSnapshot(keg.WithValidationMode(ctx, keg.ValidationModeOff), created.ID, snapshot.ID)
	require.NoError(t, err)
	content, err := k.GetContent(ctx, created.ID)
	require.NoError(t, err)
	require.Contains(t, string(content), "# Missing context")
}

func TestStrictArchiveImportRemainsExempt(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	source := keg.NewLocalKeg(keg.NewMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, source.Init(ctx))
	require.NoError(t, source.UpdateConfig(ctx, func(cfg *keg.Config) { cfg.SchemaPolicy.Strict = false }))
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
