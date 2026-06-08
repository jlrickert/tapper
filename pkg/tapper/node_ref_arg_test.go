package tapper_test

import (
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// twoLocalKegs materializes a local filesystem hub with two kegs laid out at
// <basePath>/@local/<name>. The hub's default namespace is local, so a bare keg
// name N resolves to @local/N. Each keg gets one node whose body names the keg,
// so a reader can tell which keg a node argument actually resolved to.
// "current" is the keg a command resolves for a bare id (via --keg current);
// "other" is the redirect target a cross-keg ref must reach.
//
// Returns the Tap plus the node ids created in each keg.
func twoLocalKegs(t *testing.T, fx *sandbox.Sandbox) (tap *tapper.Tap, currentID, otherID keg.NodeId) {
	t.Helper()
	rt := fx.Runtime()
	ctx := fx.Context()

	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: rt})
	require.NoError(t, err)

	basePath := filepath.Join(fx.GetJail(), "kegs")
	userCfg := `defaultKeg: current
hubs:
  home:
    kind: local
    namespace: local
    basePath: ` + basePath + `
`
	require.NoError(t, rt.Mkdir(tap.PathService.ConfigRoot, 0o755, true))
	require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))

	makeNode := func(name, body string) keg.NodeId {
		dir := filepath.Join(basePath, "@local", name)
		require.NoError(t, rt.Mkdir(dir, 0o755, true))
		k, err := keg.NewKegFromTarget(ctx, keg.NewFile(dir), rt)
		require.NoError(t, err)
		require.NoError(t, k.Init(ctx))
		id, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(body)})
		require.NoError(t, err)
		return id
	}

	currentID = makeNode("current", "I live in the CURRENT keg.\n")
	otherID = makeNode("other", "I live in the OTHER keg.\n")
	return tap, currentID, otherID
}

// TestResolveNodeArg_QualifiedRefRedirectsToOtherKeg verifies that a
// "keg:@local/<keg>/<id>" argument passed to cat operates on the named keg, not
// the --keg-resolved current keg. This is the redirect through ResolveNodeRef.
func TestResolveNodeArg_QualifiedRefRedirectsToOtherKeg(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	tap, _, otherID := twoLocalKegs(t, fx)

	out, err := tap.Cat(fx.Context(), tapper.CatOptions{
		NodeIDs:          []string{"keg:@local/other/" + otherID.PathNumeric()},
		KegTargetOptions: tapper.KegTargetOptions{Keg: "current"},
		ContentOnly:      true,
	})
	require.NoError(t, err)
	require.Contains(t, out, "OTHER keg")
	require.NotContains(t, out, "CURRENT keg")
}

// TestResolveNodeArg_AliasRefRedirectsToOtherKeg verifies that a
// "keg:<alias>/<id>" argument resolves the alias through the tap-config kegs map
// and operates on that keg rather than the current keg.
func TestResolveNodeArg_AliasRefRedirectsToOtherKeg(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	tap, _, otherID := twoLocalKegs(t, fx)

	out, err := tap.Cat(fx.Context(), tapper.CatOptions{
		NodeIDs:          []string{"keg:other/" + otherID.PathNumeric()},
		KegTargetOptions: tapper.KegTargetOptions{Keg: "current"},
		ContentOnly:      true,
	})
	require.NoError(t, err)
	require.Contains(t, out, "OTHER keg")
	require.NotContains(t, out, "CURRENT keg")
}

// TestResolveNodeArg_BareIDStaysOnCurrentKeg pins the unchanged behavior: a bare
// id reads from the resolved current keg, never the other keg.
func TestResolveNodeArg_BareIDStaysOnCurrentKeg(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	tap, currentID, _ := twoLocalKegs(t, fx)

	out, err := tap.Cat(fx.Context(), tapper.CatOptions{
		NodeIDs:          []string{currentID.PathNumeric()},
		KegTargetOptions: tapper.KegTargetOptions{Keg: "current"},
		ContentOnly:      true,
	})
	require.NoError(t, err)
	require.Contains(t, out, "CURRENT keg")
	require.NotContains(t, out, "OTHER keg")
}

// TestResolveNodeArg_StatsRedirects checks that the redirect is wired at the Tap
// layer broadly, not just in cat: Stats on a qualified ref must read the other
// keg's node (and not error as if the id were missing in the current keg).
func TestResolveNodeArg_StatsRedirects(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	tap, _, otherID := twoLocalKegs(t, fx)

	out, err := tap.Stats(fx.Context(), tapper.StatsOptions{
		NodeID:           "keg:other/" + otherID.PathNumeric(),
		KegTargetOptions: tapper.KegTargetOptions{Keg: "current"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, out)
}
