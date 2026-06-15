package tapper_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/integrations"
	"github.com/jlrickert/tapper/pkg/tapper"

	// The orient payload reads rendered host bytes from
	// integrations.IntegrationsFS. The test below asserts that the
	// Claude SKILL.md appears at tier 2; the adapter's init() must run
	// so the embedded rendered tree is usable in isolation.
	_ "github.com/jlrickert/tapper/pkg/integrations/adapters"
)

func newOrientTap(t *testing.T) *tapper.Tap {
	t.Helper()
	sb := sandbox.NewSandbox(t, &sandbox.Options{
		Home: "/home/testuser",
		User: "testuser",
	})
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	return tap
}

func TestTap_Orient_Tier0IsBoundedAndHostless(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)
	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{Tier: 0})
	require.NoError(t, err)
	require.Contains(t, payload, "tier 0")
	require.Contains(t, payload, "Rules:")
	require.NotContains(t, payload, "## Host:")
	require.NotContains(t, payload, "## Linking conventions")
	require.Less(t, len(payload), 2048, "tier-0 payload should stay bounded")
}

func TestTap_Orient_Tier2ClaudeIncludesSKILLBytes(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)
	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		Tier: 2,
		Host: "claude",
	})
	require.NoError(t, err)
	want, err := fs.ReadFile(integrations.IntegrationsFS, "rendered/claude/skills/tapper/SKILL.md")
	require.NoError(t, err)
	require.Contains(t, payload, "## Host: claude")
	require.Contains(t, payload, strings.TrimRight(string(want), "\n"))
}

func TestTap_Orient_UnknownHostReturnsError(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)
	_, err := tap.Orient(context.Background(), tapper.OrientOptions{
		Tier: 2,
		Host: "not-a-host",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown host")
}

func TestTap_Orient_TierClampsToBounds(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)

	high, err := tap.Orient(context.Background(), tapper.OrientOptions{Tier: 99})
	require.NoError(t, err)
	require.Contains(t, high, "tier 2")

	low, err := tap.Orient(context.Background(), tapper.OrientOptions{Tier: -5})
	require.NoError(t, err)
	require.Contains(t, low, "tier 0")
}

func TestTap_Orient_UnknownFlightEmitsNote(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)
	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		Tier: 1,
		KegTargetOptions: tapper.KegTargetOptions{
			Flight: "f-demo",
		},
	})
	require.NoError(t, err, "orient must never hard-fail on an unknown flight")
	require.Contains(t, payload, "## Flight `f-demo`")
	require.Contains(t, payload, "flight not found or carries no instructions")
}

func TestTap_Orient_FlightInjectsInstructions(t *testing.T) {
	t.Parallel()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	require.NoError(t, sb.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: sb.Runtime()})
	require.NoError(t, err)

	require.NoError(t, sb.Runtime().AtomicWriteFile(tap.PathService.UserConfig(),
		[]byte("hubs:\n  home:\n    kind: local\n    defaultNamespace: local\n    basePath: /home/testuser/kegs\n"), 0o644))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/kegs/flights.d/backend.yaml",
		[]byte("title: Backend\nallowedKegs: [personal]\ninstructions: Touch only backend kegs.\n"), 0o644))

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		Tier:             1,
		KegTargetOptions: tapper.KegTargetOptions{Flight: "backend"},
	})
	require.NoError(t, err)
	require.Contains(t, payload, "## Flight `@local/+backend`")
	require.Contains(t, payload, "Touch only backend kegs.")
	require.Contains(t, payload, "personal")
}

func TestTap_Orient_FlightAtTier0IsIgnored(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)
	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		Tier: 0,
		KegTargetOptions: tapper.KegTargetOptions{
			Flight: "f-demo",
		},
	})
	require.NoError(t, err)
	require.NotContains(t, payload, "Flight")
}

// TestTap_Orient_ActiveKeg_NoneConfigured covers the bootstrap case:
// a fresh sandbox with no kegs anywhere on disk. The active-keg line
// must surface a directed hint that names the next concrete step
// (`tap init`) instead of the previous "(auto-detect from working
// directory)" placeholder, which described mechanism without telling
// the user how to advance.
func TestTap_Orient_ActiveKeg_NoneConfigured(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)
	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{Tier: 0})
	require.NoError(t, err)
	require.Contains(t, payload, "Active keg: (none configured; run `tap init` to register one)")
	require.NotContains(t, payload, "auto-detect from working directory")
}

