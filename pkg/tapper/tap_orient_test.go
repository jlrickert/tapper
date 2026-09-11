package tapper_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"strings"
	"sync/atomic"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/internal/testapi"
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
	payload, err := newOrientTap(t).Orient(context.Background(), tapper.OrientOptions{})
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(payload, "# KEG System\n\n"))
	require.Contains(t, payload, "`guide`")
	require.Contains(t, payload, "identity-authorized full access")
	require.NotContains(t, payload, "## Available KEGs")
	require.NotContains(t, payload, "# Linking conventions")
}

func TestTap_Orient_UnknownFlightEmitsNote(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		KegTargetOptions: tapper.KegTargetOptions{Flight: "f-demo"},
	})
	require.NoError(t, err, "orient must never hard-fail on an unknown flight")
	require.Contains(t, payload, "# KEG System")
	require.Contains(t, payload, "Selected flight: `f-demo`")
	require.Contains(t, payload, `Flight "f-demo" is unavailable`)
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
	require.Contains(t, payload, `hub has no authenticated session for https://hub.example.com`)
	require.NotContains(t, payload, "`tap ")
}

func TestTap_IdentityKegCatalog_UsesOneKegCatalogRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		require.Equal(t, "/api/v1/kegs", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]tapper.HubKeg{{
			Namespace:   "foldwise",
			Alias:       "dev",
			Title:       "Development",
			Description: "Engineering system of record.",
			Visibility:  "private",
			Role:        "admin",
		}})
	}))
	defer srv.Close()

	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	cfg := fmt.Sprintf("hubs:\n  test:\n    kind: remote\n    url: %s\n    token: token\n", srv.URL)
	require.NoError(t, sb.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(cfg), 0o644))

	rows, warnings, catalogErr := tap.IdentityKegCatalog(context.Background())
	require.NoError(t, catalogErr)
	require.EqualValues(t, 1, requests.Load())
	require.Empty(t, warnings)
	require.Equal(t, []tapper.OrientationKeg{{
		Ref: "@foldwise/dev", Namespace: "foldwise", Alias: "dev",
		Title: "Development", Description: "Engineering system of record.",
		Visibility: "private", Role: "admin", Source: "test",
	}}, rows)
}

func TestTap_IdentityKegCatalog_NeverReadsIndividualSettings(t *testing.T) {
	t.Parallel()
	var configReads atomic.Int32
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/kegs":
			_ = json.NewEncoder(w).Encode([]tapper.HubKeg{{
				Namespace: "foldwise", Alias: "dev", Title: "Catalog title",
				Description: "Catalog summary.", Role: "admin",
			}})
		case "/api/v1/@foldwise/kegs/dev/settings":
			configReads.Add(1)
			http.Error(w, "aggregate discovery must not read settings", http.StatusInternalServerError)
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

	rows, warnings, catalogErr := tap.IdentityKegCatalog(context.Background())
	require.NoError(t, catalogErr)
	require.EqualValues(t, 0, configReads.Load())
	require.Empty(t, warnings)
	require.Len(t, rows, 1)
	require.Equal(t, "Catalog title", rows[0].Title)
	require.Equal(t, "Catalog summary.", rows[0].Description)
}

// Explicit KEG selection does not alter the orientation catalog or choose MCP
// authority; flight selection remains configuration-owned.
func TestTap_Orient_ActiveKeg_ExplicitOverride(t *testing.T) {
	t.Parallel()
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/kegs", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]tapper.HubKeg{{
			Namespace: "local", Alias: "archive", Title: "Archive", Role: "admin",
		}})
	}))
	defer srv.Close()
	fx := NewSandbox(t)
	root := "/home/testuser/work"
	require.NoError(t, fx.Runtime().Mkdir(root, 0o755, true))
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{Root: root, Runtime: fx.Runtime()})
	require.NoError(t, err)

	config := fmt.Sprintf("fallbackNamespace: local\nhubs:\n  home:\n    kind: remote\n    url: %s\n    token: test-token\n", srv.URL)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(config), 0o644))

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: "archive"},
	})
	require.NoError(t, err)
	require.NotContains(t, payload, "Active KEG:")
	require.NotContains(t, payload, "@local/archive")
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

func TestTap_Orient_UnpinnedPayloadUsesFullAccess(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)

	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{})
	require.NoError(t, err)

	require.Contains(t, payload, "No flight is active")
	require.Contains(t, payload, "identity-authorized full access")
	require.Contains(t, payload, "least-privilege flight")
	require.Contains(t, payload, "start a new connection")
	// The payload is the MCP-facing surface and never names CLI commands.
	require.NotContains(t, payload, "`tap ")
}

// TestTap_Orient_StatesZeroNodeAndAttachmentPaths pins two compact safety rules
// the runtime payload must carry alongside canonical link teaching.
func TestTap_Orient_StatesZeroNodeAndAttachmentPaths(t *testing.T) {
	guide, err := tapper.OrientationGuide("operating")
	require.NoError(t, err)
	require.Contains(t, guide, "Node 0 is the keg's placeholder landing node")
	require.Contains(t, guide, "(./assets/FILE)")
	require.Contains(t, guide, "(./images/IMAGE)")
}

func rowFor(t *testing.T, payload, ref string) string {
	t.Helper()
	for _, line := range strings.Split(payload, "\n") {
		if strings.Contains(line, "| `"+ref+"` |") {
			return line
		}
	}
	t.Fatalf("no table row for %q in payload:\n%s", ref, payload)
	return ""
}

