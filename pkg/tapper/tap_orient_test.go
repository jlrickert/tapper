package tapper_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/tapper"

	// Register the default integration adapters so IntegrateHosts()
	// produces a non-empty completion list under test.
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

func TestTap_Orient_SharedPayloadStartsWithKegSystem(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{})
	require.NoError(t, err)

	require.True(t, strings.HasPrefix(payload, "# KEG System\n\n"), payload)
	require.Contains(t, payload, "Tapper provides an MCP interface for KEG")
	require.NotContains(t, payload, "CLI")
	require.NotContains(t, payload, "`tap ")
	require.Contains(t, payload, "Rules:")
	require.Contains(t, payload, "## Active KEG")
	require.Contains(t, payload, "## Available KEGs")
	require.Contains(t, payload, "## KEG Instructions")
	require.Contains(t, payload, "## Guidance")
	require.Contains(t, payload, "# Linking conventions")
	guidance := payload[strings.Index(payload, "## Guidance"):]
	require.Contains(t, guidance, "`keg:ALIAS/NODEID`")
	require.Contains(t, guidance, "`keg:@NAMESPACE/ALIAS/NODEID`")
	require.Contains(t, payload, "# Snapshot policy")
	require.NotContains(t, payload, "## Host:")
	require.NotContains(t, strings.ToLower(payload), "tier 0")
	require.NotContains(t, strings.ToLower(payload), "tier 1")
	require.NotContains(t, strings.ToLower(payload), "tier 2")
}

func TestTap_Orient_UnknownFlightEmitsNote(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		KegTargetOptions: tapper.KegTargetOptions{Flight: "f-demo"},
	})
	require.NoError(t, err, "orient must never hard-fail on an unknown flight")
	require.Contains(t, payload, "# KEG System")
	require.Contains(t, payload, "Active flight: `f-demo`")
	require.Contains(t, payload, `Flight "f-demo" is unavailable`)
}

func TestTap_Orient_FlightAndKegInstructionsPrecedeGuidance(t *testing.T) {
	t.Parallel()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	require.NoError(t, sb.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: sb.Runtime()})
	require.NoError(t, err)

	require.NoError(t, sb.Runtime().AtomicWriteFile(tap.PathService.UserConfig(),
		[]byte("hubs:\n  home:\n    kind: local\n    defaultNamespace: local\n    basePath: /home/testuser/kegs\n"), 0o644))
	for _, name := range []string{"personal", "dev"} {
		dir := "/home/testuser/kegs/@local/" + name
		require.NoError(t, sb.Runtime().Mkdir(dir, 0o755, true))
		require.NoError(t, sb.Runtime().AtomicWriteFile(dir+"/keg", []byte("kegv: 2025-07\ntitle: "+name+"\n"), 0o644))
	}
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/kegs/@local/personal/keg",
		[]byte("kegv: 2025-07\ntitle: Personal\ninstructions: |\n  Prefer audited personal-context nodes.\n"), 0o644))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/kegs/flights.d/backend.yaml",
		[]byte("title: Backend\ncover:\n  - namespace: local\n    keg: personal\n    role: viewer\n  - namespace: local\n    keg: dev\n    role: editor\ninstructions: |\n  Touch only backend kegs.\n"), 0o644))

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		KegTargetOptions: tapper.KegTargetOptions{Flight: "backend"},
	})
	require.NoError(t, err)
	require.Contains(t, payload, "| `@local/dev` | editor | home/local | editor |")
	require.Contains(t, payload, "| `@local/personal` | editor | home/local | viewer |")
	require.Contains(t, payload, "## Flight")
	require.Contains(t, payload, "Backend")
	require.Contains(t, payload, "Touch only backend kegs.")
	require.Contains(t, payload, "## KEG Instructions")
	require.Contains(t, payload, "### `@local/personal`")
	require.Contains(t, payload, "Prefer audited personal-context nodes.")

	guidanceAt := strings.Index(payload, "## Guidance")
	require.NotEqual(t, -1, guidanceAt)
	require.Less(t, strings.Index(payload, "Touch only backend kegs."), guidanceAt)
	require.Less(t, strings.Index(payload, "Prefer audited personal-context nodes."), guidanceAt)
}

