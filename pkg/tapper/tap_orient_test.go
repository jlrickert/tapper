package tapper_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	require.NotContains(t, payload, "## Active KEG")
	require.Contains(t, payload, "## Available KEGs")
	require.NotContains(t, payload, "## KEG Instructions")
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

func TestTap_Orient_FlightInstructionsAndKegDiscoveryPrecedeGuidance(t *testing.T) {
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
		[]byte("kegv: 2025-07\ntitle: Personal\nsummary: Personal discovery text.\ninstructions: |\n  Prefer audited personal-context nodes.\n"), 0o644))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/kegs/flights.d/backend.yaml",
		[]byte("title: Backend\ncover:\n  - namespace: local\n    keg: personal\n    role: viewer\n  - namespace: local\n    keg: dev\n    role: editor\ninstructions: |\n  Touch only backend kegs.\n"), 0o644))

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		KegTargetOptions: tapper.KegTargetOptions{Flight: "backend"},
	})
	require.NoError(t, err)
	require.Contains(t, payload, "| `@local/dev` | dev | — | editor | home/local | editor |")
	require.Contains(t, payload, "| `@local/personal` | Personal | Personal discovery text. | editor | home/local | viewer |")
	require.Contains(t, payload, "## Flight")
	require.Contains(t, payload, "Backend")
	require.Contains(t, payload, "Touch only backend kegs.")
	require.NotContains(t, payload, "## KEG Instructions")
	require.NotContains(t, payload, "Prefer audited personal-context nodes.")
	require.Contains(t, payload, "Call `keg_settings`")

	guidanceAt := strings.Index(payload, "## Guidance")
	require.NotEqual(t, -1, guidanceAt)
	require.Less(t, strings.Index(payload, "Touch only backend kegs."), guidanceAt)
	require.Less(t, strings.Index(payload, "Call `keg_settings`"), guidanceAt)
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
	require.NotContains(t, payload, "Follow the covered KEG schema.")
	require.NotContains(t, payload, "## KEG Instructions")
	require.NotContains(t, payload, "| `@local/personal`")
}

// TestTap_OrientReloadsNearestProjectConfig covers the reload boundary. Orient
// owns the cache reset; ActiveFlightName is a pure read of whatever cascade is
// currently loaded, so a stale cache stays stale until Orient refreshes it.
func TestTap_OrientReloadsNearestProjectConfig(t *testing.T) {
	t.Parallel()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	project := "/home/testuser/project"
	descendant := filepath.Join(project, "src", "pkg")
	require.NoError(t, sb.Setwd(descendant))

	tap, err := tapper.NewTap(tapper.TapOptions{Root: descendant, Runtime: sb.Runtime()})
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(`flight: +baseline
fallbackNamespace: local
hubs:
  home:
    kind: local
    basePath: /home/testuser/kegs
`), 0o644))

	// Prime the merged cache before the project config exists. Orientation must
	// still reload the cascade and adopt the nearest project selection.
	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	require.Equal(t, "+baseline", cfg.Flight())
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		filepath.Join(project, ".tapper", "config.yaml"),
		[]byte("flight: +project\n"), 0o644))

	for _, flight := range []struct{ slug, title string }{
		{"baseline", "Baseline"},
		{"project", "Project"},
	} {
		require.NoError(t, sb.Runtime().AtomicWriteFile(
			filepath.Join("/home/testuser/kegs/flights.d", flight.slug+".yaml"),
			[]byte("title: "+flight.title+"\ninstructions: "+flight.title+" instructions\n"), 0o644))
	}

	// The primed cache still answers with the user-level baseline, because a
	// pure read must not silently reload behind the caller's back.
	require.Equal(t, "+baseline", tap.ActiveFlightName(""))

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{})
	require.NoError(t, err)
	require.Contains(t, payload, "+project")
	require.Contains(t, payload, "Project instructions")
	require.NotContains(t, payload, "Baseline instructions")

	// Orient reloaded the cascade, so the pure read now sees the project value.
	require.Equal(t, "+project", tap.ActiveFlightName(""))
}

