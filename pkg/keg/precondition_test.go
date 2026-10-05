package keg_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestDocumentHashUsesFixedSHA256(t *testing.T) {
	data := []byte("same document everywhere\n")
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(data)), keg.DocumentHash(data))
	require.Empty(t, keg.DocumentHash(nil))
}

func TestLocalKegNodePreconditionsProtectContentAndMetadataTogether(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	created, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Original\n"), Meta: []byte("tags:\n  - before\n")})
	require.NoError(t, err)
	original, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)

	_, err = k.UpdateNode(ctx, keg.NodeUpdateOptions{ID: created.ID, Content: []byte("# Missing token\n")})
	require.ErrorIs(t, err, keg.ErrPreconditionRequired)
	unchanged, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, original.Content, unchanged.Content)
	require.Equal(t, original.Meta, unchanged.Meta)

	metaResults, err := k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: created.ID, Meta: []byte("tags: [after]\n"), HasMeta: true, ExpectedHash: original.Hash(),
	}})
	require.NoError(t, err)
	require.NotEqual(t, original.Hash(), metaResults[0].Hash)

	_, err = k.UpdateNode(ctx, keg.NodeUpdateOptions{ID: created.ID, Content: []byte("# Stale content\n"), ExpectedHash: original.Hash()})
	require.Error(t, err)
	require.ErrorIs(t, err, keg.ErrConflict)
	var conflict *keg.PreconditionConflictError
	require.True(t, errors.As(err, &conflict))
	current, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, current.Hash(), conflict.CurrentHash)
	expectedRecovery := "---\n" + string(current.Meta)
	if len(expectedRecovery) > 0 && expectedRecovery[len(expectedRecovery)-1] != '\n' {
		expectedRecovery += "\n"
	}
	expectedRecovery += "---\n" + string(current.Content)
	require.Equal(t, expectedRecovery, string(conflict.CurrentContent))
	require.Equal(t, "# Original\n", string(current.Content))

	result, err := k.UpdateNode(ctx, keg.NodeUpdateOptions{ID: created.ID, Content: []byte("# Current\n"), ExpectedHash: current.Hash()})
	require.NoError(t, err)
	require.NotEmpty(t, result.Hash)
	current, err = k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "# Current\n", string(current.Content))
}

func TestLocalKegMoveAndRemoveRequireCurrentNodeHash(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	movable, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Movable\n")})
	require.NoError(t, err)
	view, err := k.ReadNode(ctx, movable.ID)
	require.NoError(t, err)
	destination := keg.NodeId{ID: movable.ID.ID + 10}

	_, err = k.Move(ctx, keg.NodeMoveOptions{Source: movable.ID, Destination: destination})
	require.ErrorIs(t, err, keg.ErrPreconditionRequired)
	exists, err := k.NodeExists(ctx, movable.ID)
	require.NoError(t, err)
	require.True(t, exists)

	_, err = k.Move(ctx, keg.NodeMoveOptions{Source: movable.ID, Destination: destination, ExpectedHash: "stale"})
	require.ErrorIs(t, err, keg.ErrConflict)
	exists, err = k.NodeExists(ctx, destination)
	require.NoError(t, err)
	require.False(t, exists)

	_, err = k.Move(ctx, keg.NodeMoveOptions{Source: movable.ID, Destination: destination, ExpectedHash: view.Hash()})
	require.NoError(t, err)
	moved, err := k.ReadNode(ctx, destination)
	require.NoError(t, err)

	_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: destination})
	require.ErrorIs(t, err, keg.ErrPreconditionRequired)
	_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: destination, ExpectedHash: "stale"})
	require.ErrorIs(t, err, keg.ErrConflict)
	exists, err = k.NodeExists(ctx, destination)
	require.NoError(t, err)
	require.True(t, exists)

	_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: destination, ExpectedHash: moved.Hash()})
	require.NoError(t, err)
	exists, err = k.NodeExists(ctx, destination)
	require.NoError(t, err)
	require.False(t, exists)
}

