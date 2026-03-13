package keg_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// -- FsRepo RepositoryLock tests --

func TestFsRepo_AcquireAndReleaseLock(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	tmp := t.TempDir()
	r := keg.NewFsRepo(tmp, fx.Runtime())
	id := keg.NodeId{ID: 100}

	token, err := r.AcquireLock(ctx, id)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	// Status should show an active lock.
	info, err := r.LockStatus(ctx, id)
	require.NoError(t, err)
	require.Equal(t, token, info.Token)
	require.NotZero(t, info.AcquiredAt)

	// Release with correct token.
	require.NoError(t, r.ReleaseLock(ctx, id, token))

	// Status should now be empty.
	info, err = r.LockStatus(ctx, id)
	require.NoError(t, err)
	require.Empty(t, info.Token)
}

func TestFsRepo_ReleaseLockTokenMismatch(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	tmp := t.TempDir()
	r := keg.NewFsRepo(tmp, fx.Runtime())
	id := keg.NodeId{ID: 101}

	_, err := r.AcquireLock(ctx, id)
	require.NoError(t, err)

	err = r.ReleaseLock(ctx, id, "wrong-token")
	require.ErrorIs(t, err, keg.ErrLockTokenMismatch)
}

func TestFsRepo_ReleaseLockNotLocked(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	tmp := t.TempDir()
	r := keg.NewFsRepo(tmp, fx.Runtime())
	id := keg.NodeId{ID: 102}

	err := r.ReleaseLock(ctx, id, "any-token")
	require.ErrorIs(t, err, keg.ErrNotLocked)
}

func TestFsRepo_AcquireLockContention(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	tmp := t.TempDir()
	r := keg.NewFsRepo(tmp, fx.Runtime())
	id := keg.NodeId{ID: 103}

	// First acquire succeeds.
	_, err := r.AcquireLock(ctx, id)
	require.NoError(t, err)

	// Second acquire with short timeout should fail.
	lockCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	_, err = r.AcquireLock(lockCtx, id)
	require.ErrorIs(t, err, keg.ErrLockTimeout)
}

func TestFsRepo_ForceReleaseLock(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	tmp := t.TempDir()
	r := keg.NewFsRepo(tmp, fx.Runtime())
	id := keg.NodeId{ID: 104}

	_, err := r.AcquireLock(ctx, id)
	require.NoError(t, err)

	// Force release without knowing the token.
	require.NoError(t, r.ForceReleaseLock(ctx, id))

	// Lock should now be available.
	token, err := r.AcquireLock(ctx, id)
	require.NoError(t, err)
	require.NotEmpty(t, token)
}

func TestFsRepo_ForceReleaseLockNotLocked(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	tmp := t.TempDir()
	r := keg.NewFsRepo(tmp, fx.Runtime())
	id := keg.NodeId{ID: 105}

	// Force release on an unlocked node is a no-op.
	require.NoError(t, r.ForceReleaseLock(ctx, id))
}

func TestFsRepo_CrossLockDoesNotInterfereWithNodeLock(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	tmp := t.TempDir()
	r := keg.NewFsRepo(tmp, fx.Runtime())
	id := keg.NodeId{ID: 106}

	// Acquire a cross-process lock.
	token, err := r.AcquireLock(ctx, id)
	require.NoError(t, err)

	// WithNodeLock (process-scoped) should still work.
	err = r.WithNodeLock(ctx, id, func(context.Context) error {
		return nil
	})
	require.NoError(t, err)

	// Cross-process lock should still be held.
	info, err := r.LockStatus(ctx, id)
	require.NoError(t, err)
	require.Equal(t, token, info.Token)

	// Release cross-process lock.
	require.NoError(t, r.ReleaseLock(ctx, id, token))

	// Verify the cross-lock directory is gone but the process-lock dir is
	// also gone (WithNodeLock cleaned up after itself).
	nodeDir := filepath.Join(tmp, id.Path())
	_, err = os.Stat(filepath.Join(nodeDir, keg.KegLockFile))
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(nodeDir, keg.KegCrossLockFile))
	require.True(t, os.IsNotExist(err))
}

// -- MemoryRepo RepositoryLock tests --

func TestMemoryRepo_AcquireAndReleaseLock(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	r := keg.NewMemoryRepo(fx.Runtime())
	id := keg.NodeId{ID: 200}

	token, err := r.AcquireLock(ctx, id)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	info, err := r.LockStatus(ctx, id)
	require.NoError(t, err)
	require.Equal(t, token, info.Token)

	require.NoError(t, r.ReleaseLock(ctx, id, token))

	info, err = r.LockStatus(ctx, id)
	require.NoError(t, err)
	require.Empty(t, info.Token)
}

func TestMemoryRepo_ReleaseLockTokenMismatch(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	r := keg.NewMemoryRepo(fx.Runtime())
	id := keg.NodeId{ID: 201}

	_, err := r.AcquireLock(ctx, id)
	require.NoError(t, err)

	err = r.ReleaseLock(ctx, id, "wrong-token")
	require.ErrorIs(t, err, keg.ErrLockTokenMismatch)
}

func TestMemoryRepo_ReleaseLockNotLocked(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	r := keg.NewMemoryRepo(fx.Runtime())
	id := keg.NodeId{ID: 202}

	err := r.ReleaseLock(ctx, id, "any-token")
	require.ErrorIs(t, err, keg.ErrNotLocked)
}

func TestMemoryRepo_AcquireLockContention(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	r := keg.NewMemoryRepo(fx.Runtime())
	id := keg.NodeId{ID: 203}

	_, err := r.AcquireLock(ctx, id)
	require.NoError(t, err)

	lockCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	_, err = r.AcquireLock(lockCtx, id)
	require.ErrorIs(t, err, keg.ErrLockTimeout)
}

func TestMemoryRepo_ForceReleaseLock(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	r := keg.NewMemoryRepo(fx.Runtime())
	id := keg.NodeId{ID: 204}

	_, err := r.AcquireLock(ctx, id)
	require.NoError(t, err)

	require.NoError(t, r.ForceReleaseLock(ctx, id))

	token, err := r.AcquireLock(ctx, id)
	require.NoError(t, err)
	require.NotEmpty(t, token)
}

func TestMemoryRepo_CrossLockDoesNotInterfereWithNodeLock(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()

	r := keg.NewMemoryRepo(fx.Runtime())
	id := keg.NodeId{ID: 205}

	token, err := r.AcquireLock(ctx, id)
	require.NoError(t, err)

	err = r.WithNodeLock(ctx, id, func(context.Context) error {
		return nil
	})
	require.NoError(t, err)

	info, err := r.LockStatus(ctx, id)
	require.NoError(t, err)
	require.Equal(t, token, info.Token)

	require.NoError(t, r.ReleaseLock(ctx, id, token))
}

// -- LockInfo.IsStale tests --

func TestLockInfo_IsStale(t *testing.T) {
	t.Parallel()
	now := time.Now()

	info := keg.LockInfo{
		Token:      "test-token",
		AcquiredAt: now.Add(-10 * time.Minute),
		TTLSeconds: 300, // 5 minutes
	}
	require.True(t, info.IsStale(now))

	info.AcquiredAt = now.Add(-1 * time.Minute)
	require.False(t, info.IsStale(now))
}

func TestLockInfo_IsStaleEmptyToken(t *testing.T) {
	t.Parallel()
	require.True(t, keg.LockInfo{}.IsStale(time.Now()))
}
