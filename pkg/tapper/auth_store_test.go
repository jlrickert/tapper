package tapper_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// osChmod is a test-only helper that changes file mode on a
// sandbox-resolved absolute host path. We reach around the runtime here
// because cli-toolkit's Runtime doesn't expose Chmod and we need to
// simulate out-of-band permission drift to exercise the
// "Save restores 0600" invariant. The spec explicitly authorizes this
// use of os.Chmod.
func osChmod(t *testing.T, path string, mode os.FileMode) error {
	t.Helper()
	return os.Chmod(path, mode)
}

// authStorePath returns a deterministic path inside the sandbox that
// mimics the real StateRoot/auth.yaml layout but doesn't require
// bootstrapping a full Tap — we're unit-testing the store in isolation.
func authStorePath(t *testing.T, fx *sandbox.Sandbox) string {
	t.Helper()
	abs, err := fx.AbsPath("/home/testuser/.local/state/tapper/auth.yaml")
	require.NoError(t, err)
	return abs
}

func TestAuthStore_LoadAuthStore_MissingFile_ReturnsEmptyStore(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)
	path := authStorePath(t, fx)

	store, err := tapper.LoadAuthStore(fx.Context(), fx.Runtime(), path)
	require.NoError(t, err)
	require.NotNil(t, store)
	require.True(t, store.IsEmpty())
	require.Empty(t, store.Hubs())
}

func TestAuthStore_ParseAuthStore_EmptyBytes_ReturnsEmptyStore(t *testing.T) {
	t.Parallel()

	store, err := tapper.ParseAuthStore([]byte{})
	require.NoError(t, err)
	require.NotNil(t, store)
	require.True(t, store.IsEmpty())

	store2, err := tapper.ParseAuthStore([]byte("\n"))
	require.NoError(t, err)
	require.NotNil(t, store2)
	require.True(t, store2.IsEmpty())
}

func TestAuthStore_Save_WritesFileWithMode0600(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)
	path := authStorePath(t, fx)

	store := &tapper.AuthStore{}
	store.Set("https://hub.example.com", tapper.AuthEntry{
		AccessToken: "t0k3n",
		TokenType:   "Bearer",
		Scope:       "read write",
	})

	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), path))

	info, err := fx.Runtime().Stat(path, false)
	require.NoError(t, err)
	require.Equal(t, "-rw-------", info.Mode().Perm().String())

	// Parent dir exists — and was freshly created by Save, so should be
	// 0700. On pre-existing trees we leave mode alone; that's covered by
	// Save_Overwrite_PreservesMode0600 implicitly.
	dirInfo, err := fx.Runtime().Stat(filepath.Dir(path), false)
	require.NoError(t, err)
	require.True(t, dirInfo.IsDir())
	require.Equal(t, "-rwx------", dirInfo.Mode().Perm().String())
}

func TestAuthStore_Save_Overwrite_PreservesMode0600(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)
	path := authStorePath(t, fx)

	store := &tapper.AuthStore{}
	store.Set("https://hub.example.com", tapper.AuthEntry{AccessToken: "t1"})
	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), path))

	// Simulate drift: an admin or misbehaving script re-permissions the
	// file. Rewriting must restore 0600. We need the real host path (not
	// the sandbox-virtual path) so os.Chmod can find the file; sandbox
	// ResolvePath returns virtual. Compose jail + virtual manually.
	virtual, err := fx.ResolvePath("/home/testuser/.local/state/tapper/auth.yaml")
	require.NoError(t, err)
	hostPath := filepath.Join(fx.GetJail(), virtual)
	require.NoError(t, osChmod(t, hostPath, 0o644))

	// Verify drift took effect before we rewrite.
	info, err := fx.Runtime().Stat(path, false)
	require.NoError(t, err)
	require.Equal(t, "-rw-r--r--", info.Mode().Perm().String())

	// Second save should restore 0600.
	store.Set("https://hub.example.com", tapper.AuthEntry{AccessToken: "t2"})
	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), path))

	info, err = fx.Runtime().Stat(path, false)
	require.NoError(t, err)
	require.Equal(t, "-rw-------", info.Mode().Perm().String())
}

