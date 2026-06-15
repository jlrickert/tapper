package tapper_test

import (
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// localUserConfig is what `tap bootstrap --kind local` leaves behind: one local
// hub plus fallbackHub + fallbackKeg, and NO global default/fallback namespace —
// so a bare keg name must infer @local from the hub.
const localUserConfig = "fallbackHub: home\n" +
	"fallbackKeg: private\n" +
	"namespaces:\n  local:\n    hub: home\n" +
	"hubs:\n  home:\n    kind: local\n    defaultNamespace: local\n    basePath: /home/testuser/kegs\n"

// TestNamespaceInference_LocalBareName guards the fix for "namespace is not being
// inferred": a bare keg name (`private`) on a local fallback hub must resolve and
// display as `@local/private` at both the backend (ResolveRef) and the
// display/identity layer (UseStatus → resolveIdentity).
func TestNamespaceInference_LocalBareName(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(localUserConfig), 0o644))

	// Backend resolution: a bare name infers @local on the local hub.
	cfg, err := tap.ConfigService.Config(true)
	require.NoError(t, err)
	target, err := cfg.ResolveRef(fx.Runtime(), tapper.KegRef{Name: "private"})
	require.NoError(t, err)
	require.Contains(t, target.Path(), "@local/private",
		"a bare name must infer the @local namespace on a local hub")

	// Display resolution: `tap use` shows the inferred @local/private, not a bare
	// name with an empty namespace.
	out, err := tap.UseStatus(fx.Context(), tapper.KegTargetOptions{})
	require.NoError(t, err)
	require.Contains(t, out, "@local/private")
	require.Contains(t, out, "namespace: local")
}