func TestLocalKegSettingsPreconditionsUseExactPersistedDocument(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	repo := newTestMemoryRepo(fx.Runtime())
	k := keg.NewLocalKeg(repo, fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)

	currentRaw := []byte("kegv: 2025-07\ntitle: Current\nsummary: |\n  exact formatting\n")
	require.NoError(t, repo.WriteSettingsDocument(ctx, currentRaw))
	current, err := k.Settings(ctx)
	require.NoError(t, err)
	require.Equal(t, currentRaw, current.Raw())

	nextRaw := []byte("kegv: 2025-07\ntitle: Next\nsummary: changed\n")
	err = k.SetSettings(ctx, nextRaw, keg.SettingsWriteOptions{})
	require.ErrorIs(t, err, keg.ErrPreconditionRequired)
	err = k.SetSettings(ctx, nextRaw, keg.SettingsWriteOptions{ExpectedHash: "stale"})
	require.ErrorIs(t, err, keg.ErrConflict)
	var conflict *keg.PreconditionConflictError
	require.True(t, errors.As(err, &conflict))
	require.Equal(t, keg.DocumentHash(currentRaw), conflict.CurrentHash)
	require.Equal(t, currentRaw, conflict.CurrentContent)
	stored, err := repo.ReadSettingsDocument(ctx)
	require.NoError(t, err)
	require.Equal(t, currentRaw, stored)

	require.NoError(t, k.SetSettings(ctx, nextRaw, keg.SettingsWriteOptions{ExpectedHash: current.Hash()}))
	stored, err = repo.ReadSettingsDocument(ctx)
	require.NoError(t, err)
	require.Equal(t, nextRaw, stored)
}

func TestLocalKegSchemaUpdateAndDeletePreconditionsUseStoredYAML(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	original := []byte("type: task\nsummary: |\n  exact schema\n")
	require.NoError(t, k.CreateSchema(ctx, "task", original))
	next := []byte("type: task\nsummary: updated\n")

	err := k.WriteSchema(ctx, "missing", []byte("type: missing\n"), keg.SchemaWriteOptions{ExpectedHash: "unused"})
	require.ErrorIs(t, err, keg.ErrNotExist, "WriteSchema is update-only; CreateSchema owns creation")
	err = k.WriteSchema(ctx, "task", next, keg.SchemaWriteOptions{})
	require.ErrorIs(t, err, keg.ErrPreconditionRequired)
	err = k.WriteSchema(ctx, "task", next, keg.SchemaWriteOptions{ExpectedHash: "stale"})
	require.ErrorIs(t, err, keg.ErrConflict)
	var conflict *keg.PreconditionConflictError
	require.True(t, errors.As(err, &conflict))
	require.Equal(t, original, conflict.CurrentContent)
	stored, err := k.ReadSchema(ctx, "task")
	require.NoError(t, err)
	require.Equal(t, original, stored)

	require.NoError(t, k.WriteSchema(ctx, "task", next, keg.SchemaWriteOptions{ExpectedHash: keg.DocumentHash(original)}))
	err = k.DeleteSchema(ctx, "task", keg.SchemaWriteOptions{})
	require.ErrorIs(t, err, keg.ErrPreconditionRequired)
	err = k.DeleteSchema(ctx, "task", keg.SchemaWriteOptions{ExpectedHash: keg.DocumentHash(original)})
	require.ErrorIs(t, err, keg.ErrConflict)
	stored, err = k.ReadSchema(ctx, "task")
	require.NoError(t, err)
	require.Equal(t, next, stored)
	require.NoError(t, k.DeleteSchema(ctx, "task", keg.SchemaWriteOptions{ExpectedHash: keg.DocumentHash(next)}))
	_, err = k.ReadSchema(ctx, "task")
	require.ErrorIs(t, err, keg.ErrNotExist)
}

func TestLocalKegQueryRemovalPinsHashesInsideWriteBoundary(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	one, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# One\n"), Meta: []byte("tags:\n  - discard\n")})
	require.NoError(t, err)
	two, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Two\n"), Meta: []byte("tags:\n  - keep\n")})
	require.NoError(t, err)

	result, err := k.RemoveNodes(ctx, keg.RemoveNodesOptions{Query: "discard"})
	require.NoError(t, err)
	require.Nil(t, result.Failure)
	require.Equal(t, []keg.NodeId{one.ID}, []keg.NodeId{result.Removed[0].ID})
	exists, err := k.NodeExists(ctx, one.ID)
	require.NoError(t, err)
	require.False(t, exists)
	exists, err = k.NodeExists(ctx, two.ID)
	require.NoError(t, err)
	require.True(t, exists)
}