func TestAuthStore_RoundTrip_MultiHub(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)
	path := authStorePath(t, fx)

	// Timestamps round-trip through YAML as UTC; normalizing up-front
	// keeps equality checks from getting wedged on location pointers.
	exp := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)

	orig := &tapper.AuthStore{}
	orig.Set("https://a.example.com", tapper.AuthEntry{
		AccessToken:   "a-token",
		TokenType:     "Bearer",
		ExpiresAt:     exp,
		Scope:         "read",
		RefreshToken:  "a-refresh",
		ClientID:      "tapper-cli",
		TokenEndpoint: "https://a.example.com/oauth/token",
	})
	orig.Set("https://b.example.com", tapper.AuthEntry{
		AccessToken: "b-token",
	})

	require.NoError(t, orig.Save(fx.Context(), fx.Runtime(), path))

	loaded, err := tapper.LoadAuthStore(fx.Context(), fx.Runtime(), path)
	require.NoError(t, err)
	require.Equal(t, []string{"https://a.example.com", "https://b.example.com"}, loaded.Hubs())

	a, ok := loaded.Get("https://a.example.com")
	require.True(t, ok)
	require.Equal(t, "a-token", a.AccessToken)
	require.Equal(t, "Bearer", a.TokenType)
	require.True(t, a.ExpiresAt.Equal(exp))
	require.Equal(t, "read", a.Scope)
	require.Equal(t, "a-refresh", a.RefreshToken)
	require.Equal(t, "tapper-cli", a.ClientID)
	require.Equal(t, "https://a.example.com/oauth/token", a.TokenEndpoint)

	b, ok := loaded.Get("https://b.example.com")
	require.True(t, ok)
	require.Equal(t, "b-token", b.AccessToken)
	require.Empty(t, b.TokenType)
	require.True(t, b.ExpiresAt.IsZero())
	require.Empty(t, b.RefreshToken)
}

func TestAuthStore_Delete_Entry(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)
	path := authStorePath(t, fx)

	store := &tapper.AuthStore{}
	store.Set("https://keep.example.com", tapper.AuthEntry{AccessToken: "k"})
	store.Set("https://drop.example.com", tapper.AuthEntry{AccessToken: "d"})

	require.True(t, store.Delete("https://drop.example.com"))
	require.False(t, store.Delete("https://drop.example.com"), "second delete should report missing")

	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), path))

	loaded, err := tapper.LoadAuthStore(fx.Context(), fx.Runtime(), path)
	require.NoError(t, err)
	require.Equal(t, []string{"https://keep.example.com"}, loaded.Hubs())

	_, ok := loaded.Get("https://drop.example.com")
	require.False(t, ok)
}

func TestAuthStore_Save_EmptyStore_RemovesFile(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)
	path := authStorePath(t, fx)

	store := &tapper.AuthStore{}
	store.Set("https://only.example.com", tapper.AuthEntry{AccessToken: "x"})
	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), path))

	// File exists after first save.
	_, err := fx.Runtime().Stat(path, false)
	require.NoError(t, err)

	require.True(t, store.Delete("https://only.example.com"))
	require.True(t, store.IsEmpty())
	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), path))

	// File is gone.
	_, err = fx.Runtime().Stat(path, false)
	require.Error(t, err, "file should have been removed on empty Save")

	// Saving an already-empty store with no file present must also be a
	// no-op success, not an error.
	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), path))
}

func TestAuthStore_Get_ReturnsCopy(t *testing.T) {
	t.Parallel()

	store := &tapper.AuthStore{}
	store.Set("https://hub.example.com", tapper.AuthEntry{AccessToken: "original"})

	entry, ok := store.Get("https://hub.example.com")
	require.True(t, ok)
	entry.AccessToken = "mutated"

	// Stored value must be untouched by the caller's mutation.
	entry2, ok := store.Get("https://hub.example.com")
	require.True(t, ok)
	require.Equal(t, "original", entry2.AccessToken)
}

func TestAuthStore_NilReceiver_Safe(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)
	path := authStorePath(t, fx)

	var store *tapper.AuthStore // nil on purpose

	require.NotPanics(t, func() {
		_, ok := store.Get("https://hub.example.com")
		require.False(t, ok)

		store.Set("https://hub.example.com", tapper.AuthEntry{AccessToken: "x"})
		require.False(t, store.Delete("https://hub.example.com"))
		require.Equal(t, []string{}, store.Hubs())
		require.True(t, store.IsEmpty())
		require.NoError(t, store.Save(fx.Context(), fx.Runtime(), path))
	})
}

func TestAuthStore_Hubs_Sorted(t *testing.T) {
	t.Parallel()

	store := &tapper.AuthStore{}
	store.Set("https://zeta.example.com", tapper.AuthEntry{AccessToken: "z"})
	store.Set("https://alpha.example.com", tapper.AuthEntry{AccessToken: "a"})
	store.Set("https://mu.example.com", tapper.AuthEntry{AccessToken: "m"})

	require.Equal(t, []string{
		"https://alpha.example.com",
		"https://mu.example.com",
		"https://zeta.example.com",
	}, store.Hubs())
}

func TestAuthStore_AuthStorePath_JoinsStateRoot(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)

	ps, err := tapper.NewPathService(fx.Runtime(), "/home/testuser")
	require.NoError(t, err)

	require.Equal(t, filepath.Join(ps.StateRoot, "auth.yaml"), ps.AuthStorePath())
}
