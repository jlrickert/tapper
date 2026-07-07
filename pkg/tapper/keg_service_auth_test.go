package tapper_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// TestKegService_Resolve_ThreadsAuthStoreToken confirms Phase 5 wiring:
// when `tap auth login` has written a token for a hub and a later
// command resolves a remote keg pointing at that hub with no TokenEnv
// or inline Token, KegService must feed the stored token into the
// resulting RemoteKeg. Verifying RemoteKeg.Token directly keeps the test
// hermetic — no httptest server needed to observe the observable.
func TestKegService_Resolve_ThreadsAuthStoreToken(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)
	root := "/home/testuser/project"
	require.NoError(t, fx.Runtime().Mkdir(root, 0o755, true))
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	const hubURL = "https://hub.example.com"
	const hubToken = "seeded-access-token"

	store := &tapper.AuthStore{}
	store.Set(tapper.CanonicalHubURL(hubURL), tapper.AuthEntry{AccessToken: hubToken})
	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), tap.PathService.AuthStorePath()))

	userCfg := fmt.Sprintf(`defaultKeg: "@me/demo"
namespaces:
  me: { hub: example }
hubs:
  example:
    kind: remote
    url: %s
`, hubURL)
	require.NoError(t, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.UserConfig()), 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))

	k, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
		Root: root,
		Keg:  "@me/demo",
	})
	require.NoError(t, err)
	require.NotNil(t, k)

	remote, ok := k.(*keg.RemoteKeg)
	require.True(t, ok, "expected *RemoteKeg, got %T", k)
	require.Equal(t, hubToken, remote.Token(), "auth store token should flow into RemoteKeg")
}

// TestKegService_Resolve_CachedKegRefreshesMidSession reproduces the
// long-running MCP server failure mode: a keg resolved once and memoized in
// the service cache outlives its 15-minute access token. The cached
// RemoteKeg's per-request token source must re-resolve — and thereby
// refresh — the token when the clock passes expiry, instead of pinning the
// token from construction time.
func TestKegService_Resolve_CachedKegRefreshesMidSession(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)
	root := "/home/testuser/project"
	require.NoError(t, fx.Runtime().Mkdir(root, 0o755, true))
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	srv := rotatingTokenHub(t, nil)

	// Seed a token that is still fresh (expires in 10m) with a refresh
	// token pointing at the rotating hub.
	now := fx.Runtime().Clock().Now()
	store := &tapper.AuthStore{}
	store.Set(tapper.CanonicalHubURL(srv.URL), tapper.AuthEntry{
		AccessToken:   "thub_freshseed00",
		TokenType:     "Bearer",
		ExpiresAt:     now.Add(10 * time.Minute),
		RefreshToken:  "rt-old",
		ClientID:      "tapper-cli",
		TokenEndpoint: srv.URL,
	})
	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), tap.PathService.AuthStorePath()))

	userCfg := fmt.Sprintf(`defaultKeg: "@me/demo"
namespaces:
  me: { hub: example }
hubs:
  example:
    kind: remote
    url: %s
`, srv.URL)
	require.NoError(t, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.UserConfig()), 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))

	k, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
		Root: root,
		Keg:  "@me/demo",
	})
	require.NoError(t, err)
	remote, ok := k.(*keg.RemoteKeg)
	require.True(t, ok)

	// Inside the token's lifetime the seeded token is used as-is.
	require.Equal(t, "thub_freshseed00", remote.Token())

	// Resolve again: the cache must return the same instance (that's the
	// long-lived keg whose token would previously go stale).
	k2, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
		Root: root,
		Keg:  "@me/demo",
	})
	require.NoError(t, err)
	require.Same(t, k, k2, "expected the memoized keg instance")

	// 16 minutes later the access token has expired. The cached keg must
	// come back with the refreshed token, not the stale one.
	fx.Advance(16 * time.Minute)
	require.Equal(t, "thub_refreshednew99", remote.Token(),
		"cached keg must pick up the refreshed token after expiry")
}

// TestKegService_Resolve_TokenEnvStillWinsOverAuthStore guards the
// precedence contract at the service layer: even with a matching entry
// in the auth store, an explicit TokenEnv takes priority.
func TestKegService_Resolve_TokenEnvStillWinsOverAuthStore(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t)
	root := "/home/testuser/project"
	require.NoError(t, fx.Runtime().Mkdir(root, 0o755, true))
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	const hubURL = "https://hub.example.com"
	store := &tapper.AuthStore{}
	store.Set(tapper.CanonicalHubURL(hubURL), tapper.AuthEntry{AccessToken: "store-token"})
	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), tap.PathService.AuthStorePath()))

	require.NoError(t, fx.Runtime().Set("HUB_TOKEN", "env-token"))

	userCfg := fmt.Sprintf(`defaultKeg: "@me/demo"
namespaces:
  me: { hub: example }
hubs:
  example:
    kind: remote
    url: %s
    tokenEnv: HUB_TOKEN
`, hubURL)
	require.NoError(t, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.UserConfig()), 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))

	k, err := tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
		Root: root,
		Keg:  "@me/demo",
	})
	require.NoError(t, err)

	remote, ok := k.(*keg.RemoteKeg)
	require.True(t, ok)
	require.Equal(t, "env-token", remote.Token(), "TokenEnv must win over auth store")
}