func TestNodePartHashesAreDeterministicAndNormalized(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	emptyContent, emptyMeta := keg.NodePartHashes(fx.Runtime(), nil, nil)
	require.NotEmpty(t, emptyContent, "empty content still has a token")
	require.NotEmpty(t, emptyMeta, "absent meta still has a token")
	parsedEmpty, err := keg.ParseMeta(ctx, nil)
	require.NoError(t, err)
	_, parsedEmptyMeta := keg.NodePartHashes(fx.Runtime(), []byte{}, parsedEmpty)
	require.Equal(t, emptyMeta, parsedEmptyMeta, "nil and empty meta share a token")

	content := []byte("# Title\n\nBody.\n")
	parsed, err := keg.ParseContent(fx.Runtime(), content, keg.MarkdownContentFilename)
	require.NoError(t, err)
	contentHash, _ := keg.NodePartHashes(fx.Runtime(), content, nil)
	require.Equal(t, parsed.Hash, contentHash, "content token matches ParseContent's hash")

	a, err := keg.ParseMeta(ctx, []byte("tags:\n  - b\n  - a\n"))
	require.NoError(t, err)
	b, err := keg.ParseMeta(ctx, []byte("tags:\n  - a\n  - b\n"))
	require.NoError(t, err)
	_, aHash := keg.NodePartHashes(fx.Runtime(), nil, a)
	_, bHash := keg.NodePartHashes(fx.Runtime(), nil, b)
	require.Equal(t, aHash, bHash, "meta token uses the normalized YAML")
}

func TestLocalKegNodePartPreconditionsAreIndependent(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	created, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Original\n"), Meta: []byte("tags:\n  - before\n")})
	require.NoError(t, err)
	original, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	require.NotEmpty(t, original.ContentHash())
	require.NotEmpty(t, original.MetaHash())

	// Meta-only edit under the meta token: content token unchanged.
	metaResults, err := k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: created.ID, Meta: []byte("tags: [after]\n"), HasMeta: true, ExpectedMetaHash: original.MetaHash(),
	}})
	require.NoError(t, err)
	require.Equal(t, original.ContentHash(), metaResults[0].ContentHash)
	require.NotEqual(t, original.MetaHash(), metaResults[0].MetaHash)
	afterMeta, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, original.ContentHash(), afterMeta.ContentHash())
	require.Equal(t, metaResults[0].MetaHash, afterMeta.MetaHash(), "result token matches a fresh read")
	require.Equal(t, metaResults[0].Hash, afterMeta.Hash())
	require.NotEqual(t, original.Hash(), afterMeta.Hash(), "combined hash still covers meta")

	// Content edit with only the pre-meta-edit content token succeeds.
	contentResults, err := k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: created.ID, Content: []byte("# Edited\n"), HasContent: true, ExpectedContentHash: original.ContentHash(),
	}})
	require.NoError(t, err)
	require.Equal(t, afterMeta.MetaHash(), contentResults[0].MetaHash)
	require.NotEqual(t, original.ContentHash(), contentResults[0].ContentHash)

	// A stale content token conflicts and carries the current part tokens.
	current, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: created.ID, Content: []byte("# Stale\n"), HasContent: true, ExpectedContentHash: original.ContentHash(),
	}})
	require.ErrorIs(t, err, keg.ErrConflict)
	var conflict *keg.PreconditionConflictError
	require.True(t, errors.As(err, &conflict))
	require.Equal(t, current.Hash(), conflict.CurrentHash)
	require.Equal(t, current.ContentHash(), conflict.CurrentContentHash)
	require.Equal(t, current.MetaHash(), conflict.CurrentMetaHash)

	// A supplied part without its token is refused, even when the other
	// part's token is present and the combined hash is current.
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: created.ID, Content: []byte("# Both\n"), HasContent: true, Meta: []byte("tags: [both]\n"), HasMeta: true,
		ExpectedContentHash: current.ContentHash(), ExpectedHash: current.Hash(),
	}})
	require.ErrorIs(t, err, keg.ErrPreconditionRequired)

	// The combined token still guards writes that use it (CLI path).
	combined, err := k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: created.ID, Content: []byte("# Combined\n"), HasContent: true, ExpectedHash: current.Hash(),
	}})
	require.NoError(t, err)
	require.NotEmpty(t, combined[0].ContentHash)
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: created.ID, Content: []byte("# Combined stale\n"), HasContent: true, ExpectedHash: current.Hash(),
	}})
	require.ErrorIs(t, err, keg.ErrConflict)
	require.True(t, errors.As(err, &conflict))
	latest, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, latest.ContentHash(), conflict.CurrentContentHash, "combined conflicts also report part tokens")
	require.Equal(t, "# Combined\n", string(latest.Content))
}

func TestLocalKegMetaOnlyReadOmitsContentHash(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	created, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Meta only\n"), Meta: []byte("tags: [x]\n")})
	require.NoError(t, err)
	full, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	views, err := k.ReadNodes(ctx, keg.ReadNodesOptions{NodeIDs: []keg.NodeId{created.ID}, MetaOnly: true})
	require.NoError(t, err)
	require.Len(t, views, 1)
	require.Empty(t, views[0].ContentHash())
	require.Equal(t, full.MetaHash(), views[0].MetaHash())
}
