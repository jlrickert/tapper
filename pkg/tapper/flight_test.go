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
    namespace: local
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
	require.Equal(t, []string{"backend"}, names)

	f, err := tap.GetFlight(fx.Context(), tapper.GetFlightOptions{Name: "backend"})
	require.NoError(t, err)
	require.Equal(t, "Backend work", f.Title)
	require.Equal(t, []string{"personal", "@local/notes"}, f.AllowedKegs)
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