// TestTap_Orient_ActiveKeg_AliasResolutionFromCwd covers the common
// case: a kegMap entry whose pathPrefix matches the working directory
// resolves the keg from cwd. The keg lives on the local hub, so the
// resolved target is a bare file backend with no keg name; the active-keg
// line surfaces the path-free backend label with a "no alias" suffix and
// never leaks the underlying filesystem location.
func TestTap_Orient_ActiveKeg_AliasResolutionFromCwd(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	root := "/home/testuser/work"
	require.NoError(t, fx.Runtime().Mkdir(root, 0o755, true))
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{Root: root, Runtime: fx.Runtime()})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.UserConfig()), 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(`fallbackKeg: notes
fallbackNamespace: local
kegMap:
  - alias: notes
    pathPrefix: ~/work
hubs:
  home:
    kind: local
    basePath: ~/Documents/kegs
`), 0o644))
	require.NoError(t, fx.Runtime().Mkdir("/home/testuser/Documents/kegs/@local/notes", 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile("/home/testuser/Documents/kegs/@local/notes/keg", []byte(""), 0o644))

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{Tier: 0})
	require.NoError(t, err)
	// No explicit --keg: resolution succeeds to the local-hub file backend, but
	// a local file target carries no keg name, so the active-keg line surfaces
	// the backend with a "no alias" suffix and never leaks the filesystem path.
	require.Contains(t, payload, "Active keg: (file-backed; no alias)")
	require.NotContains(t, payload, "Documents/kegs/notes")
}

// TestTap_Orient_ActiveKeg_NoAliasFallback covers a project-local keg
// resolved from the working directory but not registered under any
// alias in tap config — the same shape the `keg` CLI hits via its
// ForceProjectResolution profile, and what `tap orient --project`
// produces. The active-keg line surfaces the backend label with a
// "; no alias" suffix so the user knows the keg works without `--keg`
// but cannot be referenced by name elsewhere. The filesystem path is
// intentionally omitted — `tap info` is the dedicated locator.
func TestTap_Orient_ActiveKeg_NoAliasFallback(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	root := "/home/testuser/loose"
	kegDir := root + "/kegs/loose"
	require.NoError(t, fx.Runtime().Mkdir(kegDir, 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(kegDir+"/keg", []byte(""), 0o644))
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{Root: root, Runtime: fx.Runtime()})
	require.NoError(t, err)

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		Tier:             0,
		KegTargetOptions: tapper.KegTargetOptions{Project: true},
	})
	require.NoError(t, err)
	require.Contains(t, payload, "Active keg:")
	require.Contains(t, payload, "(file-backed; no alias)")
	require.NotContains(t, payload, "loose/kegs/loose")
}

// TestTap_Orient_ActiveKeg_ExplicitOverride confirms that an explicit
// keg passed through OrientOptions wins over auto-resolution from cwd.
// This mirrors how every other tap command treats --keg: explicit beats
// implicit. The active-keg line must reflect what the next call would
// hit, not what cwd would have suggested.
func TestTap_Orient_ActiveKeg_ExplicitOverride(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	root := "/home/testuser/work"
	require.NoError(t, fx.Runtime().Mkdir(root, 0o755, true))
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{Root: root, Runtime: fx.Runtime()})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.UserConfig()), 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(`fallbackNamespace: local
hubs:
  home:
    kind: local
    basePath: ~/Documents/kegs
`), 0o644))
	for _, dir := range []string{"/home/testuser/Documents/kegs/@local/archive", "/home/testuser/Documents/kegs/@local/notes"} {
		require.NoError(t, fx.Runtime().Mkdir(dir, 0o755, true))
		require.NoError(t, fx.Runtime().AtomicWriteFile(dir+"/keg", []byte(""), 0o644))
	}

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		Tier:             0,
		KegTargetOptions: tapper.KegTargetOptions{Keg: "archive"},
	})
	require.NoError(t, err)
	require.Contains(t, payload, "Active keg: `archive` (file-backed)")
	require.NotContains(t, payload, "Documents/kegs/archive")
	require.NotContains(t, payload, "notes")
}

func TestTap_OrientableHosts_IsSortedAndIncludesClaude(t *testing.T) {
	t.Parallel()
	hosts := tapper.OrientableHosts()
	require.NotEmpty(t, hosts)
	require.Contains(t, hosts, "claude")
	for i := 1; i < len(hosts); i++ {
		require.LessOrEqual(t, hosts[i-1], hosts[i], "OrientableHosts must be sorted")
	}
}