func TestTap_Orient_FullAccessStillSuppressesKegInstructions(t *testing.T) {
	t.Parallel()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	require.NoError(t, sb.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: sb.Runtime()})
	require.NoError(t, err)

	require.NoError(t, sb.Runtime().AtomicWriteFile(tap.PathService.UserConfig(),
		[]byte("hubs:\n  home:\n    kind: local\n    defaultNamespace: local\n    basePath: /home/testuser/kegs\n"), 0o644))
	dir := "/home/testuser/kegs/@local/dev"
	require.NoError(t, sb.Runtime().Mkdir(dir, 0o755, true))
	require.NoError(t, sb.Runtime().AtomicWriteFile(dir+"/keg", []byte(
		"kegv: 2025-07\ntitle: Development\nsummary: Discoverable engineering context.\ninstructions: DO NOT LEAK FULL ACCESS GUIDANCE\n",
	), 0o644))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/kegs/flights.d/full.yaml",
		[]byte("title: Full access\ncapabilities: [full_access]\ninstructions: Flight guidance remains visible.\n"),
		0o644,
	))

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		KegTargetOptions: tapper.KegTargetOptions{Flight: "full"},
	})
	require.NoError(t, err)
	require.Contains(t, payload, "Discoverable engineering context.")
	require.Contains(t, payload, "Flight guidance remains visible.")
	require.Contains(t, payload, "| admin |")
	require.NotContains(t, payload, "DO NOT LEAK FULL ACCESS GUIDANCE")
	require.NotContains(t, payload, "## KEG Instructions")
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
	require.NotContains(t, payload, "Active KEG:")
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

func TestTap_Orient_CompatibleRemoteUsesOneDiscoveryRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		require.Equal(t, "/api/v1/orient", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]tapper.HubOrientationKeg{{
			Namespace:  "foldwise",
			Alias:      "dev",
			Title:      "Development",
			Summary:    "Engineering system of record.",
			Visibility: "private",
			Role:       "admin",
		}})
	}))
	defer srv.Close()

	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	cfg := fmt.Sprintf("hubs:\n  test:\n    kind: remote\n    url: %s\n    token: token\n", srv.URL)
	require.NoError(t, sb.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(cfg), 0o644))

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 1, requests.Load())
	require.Contains(t, payload, "| `@foldwise/dev` | Development | Engineering system of record. | admin | test/private | none |")
	require.NotContains(t, payload, "## KEG Instructions")
}

func TestTap_Orient_OlderHubFallbackSuppressesInstructions(t *testing.T) {
	t.Parallel()
	var configReads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orient":
			http.NotFound(w, r)
		case "/api/v1/kegs":
			_ = json.NewEncoder(w).Encode([]tapper.HubKeg{{
				Namespace: "foldwise",
				Alias:     "dev",
				Role:      "admin",
			}})
		case "/api/v1/@foldwise/kegs/dev/config":
			configReads.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kegv":         "2025-07",
				"title":        "Fallback title",
				"summary":      "Fallback summary.",
				"instructions": "DO NOT LEAK FALLBACK INSTRUCTIONS",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	cfg := fmt.Sprintf("hubs:\n  test:\n    kind: remote\n    url: %s\n    token: token\n", srv.URL)
	require.NoError(t, sb.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(cfg), 0o644))

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{})
	require.NoError(t, err)
	require.EqualValues(t, 1, configReads.Load())
	require.Contains(t, payload, "Fallback title")
	require.Contains(t, payload, "Fallback summary.")
	require.NotContains(t, payload, "DO NOT LEAK FALLBACK INSTRUCTIONS")
	require.NotContains(t, payload, "## KEG Instructions")
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
	require.NotContains(t, payload, "Active KEG:")
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
	require.NotContains(t, payload, "Active KEG:")
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
	require.NotContains(t, payload, "Active KEG:")
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

// TestTap_Orient_RecoveryPayloadStatesTheSituation pins the recovery guidance.
// The MCP tool list is filtered to the recovery set, so an agent never gets to
// call a locked tool and see the error explaining why — which left the empty
// KEG table as the only signal, and weaker models do not act on an absence.
func TestTap_Orient_RecoveryPayloadStatesTheSituation(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{})
	require.NoError(t, err)

	require.Contains(t, payload, "No flight is selected")
	require.Contains(t, payload, "recovery mode")
	require.Contains(t, payload, "KEG tools are locked")
	require.Contains(t, payload, "`list_flights`")
	require.Contains(t, payload, "Call `orient` again")
	// The payload is the MCP-facing surface and never names CLI commands.
	require.NotContains(t, payload, "`tap ")
}
