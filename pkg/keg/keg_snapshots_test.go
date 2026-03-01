package keg_test

import (
	"testing"

	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

type repoWithoutSnapshots struct {
	kegpkg.Repository
}

func TestKegSnapshots_ReturnErrNotSupportedWithoutSnapshotBackend(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)
	base := kegpkg.NewMemoryRepo(fx.Runtime())
	repo := &repoWithoutSnapshots{Repository: base}
	k := kegpkg.NewKeg(repo, fx.Runtime())

	require.NoError(t, k.Init(fx.Context()))

	id, err := k.Create(fx.Context(), &kegpkg.CreateOptions{Title: "Snapshot Target"})
	require.NoError(t, err)

	_, err = k.AppendSnapshot(fx.Context(), id, "before unsupported")
	require.ErrorIs(t, err, kegpkg.ErrNotSupported)

	_, err = k.ListSnapshots(fx.Context(), id)
	require.ErrorIs(t, err, kegpkg.ErrNotSupported)

	_, err = k.ReadContentAt(fx.Context(), id, 1)
	require.ErrorIs(t, err, kegpkg.ErrNotSupported)

	err = k.RestoreSnapshot(fx.Context(), id, 1)
	require.ErrorIs(t, err, kegpkg.ErrNotSupported)
}