func TestTap_Orient_UsesPersistedFlightBeforeDefaultKeg(t *testing.T) {
	t.Parallel()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	require.NoError(t, sb.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: sb.Runtime()})
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(`flight: backend
fallbackKeg: personal
hubs:
  home:
    kind: local
    defaultNamespace: local
    basePath: /home/testuser/kegs
`), 0o644))
	for _, name := range []string{"personal", "dev"} {
		dir := "/home/testuser/kegs/@local/" + name
		require.NoError(t, sb.Runtime().Mkdir(dir, 0o755, true))
		require.NoError(t, sb.Runtime().AtomicWriteFile(dir+"/keg", []byte("kegv: 2025-07\ntitle: "+name+"\n"), 0o644))
	}
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/kegs/@local/dev/keg", []byte("kegv: 2025-07\ntitle: dev\ninstructions: |\n  Follow the covered KEG schema.\n"), 0o644))
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/kegs/flights.d/backend.yaml", []byte("title: Backend\ncover:\n  - namespace: local\n    keg: dev\n    role: editor\ninstructions: |\n  Flight instructions win.\n"), 0o644))

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{})
	require.NoError(t, err)
	require.Contains(t, payload, "Active flight: `@local/+backend`")
	require.Contains(t, payload, "Flight instructions win.")
	require.Contains(t, payload, "Follow the covered KEG schema.")
	require.NotContains(t, payload, "| `@local/personal`")
}

func TestTap_Orient_BarePayloadDoesNotInjectDeveloperLifecycle(t *testing.T) {
	t.Parallel()
	payload, err := newOrientTap(t).Orient(context.Background(), tapper.OrientOptions{})
	require.NoError(t, err)
	for _, heading := range []string{"## Plan", "## Code", "## Review", "## Commit"} {
		require.NotContains(t, payload, heading)
	}
}

// TestTap_Orient_ActiveKeg_NoneConfigured covers the bootstrap case:
// a fresh sandbox with no kegs anywhere on disk. The active-keg line reports
// the empty state without directing MCP clients to a compatibility command.
func TestTap_Orient_ActiveKeg_NoneConfigured(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)
	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{})
	require.NoError(t, err)
	require.Contains(t, payload, "Active KEG: (none configured)")
	require.NotContains(t, payload, "auto-detect from working directory")
	require.NotContains(t, payload, "`tap ")
}

func TestTap_Orient_MissingHubAuthenticationIsMCPFirst(t *testing.T) {
	t.Parallel()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(`hubs:
  work:
    kind: remote
    url: https://hub.example.com
`), 0o644))

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{})
	require.NoError(t, err)
	require.Contains(t, payload, `skipped hub "work": hub has no authenticated session for https://hub.example.com`)
	require.NotContains(t, payload, "CLI")
	require.NotContains(t, payload, "`tap ")
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
	require.NoError(t, fx.Runtime().AtomicWriteFile("/home/testuser/Documents/kegs/@local/notes/keg", []byte("kegv: 2025-07\n"), 0o644))

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{})
	require.NoError(t, err)
	require.Contains(t, payload, "Active KEG: (file-backed; no alias)")
	require.NotContains(t, payload, "Documents/kegs/notes")
}

// TestTap_Orient_ActiveKeg_NoAliasFallback covers a project-local keg
// resolved from the working directory but not registered under any
// alias in tap config.
func TestTap_Orient_ActiveKeg_NoAliasFallback(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	root := "/home/testuser/loose"
	kegDir := root + "/kegs/loose"
	require.NoError(t, fx.Runtime().Mkdir(kegDir, 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(kegDir+"/keg", []byte("kegv: 2025-07\n"), 0o644))
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{Root: root, Runtime: fx.Runtime()})
	require.NoError(t, err)

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		KegTargetOptions: tapper.KegTargetOptions{Project: true},
	})
	require.NoError(t, err)
	require.Contains(t, payload, "Active KEG:")
	require.Contains(t, payload, "(file-backed; no alias)")
	require.NotContains(t, payload, "loose/kegs/loose")
}

// TestTap_Orient_ActiveKeg_ExplicitOverride confirms that an explicit
// keg passed through OrientOptions wins over auto-resolution from cwd.
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
		require.NoError(t, fx.Runtime().AtomicWriteFile(dir+"/keg", []byte("kegv: 2025-07\n"), 0o644))
	}

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: "archive"},
	})
	require.NoError(t, err)
	require.Contains(t, payload, "Active KEG: `archive` (file-backed)")
	require.NotContains(t, payload, "Documents/kegs/archive")
}

func TestTap_IntegrateHosts_IsSortedAndIncludesDefaults(t *testing.T) {
	t.Parallel()
	hosts := tapper.IntegrateHosts()
	require.NotEmpty(t, hosts)
	require.Contains(t, hosts, "claude")
	require.Contains(t, hosts, "codex")
	for i := 1; i < len(hosts); i++ {
		require.LessOrEqual(t, hosts[i-1], hosts[i], "IntegrateHosts must be sorted")
	}
}
