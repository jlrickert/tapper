package tapper_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestFlightService_ListAndGet(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	// A user config whose local hub basePath we control, so flights.d is at a
	// known location.
	userCfg := `hubs:
  home:
    kind: local
    defaultNamespace: local
    basePath: /home/testuser/kegs
`
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))

	flightYAML := `title: Backend work
allowedKegs:
  - personal
  - "@local/notes"
instructions: |
  Only touch backend kegs.
`
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		"/home/testuser/kegs/flights.d/backend.yaml", []byte(flightYAML), 0o644))
	// A non-manifest file and a dotfile must be ignored.
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		"/home/testuser/kegs/flights.d/README.md", []byte("ignore me"), 0o644))

	names, err := tap.ListFlights(fx.Context(), tapper.ListFlightsOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"@local/+backend"}, names)

	f, err := tap.GetFlight(fx.Context(), tapper.GetFlightOptions{Name: "backend"})
	require.NoError(t, err)
	require.Equal(t, "@local/+backend", f.Name)
	require.Equal(t, "Backend work", f.Title)
	require.Equal(t, []string{"personal", "@local/notes"}, f.AllowedKegs)
	require.Equal(t, []tapper.FlightCover{
		{Keg: "personal", Role: tapper.FlightRoleEditor},
		{Namespace: "local", Keg: "notes", Role: tapper.FlightRoleEditor},
	}, f.Cover)
	require.Contains(t, f.Instructions, "backend kegs")
	require.Equal(t, "local", f.Source)
	require.Len(t, f.ManifestHash, 64)
	encoded, err := json.Marshal(f)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "manifest_hash")

	_, err = tap.GetFlight(fx.Context(), tapper.GetFlightOptions{Name: "nope"})
	require.Error(t, err, "missing flight must error")
}

func TestFlightService_ManifestHashUsesNormalizedContent(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(`hubs:
  home:
    kind: local
    defaultNamespace: local
    basePath: /home/testuser/kegs
`), 0o644))

	path := "/home/testuser/kegs/flights.d/hash.yaml"
	baseline := `title: Focused
visibility: private
capabilities: [manage_flights, full_access]
cover:
  - namespace: local
    keg: personal
    role: editor
instructions: Stay focused.
`
	require.NoError(t, fx.Runtime().AtomicWriteFile(path, []byte(baseline), 0o644))
	flight, err := tap.FlightService.GetFlightFresh(fx.Context(), "+hash")
	require.NoError(t, err)
	baselineHash := flight.ManifestHash
	require.Len(t, baselineHash, 64)

	equivalent := `instructions: Stay focused.
cover:
- role: editor
  keg: personal
  namespace: local
capabilities:
- full_access
- manage_flights
visibility: private
title: Focused
`
	require.NoError(t, fx.Runtime().AtomicWriteFile(path, []byte(equivalent), 0o644))
	flight, err = tap.FlightService.GetFlightFresh(fx.Context(), "+hash")
	require.NoError(t, err)
	require.Equal(t, baselineHash, flight.ManifestHash)

	changes := map[string]string{
		"title":        strings.Replace(baseline, "title: Focused", "title: Changed", 1),
		"visibility":   strings.Replace(baseline, "visibility: private", "visibility: public", 1),
		"capabilities": strings.Replace(baseline, "capabilities: [manage_flights, full_access]", "capabilities: []", 1),
		"cover":        strings.Replace(baseline, "role: editor", "role: viewer", 1),
		"instructions": strings.Replace(baseline, "instructions: Stay focused.", "instructions: Changed.", 1),
	}
	for name, manifest := range changes {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, fx.Runtime().AtomicWriteFile(path, []byte(manifest), 0o644))
			changed, err := tap.FlightService.GetFlightFresh(fx.Context(), "+hash")
			require.NoError(t, err)
			require.NotEqual(t, baselineHash, changed.ManifestHash)
		})
	}
}

func TestFlightService_NoFlightsDir(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	// No flights.d anywhere: discovery yields an empty list, not an error.
	names, err := tap.ListFlights(fx.Context(), tapper.ListFlightsOptions{})
	require.NoError(t, err)
	require.Empty(t, names)
}

