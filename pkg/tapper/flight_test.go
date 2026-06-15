package tapper_test

import (
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

	_, err = tap.GetFlight(fx.Context(), tapper.GetFlightOptions{Name: "nope"})
	require.Error(t, err, "missing flight must error")
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

func TestFlightRoleFor_CoverCapsWrites(t *testing.T) {
	t.Parallel()
	flight := &tapper.Flight{
		Name: "@foldwise/+review",
		FlightManifest: tapper.FlightManifest{
			Cover: []tapper.FlightCover{
				{Namespace: "foldwise", Keg: "docs", Role: tapper.FlightRoleViewer},
				{Namespace: "foldwise", Keg: "dev", Role: tapper.FlightRoleEditor},
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

	_, ok = flight.RoleFor("", "foldwise", "private")
	require.False(t, ok)
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
