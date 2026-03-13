package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestLock_AcquireAndRelease(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Acquire lock on node 0.
	acquireRes := NewProcess(t, false, "lock", "acquire", "0").
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, acquireRes.Err)
	token := strings.TrimSpace(string(acquireRes.Stdout))
	require.NotEmpty(t, token)

	// Status should show locked.
	statusRes := NewProcess(t, false, "lock", "status", "0").
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, statusRes.Err)
	require.Contains(t, string(statusRes.Stdout), token)

	// Release with correct token.
	releaseRes := NewProcess(t, false, "lock", "release", "0", "--token", token).
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, releaseRes.Err)

	// Status should show unlocked.
	statusRes2 := NewProcess(t, false, "lock", "status", "0").
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, statusRes2.Err)
	require.Contains(t, string(statusRes2.Stdout), "unlocked")
}

func TestLock_ForceRelease(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Acquire lock.
	acquireRes := NewProcess(t, false, "lock", "acquire", "0").
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, acquireRes.Err)

	// Force release without token.
	forceRes := NewProcess(t, false, "lock", "force-release", "0").
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, forceRes.Err)

	// Should be unlocked now.
	statusRes := NewProcess(t, false, "lock", "status", "0").
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, statusRes.Err)
	require.Contains(t, string(statusRes.Stdout), "unlocked")
}

func TestLock_ReleaseTokenMismatch(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Acquire lock.
	acquireRes := NewProcess(t, false, "lock", "acquire", "0").
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, acquireRes.Err)

	// Release with wrong token should fail.
	releaseRes := NewProcess(t, false, "lock", "release", "0", "--token", "wrong").
		Run(fx.Context(), fx.Runtime())
	require.Error(t, releaseRes.Err)
	require.Contains(t, string(releaseRes.Stderr), "mismatch")
}

func TestLock_StatusUnlocked(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Node 0 is not locked.
	res := NewProcess(t, false, "lock", "status", "0").
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "unlocked")
}

func TestEdit_WithCorrectLockToken(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Acquire lock.
	acquireRes := NewProcess(t, false, "lock", "acquire", "0").
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, acquireRes.Err)
	token := strings.TrimSpace(string(acquireRes.Stdout))

	// Edit with correct token should succeed.
	stdin := strings.NewReader("# Edited with lock\n")
	editRes := NewProcess(t, false, "edit", "0", "--lock-token", token).
		RunWithIO(fx.Context(), fx.Runtime(), stdin)
	require.NoError(t, editRes.Err)
}

func TestEdit_WithWrongLockToken(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Acquire lock.
	acquireRes := NewProcess(t, false, "lock", "acquire", "0").
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, acquireRes.Err)

	// Edit with wrong token should fail.
	stdin := strings.NewReader("# Should not work\n")
	editRes := NewProcess(t, false, "edit", "0", "--lock-token", "wrong-token").
		RunWithIO(fx.Context(), fx.Runtime(), stdin)
	require.Error(t, editRes.Err)
	require.Contains(t, string(editRes.Stderr), "mismatch")
}

func TestEdit_WithoutLockToken_NoLockHeld(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Edit without --lock-token when no lock is held should succeed (backward compat).
	stdin := strings.NewReader("# No lock needed\n")
	editRes := NewProcess(t, false, "edit", "0").
		RunWithIO(fx.Context(), fx.Runtime(), stdin)
	require.NoError(t, editRes.Err)
}

func TestMeta_WithCorrectLockToken(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Acquire lock.
	acquireRes := NewProcess(t, false, "lock", "acquire", "0").
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, acquireRes.Err)
	token := strings.TrimSpace(string(acquireRes.Stdout))

	// Meta write with correct token should succeed.
	stdin := strings.NewReader("tags:\n  - locked-edit\n")
	metaRes := NewProcess(t, false, "meta", "0", "--lock-token", token).
		RunWithIO(fx.Context(), fx.Runtime(), stdin)
	require.NoError(t, metaRes.Err)
}

func TestMeta_WithWrongLockToken(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Acquire lock.
	acquireRes := NewProcess(t, false, "lock", "acquire", "0").
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, acquireRes.Err)

	// Meta write with wrong token should fail.
	stdin := strings.NewReader("tags:\n  - should-fail\n")
	metaRes := NewProcess(t, false, "meta", "0", "--lock-token", "wrong-token").
		RunWithIO(fx.Context(), fx.Runtime(), stdin)
	require.Error(t, metaRes.Err)
	require.Contains(t, string(metaRes.Stderr), "mismatch")
}

func TestMeta_ReadIgnoresLockToken(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	// Acquire lock.
	acquireRes := NewProcess(t, false, "lock", "acquire", "0").
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, acquireRes.Err)

	// Meta read (no stdin, no --edit) should succeed even with wrong token
	// because reads don't require lock validation.
	metaRes := NewProcess(t, false, "meta", "0", "--lock-token", "wrong-token").
		Run(fx.Context(), fx.Runtime())
	require.NoError(t, metaRes.Err, "meta read should succeed despite wrong lock token; stderr: %s", string(metaRes.Stderr))
}