func TestFlightService_ListHubFilterContactsOnlySelectedHub(t *testing.T) {
	t.Parallel()
	var firstCalls, secondCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls.Add(1)
		require.Equal(t, "/api/v1/flights", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]tapper.HubFlight{{Namespace: "one", Slug: "focus"}})
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalls.Add(1)
		require.Equal(t, "/api/v1/flights", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]tapper.HubFlight{{Namespace: "two", Slug: "review"}})
	}))
	defer second.Close()

	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	config := "hubs:\n" +
		"  first: {kind: remote, url: " + first.URL + ", token: tok}\n" +
		"  second: {kind: remote, url: " + second.URL + ", token: tok}\n"
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(config), 0o644))

	names, err := tap.ListFlights(fx.Context(), tapper.ListFlightsOptions{Hub: "first"})
	require.NoError(t, err)
	require.Equal(t, []string{"@one/+focus"}, names)
	require.EqualValues(t, 1, firstCalls.Load())
	require.Zero(t, secondCalls.Load(), "filtered discovery must not contact unrelated hubs")

	names, err = tap.ListFlights(fx.Context(), tapper.ListFlightsOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"@one/+focus", "@two/+review"}, names)
	require.EqualValues(t, 2, firstCalls.Load())
	require.EqualValues(t, 1, secondCalls.Load(), "unfiltered listing should retain all-hub behavior")
}

func TestParseFlightRef(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		raw              string
		defaultNamespace string
		want             tapper.FlightRef
	}{
		{name: "slug", raw: "agent-work", want: tapper.FlightRef{Slug: "agent-work"}},
		{name: "plus_slug", raw: "+agent-work", want: tapper.FlightRef{Slug: "agent-work"}},
		{name: "default_namespace", raw: "+agent-work", defaultNamespace: "jlrickert", want: tapper.FlightRef{Namespace: "jlrickert", Slug: "agent-work"}},
		{name: "qualified", raw: "@foldwise/+agent-work", want: tapper.FlightRef{Namespace: "foldwise", Slug: "agent-work"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tapper.ParseFlightRef(tc.raw, tc.defaultNamespace)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestParseFlightRefRejectsUnusableNamespace pins that a malformed namespace
// fails here rather than travelling. The `+` sigil marks the slug, so
// "@+slug/..." transposes it into the namespace position — a namespace that can
// never exist. Left unvalidated it reaches the hub and returns as a bare 404,
// which reads as a missing flight and sends the author looking at permissions.
func TestParseFlightRefRejectsUnusableNamespace(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"@+agent-work/x", "@Foldwise/+agent-work", "@foldwise.dev/+agent-work"} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			_, err := tapper.ParseFlightRef(raw, "jlrickert")
			require.ErrorContains(t, err, "invalid flight reference")
			require.ErrorContains(t, err, "@namespace/+slug",
				"the refusal must show the form the author meant to write")
		})
	}
}

func TestFlightRoleFor_CoverCapsWrites(t *testing.T) {
	t.Parallel()
	flight := &tapper.Flight{
		Name: "@foldwise/+review",
		FlightManifest: tapper.FlightManifest{
			Cover: []tapper.FlightCover{
				{Namespace: "foldwise", Keg: "docs", Role: tapper.FlightRoleViewer},
				{Namespace: "foldwise", Keg: "dev", Role: tapper.FlightRoleEditor},
				{Namespace: "foldwise", Keg: "admin", Role: tapper.FlightRoleAdmin},
			},
		},
	}
	role, ok := flight.RoleFor("", "foldwise", "docs")
	require.True(t, ok)
	require.Equal(t, tapper.FlightRoleViewer, role)
	require.True(t, role.AtLeast(tapper.FlightRoleViewer))
	require.False(t, role.AtLeast(tapper.FlightRoleEditor))

	role, ok = flight.RoleFor("", "foldwise", "dev")
	require.True(t, ok)
	require.Equal(t, tapper.FlightRoleEditor, role)
	require.True(t, role.AtLeast(tapper.FlightRoleEditor))
	require.False(t, role.AtLeast(tapper.FlightRoleAdmin))

	role, ok = flight.RoleFor("", "foldwise", "admin")
	require.True(t, ok)
	require.Equal(t, tapper.FlightRoleAdmin, role)
	require.True(t, role.AtLeast(tapper.FlightRoleViewer))
	require.True(t, role.AtLeast(tapper.FlightRoleEditor))
	require.True(t, role.AtLeast(tapper.FlightRoleAdmin))

	_, ok = flight.RoleFor("", "foldwise", "private")
	require.False(t, ok)
}

func TestParseFlightCoverSpecs_AdminAndUnknownRoles(t *testing.T) {
	t.Parallel()
	cover, err := tapper.ParseFlightCoverSpecs([]string{"@foldwise/dev=admin"})
	require.NoError(t, err)
	require.Equal(t, []tapper.FlightCover{{
		Namespace: "foldwise",
		Keg:       "dev",
		Role:      tapper.FlightRoleAdmin,
	}}, cover)

	_, err = tapper.ParseFlightCoverSpecs([]string{"@foldwise/dev=owner"})
	require.ErrorContains(t, err, `invalid flight cover role "owner"`)
}

