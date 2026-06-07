package tapper_test

import (
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// testHost is the deterministic hostname pinned in bootstrap tests so the
// machine-keyed local hub is stable across machines and CI.
const testHost = "testhost"

func newBootstrapTap(t *testing.T, fx *sandbox.Sandbox) *tapper.Tap {
	t.Helper()
	require.NoError(t, fx.Runtime().Set("HOSTNAME", testHost))
	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)
	return tap
}

// TestBootstrap_Local sets up only the built-in local filesystem hub: no remote
// URL, fallbackHub points at local.
func TestBootstrap_Local(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap := newBootstrapTap(t, fx)

	res, err := tap.Bootstrap(fx.Context(), tapper.BootstrapOptions{Kind: tapper.BootstrapKindLocal})
	require.NoError(t, err)
	require.True(t, res.Created)
	require.Equal(t, tapper.BootstrapKindLocal, res.Kind)
	require.Equal(t, testHost, res.Hub)
	require.Empty(t, res.HubURL, "local has no remote URL to log in against")
	require.Equal(t, "testuser", res.Namespace)

	cfg, err := tap.ConfigService.UserConfig(false)
	require.NoError(t, err)
	require.Equal(t, testHost, cfg.FallbackHub())
	require.Equal(t, "testuser", cfg.FallbackNamespace())
	hubs := cfg.Hubs()
	require.Contains(t, hubs, testHost)
	require.Equal(t, tapper.HubKindLocal, hubs[testHost].Kind)
	require.Equal(t, tapper.LocalHubName, hubs[testHost].Namespace, "local hub defaults to @local")
	require.NotEmpty(t, hubs[testHost].BasePath)
	require.NotContains(t, hubs, tapper.DefaultHubName, "a fresh local bootstrap should not seed an atlas hub")
}

// TestBootstrap_Cloud targets atlas and is also the default when Kind is empty.
func TestBootstrap_Cloud(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap := newBootstrapTap(t, fx)

	res, err := tap.Bootstrap(fx.Context(), tapper.BootstrapOptions{}) // empty -> cloud
	require.NoError(t, err)
	require.Equal(t, tapper.BootstrapKindCloud, res.Kind)
	require.Equal(t, tapper.DefaultHubName, res.Hub)
	require.Equal(t, tapper.DefaultHubURL, res.HubURL)

	cfg, err := tap.ConfigService.UserConfig(false)
	require.NoError(t, err)
	require.Equal(t, tapper.DefaultHubName, cfg.FallbackHub())
	hubs := cfg.Hubs()
	require.Contains(t, hubs, tapper.DefaultHubName)
	require.Contains(t, hubs, testHost, "local hub is always ensured")
	require.Equal(t, tapper.HubKindRemote, hubs[tapper.DefaultHubName].Kind)
	require.Equal(t, tapper.DefaultHubURL, hubs[tapper.DefaultHubName].URL)
}

// TestBootstrap_Enterprise registers a custom remote endpoint and derives the
// hub name from its host.
func TestBootstrap_Enterprise(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap := newBootstrapTap(t, fx)

	res, err := tap.Bootstrap(fx.Context(), tapper.BootstrapOptions{
		Kind:     tapper.BootstrapKindEnterprise,
		Endpoint: "https://keg.acme.com",
	})
	require.NoError(t, err)
	require.Equal(t, tapper.BootstrapKindEnterprise, res.Kind)
	require.Equal(t, "acme", res.Hub, "hub name derived from endpoint host")
	require.Equal(t, "https://keg.acme.com", res.HubURL)

	cfg, err := tap.ConfigService.UserConfig(false)
	require.NoError(t, err)
	require.Equal(t, "acme", cfg.FallbackHub())
	hubs := cfg.Hubs()
	require.Contains(t, hubs, "acme")
	require.Equal(t, tapper.HubKindRemote, hubs["acme"].Kind)
	require.Equal(t, "https://keg.acme.com", hubs["acme"].URL)
	require.Contains(t, hubs, testHost)
}

