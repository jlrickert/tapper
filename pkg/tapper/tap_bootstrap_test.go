package tapper_test

import (
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func newBootstrapTap(t *testing.T, fx *sandbox.Sandbox) *tapper.Tap {
	t.Helper()
	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)
	return tap
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

	cfg, err := tap.ConfigService.UserConfig()
	require.NoError(t, err)
	require.Equal(t, tapper.DefaultHubName, cfg.FallbackHub())
	require.Empty(t, cfg.FallbackNamespace(), "namespace comes from the hub, not a global fallback")
	hubs := cfg.Hubs()
	require.Contains(t, hubs, tapper.DefaultHubName)
	require.Equal(t, tapper.HubKindRemote, hubs[tapper.DefaultHubName].Kind)
	require.Equal(t, tapper.DefaultHubURL, hubs[tapper.DefaultHubName].URL)
	require.Empty(t, hubs[tapper.DefaultHubName].DefaultNamespace, "cloud hub namespace stays empty until login adopts it")

	require.Empty(t, cfg.Namespaces())
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

	cfg, err := tap.ConfigService.UserConfig()
	require.NoError(t, err)
	require.Equal(t, "acme", cfg.FallbackHub())
	require.Empty(t, cfg.FallbackNamespace(), "namespace comes from the hub, not a global fallback")
	hubs := cfg.Hubs()
	require.Contains(t, hubs, "acme")
	require.Equal(t, tapper.HubKindRemote, hubs["acme"].Kind)
	require.Equal(t, "https://keg.acme.com", hubs["acme"].URL)
	require.Empty(t, hubs["acme"].DefaultNamespace, "enterprise hub namespace stays empty until login adopts it")
	require.Empty(t, cfg.Namespaces())
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

	cfg, err := tap.ConfigService.UserConfig()
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

	cfg, err := tap.ConfigService.UserConfig()
	require.NoError(t, err)
	require.Equal(t, "https://old.acme.example", cfg.Hubs()["acme"].URL, "original hub untouched")
	require.Equal(t, "https://keg.acme.com", cfg.Hubs()["acme-2"].URL)
}

// TestBootstrap_Idempotent_PreservesUserConfig confirms a re-run refreshes
// fallbacks while leaving user-defined kegMap entries untouched.
func TestBootstrap_Idempotent_PreservesUserConfig(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap := newBootstrapTap(t, fx)

	existing := strings.TrimSpace(`
fallbackHub: stale
fallbackNamespace: olduser
vendorFeature:
  enabled: true
kegMap:
  - alias: "@alice/notes"
    pathPrefix: ~/repos/notes
    vendorMapping: keep
hubs:
  atlas:
    kind: remote
    url: https://atlas.foldwise.ai
    tokenEnv: ATLAS_API_KEY
    vendorHub: keep
`) + "\n"
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(existing), 0o644))

	res, err := tap.Bootstrap(fx.Context(), tapper.BootstrapOptions{Kind: tapper.BootstrapKindCloud})
	require.NoError(t, err)
	require.False(t, res.Created)
	require.Equal(t, tapper.DefaultHubName, res.Hub)

	cfg, err := tap.ConfigService.UserConfig()
	require.NoError(t, err)
	require.Equal(t, tapper.DefaultHubName, cfg.FallbackHub())
	// Bootstrap no longer manages fallbackNamespace, so a pre-existing value is
	// left untouched rather than overwritten with the OS user.
	require.Equal(t, "olduser", cfg.FallbackNamespace())
	require.Empty(t, cfg.Namespaces())

	// The user-defined keg-map entry survives the idempotent re-run.
	out, err := cfg.ToYAML()
	require.NoError(t, err)
	require.Contains(t, string(out), "@alice/notes")
	require.Contains(t, string(out), "vendorMapping: keep")
	require.Contains(t, string(out), "vendorHub: keep")
	require.Contains(t, string(out), "vendorFeature:")
}