func TestFlightRoleFor_EmptyCoverDeniesAll(t *testing.T) {
	t.Parallel()
	flight := &tapper.Flight{Name: "@foldwise/+empty"}
	_, ok := flight.RoleFor("docs", "foldwise", "docs")
	require.False(t, ok)
}

func TestFlightService_RejectsUnknownCapabilities(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(`hubs:
  home:
    kind: local
    defaultNamespace: local
    basePath: /home/testuser/kegs
`), 0o644))
	require.NoError(t, fx.Runtime().AtomicWriteFile("/home/testuser/kegs/flights.d/bad.yaml", []byte("capabilities: [shell_access]\n"), 0o644))

	_, err = tap.GetFlight(fx.Context(), tapper.GetFlightOptions{Name: "+bad"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown flight capability "shell_access"`)
}

func TestFlightService_RejectsUnknownCoverRoles(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(`hubs:
  home:
    kind: local
    defaultNamespace: local
    basePath: /home/testuser/kegs
`), 0o644))
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		"/home/testuser/kegs/flights.d/bad-role.yaml",
		[]byte("cover:\n  - keg: personal\n    role: owner\n"),
		0o644,
	))

	_, err = tap.GetFlight(fx.Context(), tapper.GetFlightOptions{Name: "+bad-role"})
	require.ErrorContains(t, err, `invalid flight cover role "owner"`)
}

func TestFlightService_AcceptsFullAccessCapability(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(`hubs:
  home:
    kind: local
    defaultNamespace: local
    basePath: /home/testuser/kegs
`), 0o644))
	require.NoError(t, fx.Runtime().AtomicWriteFile("/home/testuser/kegs/flights.d/full.yaml", []byte("capabilities: [full_access]\n"), 0o644))

	flight, err := tap.GetFlight(fx.Context(), tapper.GetFlightOptions{Name: "+full"})
	require.NoError(t, err)
	require.True(t, flight.HasCapability(tapper.FlightCapabilityFullAccess))
	require.False(t, flight.HasCapability(tapper.FlightCapabilityManageFlights))
}

// A viewer cap must survive repeated RoleFor calls: the legacy AllowedKegs
// mirror used to be re-merged into the cover as editor rows on every call,
// leaving viewer enforcement to a fragile ordering invariant.
func TestFlightRoleFor_ViewerCapStableAcrossCalls(t *testing.T) {
	t.Parallel()
	flight := &tapper.Flight{
		Name: "@foldwise/+review",
		FlightManifest: tapper.FlightManifest{
			Cover: []tapper.FlightCover{
				{Namespace: "foldwise", Keg: "docs", Role: tapper.FlightRoleViewer},
			},
			// Legacy mirror naming the same keg, as older normalize passes
			// produced. Must not escalate the explicit viewer cap.
			AllowedKegs: []string{"@foldwise/docs"},
		},
	}
	for i := range 3 {
		role, ok := flight.RoleFor("", "foldwise", "docs")
		require.True(t, ok)
		require.Equal(t, tapper.FlightRoleViewer, role, "call %d", i)
	}
	require.Len(t, flight.Cover, 1, "RoleFor must not mutate the flight's cover")
}

// Legacy allowedKegs entries keep their historical editor default, but an
// explicit =viewer suffix is honored.
func TestFlightRoleFor_LegacyAllowedKegsRoles(t *testing.T) {
	t.Parallel()
	flight := &tapper.Flight{
		Name: "@foldwise/+legacy",
		FlightManifest: tapper.FlightManifest{
			AllowedKegs: []string{"@foldwise/dev", "@foldwise/docs=viewer"},
		},
	}
	role, ok := flight.RoleFor("", "foldwise", "dev")
	require.True(t, ok)
	require.Equal(t, tapper.FlightRoleEditor, role)

	role, ok = flight.RoleFor("", "foldwise", "docs")
	require.True(t, ok)
	require.Equal(t, tapper.FlightRoleViewer, role)
}

func TestFlightEnforcement_LocalHubPathIdentity(t *testing.T) {
	t.Parallel()
	tap, personalID, privateID := newLocalFlightEnforcementFixture(t)

	got, err := tap.Cat(t.Context(), tapper.CatOptions{
		NodeIDs: []string{personalID},
		KegTargetOptions: tapper.KegTargetOptions{
			Keg:    "personal",
			Flight: "+focused",
		},
		ContentOnly: true,
	})
	require.NoError(t, err)
	require.Contains(t, got, "# Personal")

	_, err = tap.Cat(t.Context(), tapper.CatOptions{
		NodeIDs: []string{privateID},
		KegTargetOptions: tapper.KegTargetOptions{
			Keg:    "private",
			Flight: "+focused",
		},
		ContentOnly: true,
	})
	require.Error(t, err)
	var restriction *tapper.FlightRestrictionError
	require.ErrorAs(t, err, &restriction)
	require.Contains(t, err.Error(), `keg "@local/private" is not available in flight`)
}

func TestFlightEnforcement_ViewerCoverAllowsReadsAndRejectsWrites(t *testing.T) {
	t.Parallel()
	tap, personalID, _ := newLocalFlightEnforcementFixture(t)

	_, err := tap.Cat(t.Context(), tapper.CatOptions{
		NodeIDs: []string{personalID},
		KegTargetOptions: tapper.KegTargetOptions{
			Keg:    "personal",
			Flight: "+focused",
		},
		ContentOnly: true,
	})
	require.NoError(t, err)

	_, err = tap.Create(t.Context(), tapper.CreateOptions{
		KegTargetOptions: tapper.KegTargetOptions{
			Keg:    "personal",
			Flight: "+focused",
		},
		Title: "Blocked Write",
	})
	require.Error(t, err)
	var restriction *tapper.FlightRestrictionError
	require.ErrorAs(t, err, &restriction)
	require.Contains(t, err.Error(), `keg "@local/personal" is viewer-only in flight`)
}

func TestFlightBypass_AllowsReadOutsideCover(t *testing.T) {
	t.Parallel()
	tap, _, privateID := newLocalFlightEnforcementFixture(t)

	got, err := tap.Cat(t.Context(), tapper.CatOptions{
		NodeIDs: []string{privateID},
		KegTargetOptions: tapper.KegTargetOptions{
			Keg:                      "private",
			Flight:                   "+focused",
			BypassFlightRestrictions: true,
		},
		ContentOnly: true,
	})
	require.NoError(t, err)
	require.Contains(t, got, "# Private")
}

func TestFlightBypass_AllowsWriteThroughViewerCover(t *testing.T) {
	t.Parallel()
	tap, _, _ := newLocalFlightEnforcementFixture(t)

	node, err := tap.Create(t.Context(), tapper.CreateOptions{
		KegTargetOptions: tapper.KegTargetOptions{
			Keg:                      "personal",
			Flight:                   "+focused",
			BypassFlightRestrictions: true,
		},
		Title: "Allowed Write",
	})
	require.NoError(t, err)
	require.NotEmpty(t, node.Path())
}

func TestFlightEnforcement_FullAccessBypassesCoverCaps(t *testing.T) {
	t.Parallel()
	tap, _, privateID := newLocalFlightEnforcementFixture(t)
	manifest := `title: Full access
capabilities: [full_access]
cover:
  - namespace: local
    keg: personal
    role: viewer
`
	require.NoError(t, tap.Runtime.AtomicWriteFile("/home/testuser/kegs/flights.d/focused.yaml", []byte(manifest), 0o644))

	got, err := tap.Cat(t.Context(), tapper.CatOptions{
		NodeIDs: []string{privateID},
		KegTargetOptions: tapper.KegTargetOptions{
			Keg:    "private",
			Flight: "+focused",
		},
		ContentOnly: true,
	})
	require.NoError(t, err)
	require.Contains(t, got, "# Private")

	_, err = tap.Create(t.Context(), tapper.CreateOptions{
		KegTargetOptions: tapper.KegTargetOptions{
			Keg:    "personal",
			Flight: "+focused",
		},
		Title: "Full Access Write",
	})
	require.NoError(t, err)
}

func newLocalFlightEnforcementFixture(t *testing.T) (*tapper.Tap, string, string) {
	t.Helper()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	userCfg := `fallbackNamespace: local
hubs:
  home:
    kind: local
    defaultNamespace: local
    basePath: /home/testuser/kegs
`
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))
	for _, name := range []string{"personal", "private"} {
		_, err := tap.InitKeg(t.Context(), tapper.InitOptions{Keg: name, Namespace: "local"})
		require.NoError(t, err)
	}
	personalID, err := tap.Create(t.Context(), tapper.CreateOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: "personal"},
		Title:            "Personal",
	})
	require.NoError(t, err)
	privateID, err := tap.Create(t.Context(), tapper.CreateOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: "private"},
		Title:            "Private",
	})
	require.NoError(t, err)

	flightYAML := `title: Focused
cover:
  - namespace: local
    keg: personal
    role: viewer
`
	require.NoError(t, fx.Runtime().AtomicWriteFile("/home/testuser/kegs/flights.d/focused.yaml", []byte(flightYAML), 0o644))
	return tap, personalID.PathNumeric(), privateID.PathNumeric()
}