func orientFlight(name, namespace, slug string, cover ...tapper.FlightCover) *tapper.Flight {
	return &tapper.Flight{
		Name: name, Namespace: namespace, Slug: slug, Source: "atlas",
		FlightManifest: tapper.FlightManifest{Cover: cover},
	}
}

func TestBuildOrientationPayload_SplitsCoveredKegsFromSubflightOnlyKegs(t *testing.T) {
	t.Parallel()
	root := orientFlight("@ada/+root", "ada", "root",
		tapper.FlightCover{Namespace: "ada", Keg: "covered", Role: tapper.FlightRoleEditor})
	// A graph-wide listing: one KEG the root covers, one only a descendant does.
	kegs := []tapper.OrientationKeg{
		{Ref: "@ada/covered", Namespace: "ada", Alias: "covered", Role: "admin",
			FlightCap: "editor", Flights: []string{"@ada/+root"}, Source: "atlas", Visibility: "private"},
		{Ref: "@ada/childonly", Namespace: "ada", Alias: "childonly", Role: "admin",
			FlightCap: "editor", Flights: []string{"@ada/+child"}, Source: "atlas", Visibility: "private"},
	}

	payload, err := tapper.BuildOrientationPayload(root, "", "", kegs, nil, nil)
	require.NoError(t, err)

	require.Contains(t, payload, "@ada/covered")
	require.NotContains(t, payload, "@ada/childonly")
	require.NotContains(t, payload, "## Reachable via subflight")
}

func TestBuildOrientationPayload_PartitionsByActiveCoverNotAggregateProvenance(t *testing.T) {
	t.Parallel()
	root := orientFlight("@ada/+root", "ada", "root",
		tapper.FlightCover{Namespace: "ada", Keg: "shared", Role: tapper.FlightRoleViewer})
	// AggregateOrientationKegs keeps only the winning grant, so this row names
	// the descendant and carries the descendant's editor cap even though the
	// root covers the same KEG at viewer. Partitioning on the Flights column
	// would hide a readable KEG behind a flight selection it does not need.
	kegs := []tapper.OrientationKeg{
		{Ref: "@ada/shared", Namespace: "ada", Alias: "shared", Role: "admin",
			FlightCap: "editor", Flights: []string{"@ada/+child"}, Source: "atlas", Visibility: "private"},
	}

	payload, err := tapper.BuildOrientationPayload(root, "", "", kegs, nil, nil)
	require.NoError(t, err)

	require.NotContains(t, payload, "## Reachable via subflight",
		"the active flight covers this KEG, so it is usable now")
	row := rowFor(t, payload, "@ada/shared")
	require.Contains(t, row, "viewer", "role is re-priced to the active flight's cap")
	require.NotContains(t, row, "editor", "the descendant's higher cap must not be quoted")
	require.Contains(t, payload, "@ada/+root")

	// Re-pricing must not reach back into the caller's rows. Providers hand the
	// same slice to FinalizeOrientation, which hashes Ref/Role/Visibility and
	// FlightCap into the authority revision, so partitioning in place would
	// move every revision and stale every governed request.
	require.Equal(t, "editor", kegs[0].FlightCap, "caller rows must not be mutated")
	require.Equal(t, []string{"@ada/+child"}, kegs[0].Flights)
}

func TestBuildOrientationPayload_EmptyCoverWithSubflightKegsPointsAtTheNextSection(t *testing.T) {
	t.Parallel()
	// The dispatcher shape: a root that carries instructions and no cover, and
	// delegates every KEG to a descendant. Reporting "no KEGs available" here
	// would stop an agent that should be reading the next section instead.
	root := orientFlight("@admin/+admin", "admin", "admin")
	kegs := []tapper.OrientationKeg{
		{Ref: "@admin/private", Namespace: "admin", Alias: "private", Role: "editor",
			FlightCap: "editor", Flights: []string{"@admin/+test"}, Source: "atlas", Visibility: "private"},
	}

	payload, err := tapper.BuildOrientationPayload(root, "", "", kegs, nil, nil)
	require.NoError(t, err)

	require.Contains(t, payload, "No covered readable KEGs")
	require.NotContains(t, payload, "No KEGs are currently available")
	require.NotContains(t, payload, "### Immediate subflights")
	require.NotContains(t, payload, "@admin/private")
}

func TestBuildOrientationPayload_SingleFlightProjectionRendersOneTable(t *testing.T) {
	t.Parallel()
	// Supplying an explicit flight projects exactly that flight, so every row
	// is covered and there is nothing to defer to a selection.
	root := orientFlight("@ada/+child", "ada", "child",
		tapper.FlightCover{Namespace: "ada", Keg: "notes", Role: tapper.FlightRoleEditor})
	kegs := []tapper.OrientationKeg{
		{Ref: "@ada/notes", Namespace: "ada", Alias: "notes", Role: "admin",
			FlightCap: "editor", Flights: []string{"@ada/+child"}, Source: "atlas", Visibility: "private"},
	}

	payload, err := tapper.BuildOrientationPayload(root, "", "", kegs, nil, nil)
	require.NoError(t, err)
	require.Contains(t, payload, "@ada/notes")
	require.NotContains(t, payload, "## Reachable via subflight")
}
