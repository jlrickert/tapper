package tapper_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// TestKegService_Resolve_ThreadsAuthStoreToken confirms Phase 5 wiring:
// when `tap auth login` has written a token for a hub and a later
// command resolves a remote keg pointing at that hub with no TokenEnv
// or inline Token, KegService must feed the stored token into the
// resulting ApiRepo. Verifying ApiRepo.Token directly keeps the test
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

	apiRepo, ok := k.Repo.(*keg.ApiRepo)
	require.True(t, ok, "expected ApiRepo, got %T", k.Repo)
	require.Equal(t, hubToken, apiRepo.Token, "auth store token should flow into ApiRepo")
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

	apiRepo, ok := k.Repo.(*keg.ApiRepo)
	require.True(t, ok)
	require.Equal(t, "env-token", apiRepo.Token, "TokenEnv must win over auth store")
}
