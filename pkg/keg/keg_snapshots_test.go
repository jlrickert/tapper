package keg_test

import (
	"strings"
	"testing"

	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

type repoWithoutSnapshots struct {
	kegpkg.Repository
}

func TestKegSnapshotsRestoreSkipsSchemaEnforcement(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := kegpkg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)

	id, err := k.Create(kegpkg.WithValidationMode(ctx, kegpkg.ValidationModeOff), &kegpkg.CreateOptions{
		Body:  []byte("# Historical Task\n"),
		Attrs: map[string]any{"type": "task"},
	})
	require.NoError(t, err)
	snap, err := k.AppendSnapshot(ctx, id.ID, "before schema")
	require.NoError(t, err)
	require.NoError(t, k.CreateSchema(ctx, "task", []byte(`type: task
meta:
  type: object
  required: ["type"]
  properties:
    type:
      const: task
markdown:
  requireTitle: true
  sections:
    - heading: Required
      level: 2
      required: true
`)))
	require.NoError(t, k.SetContent(ctx, id.ID, []byte("# Current Task\n\n## Required\n\nPresent.\n")))

	blockCtx := kegpkg.WithValidationMode(ctx, kegpkg.ValidationModeBlock)
	require.NoError(t, k.RestoreSnapshot(blockCtx, id.ID, snap.ID))
	content, err := k.GetContent(ctx, id.ID)
	require.NoError(t, err)
	require.NotContains(t, string(content), "## Required")
	require.True(t, strings.Contains(string(content), "Historical Task"))
	history, err := k.ListSnapshots(ctx, id.ID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(history), 2, "restore should retain snapshot history")
}

func TestKegSnapshots_ReturnErrNotSupportedWithoutSnapshotBackend(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)
	base := newTestMemoryRepo(fx.Runtime())
	repo := &repoWithoutSnapshots{Repository: base}
	k := kegpkg.NewLocalKeg(repo, fx.Runtime())

	initNonStrictTestKeg(t, k, fx.Context())

	id, err := k.Create(fx.Context(), &kegpkg.CreateOptions{Title: "Snapshot Target"})
	require.NoError(t, err)

	_, err = k.AppendSnapshot(fx.Context(), id.ID, "before unsupported")
	require.ErrorIs(t, err, kegpkg.ErrNotSupported)

	_, err = k.ListSnapshots(fx.Context(), id.ID)
	require.ErrorIs(t, err, kegpkg.ErrNotSupported)

	_, err = k.ReadContentAt(fx.Context(), id.ID, 1)
	require.ErrorIs(t, err, kegpkg.ErrNotSupported)

	err = k.RestoreSnapshot(fx.Context(), id.ID, 1)
	require.ErrorIs(t, err, kegpkg.ErrNotSupported)
}