// TestBootstrap_Enterprise_SchemeAddedAndHubNameOverride covers a bare host
// (https:// added) and the --hub-name override.
func TestBootstrap_Enterprise_SchemeAddedAndHubNameOverride(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap := newBootstrapTap(t, fx)

	res, err := tap.Bootstrap(fx.Context(), tapper.BootstrapOptions{
		Kind:     tapper.BootstrapKindEnterprise,
		Endpoint: "kegs.example.org", // no scheme
		HubName:  "work",
	})
	require.NoError(t, err)
	require.Equal(t, "work", res.Hub, "explicit --hub-name wins over derivation")
	require.Equal(t, "https://kegs.example.org", res.HubURL, "bare host upgraded to https")

	cfg, err := tap.ConfigService.UserConfig(false)
	require.NoError(t, err)
	require.Equal(t, "https://kegs.example.org", cfg.Hubs()["work"].URL)
}

func TestBootstrap_Enterprise_RequiresEndpoint(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap := newBootstrapTap(t, fx)

	_, err := tap.Bootstrap(fx.Context(), tapper.BootstrapOptions{Kind: tapper.BootstrapKindEnterprise})
	require.Error(t, err)
	require.Contains(t, err.Error(), "endpoint")
}

func TestBootstrap_UnknownKind(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap := newBootstrapTap(t, fx)

	_, err := tap.Bootstrap(fx.Context(), tapper.BootstrapOptions{Kind: "hybrid"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown bootstrap kind")
}

// TestBootstrap_Enterprise_NameCollisionSuffixes confirms a derived name that
// already maps to a different URL gets a numeric suffix rather than clobbering.
func TestBootstrap_Enterprise_NameCollisionSuffixes(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap := newBootstrapTap(t, fx)

	// Seed a config with an existing "acme" hub at a different URL.
	existing := strings.TrimSpace(`
hubs:
  acme: { kind: remote, url: https://old.acme.example }
`) + "\n"
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(existing), 0o644))

	res, err := tap.Bootstrap(fx.Context(), tapper.BootstrapOptions{
		Kind:     tapper.BootstrapKindEnterprise,
		Endpoint: "https://keg.acme.com",
	})
	require.NoError(t, err)
	require.Equal(t, "acme-2", res.Hub, "collision with a different URL should suffix")

	cfg, err := tap.ConfigService.UserConfig(false)
	require.NoError(t, err)
	require.Equal(t, "https://old.acme.example", cfg.Hubs()["acme"].URL, "original hub untouched")
	require.Equal(t, "https://keg.acme.com", cfg.Hubs()["acme-2"].URL)
}

// TestBootstrap_Idempotent_PreservesKegs confirms a re-run refreshes fallbacks
// while leaving user-defined kegs untouched.
func TestBootstrap_Idempotent_PreservesKegs(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap := newBootstrapTap(t, fx)

	existing := strings.TrimSpace(`
fallbackHub: stale
fallbackNamespace: olduser
kegs:
  notes: { hub: atlas, namespace: alice, name: notes }
hubs:
  atlas: { kind: remote, url: https://atlas.foldwise.ai, tokenEnv: ATLAS_API_KEY }
`) + "\n"
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(existing), 0o644))

	res, err := tap.Bootstrap(fx.Context(), tapper.BootstrapOptions{Kind: tapper.BootstrapKindCloud})
	require.NoError(t, err)
	require.False(t, res.Created)
	require.Equal(t, tapper.DefaultHubName, res.Hub)

	cfg, err := tap.ConfigService.UserConfig(false)
	require.NoError(t, err)
	require.Equal(t, tapper.DefaultHubName, cfg.FallbackHub())
	require.Equal(t, "testuser", cfg.FallbackNamespace())

	kegs := cfg.Kegs()
	require.Contains(t, kegs, "notes")
	require.Equal(t, "alice", kegs["notes"].Namespace)
}
