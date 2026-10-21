package tapper_test

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func (fx *flightEditFixture) lastReceived(t *testing.T) tapper.HubFlight {
	t.Helper()
	fx.mu.Lock()
	defer fx.mu.Unlock()
	require.NotEmpty(t, fx.received)
	return fx.received[len(fx.received)-1]
}

// A piped manifest that sets a switch sends it, and the flight it returns
// carries it.
func TestEditFlight_PipedManifestSetsSwitches(t *testing.T) {
	t.Parallel()
	hub := newFlightEditFixture(t)
	tap, fx := newFlightEditTap(t, hub.srv.URL)

	flight, err := tap.EditFlight(fx.Context(), tapper.EditFlightOptions{
		Ref:    "@foldwise/+agent-work",
		Stream: pipedStream("title: Agent Work\nmanageKegs: true\ncover:\n  - keg: docs\ninstructions: Stay inside the cover.\n"),
	})
	require.NoError(t, err)
	require.True(t, flight.ManageKegs)
	require.False(t, flight.ManageFlights)
	sent := hub.lastReceived(t)
	require.NotNil(t, sent.ManageKegs)
	require.True(t, *sent.ManageKegs)
	require.Nil(t, sent.ManageFlights, "an unchanged switch is not sent")
}

// A manifest that leaves the switches as they are does not send them, so
// editing a managing flight's title cannot change what it lets a request
// manage; a manifest that drops one clears it.
func TestEditFlight_SwitchesSentOnlyWhenChanged(t *testing.T) {
	t.Parallel()
	hub := newFlightEditFixture(t)
	on := true
	hub.setCurrent(tapper.HubFlight{Namespace: "foldwise", Slug: "agent-work", Title: "Agent Work", ManageKegs: &on, ManageFlights: &on,
		Cover: []tapper.HubFlightCover{{Namespace: "foldwise", Keg: "docs", Role: "viewer"}}})
	tap, fx := newFlightEditTap(t, hub.srv.URL)

	flight, err := tap.EditFlight(fx.Context(), tapper.EditFlightOptions{
		Ref:    "@foldwise/+agent-work",
		Stream: pipedStream("title: Retitled\nmanageKegs: true\nmanageFlights: true\ncover:\n  - namespace: foldwise\n    keg: docs\n"),
	})
	require.NoError(t, err)
	sent := hub.lastReceived(t)
	require.Nil(t, sent.ManageKegs, "an unchanged switch is not sent")
	require.Nil(t, sent.ManageFlights)
	require.True(t, flight.ManageKegs)

	flight, err = tap.EditFlight(fx.Context(), tapper.EditFlightOptions{
		Ref:    "@foldwise/+agent-work",
		Stream: pipedStream("title: Retitled\nmanageFlights: true\ncover:\n  - namespace: foldwise\n    keg: docs\n"),
	})
	require.NoError(t, err)
	sent = hub.lastReceived(t)
	require.NotNil(t, sent.ManageKegs)
	require.False(t, *sent.ManageKegs, "a full manifest without a switch clears it")
	require.Nil(t, sent.ManageFlights)
	require.False(t, flight.ManageKegs)
	require.True(t, flight.ManageFlights)
}

// A partial update sends a switch only when the caller sets it.
func TestUpdateFlight_SwitchesOnlyWhenSet(t *testing.T) {
	t.Parallel()
	hub := newFlightEditFixture(t)
	tap, fx := newFlightEditTap(t, hub.srv.URL)

	title := "Retitled"
	_, err := tap.UpdateFlight(fx.Context(), tapper.UpdateFlightOptions{Ref: "@foldwise/+agent-work", Title: &title, ExpectedHash: "h"})
	require.NoError(t, err)
	require.Nil(t, hub.lastReceived(t).ManageKegs)
	require.Nil(t, hub.lastReceived(t).ManageFlights)

	on := true
	flight, err := tap.UpdateFlight(fx.Context(), tapper.UpdateFlightOptions{Ref: "@foldwise/+agent-work", ManageFlights: &on, ExpectedHash: "h"})
	require.NoError(t, err)
	sent := hub.lastReceived(t)
	require.Nil(t, sent.ManageKegs)
	require.NotNil(t, sent.ManageFlights)
	require.True(t, *sent.ManageFlights)
	require.True(t, flight.ManageFlights)
}

// Creating a flight sends only the switches it sets; the create body leaves
// the others out.
func TestCreateFlight_Switches(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var bodies []map[string]any
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "POST /api/v1/@foldwise/flights", r.Method+" "+r.URL.Path)
		var raw map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&raw))
		mu.Lock()
		bodies = append(bodies, raw)
		mu.Unlock()
		kegs, _ := raw["manage_kegs"].(bool)
		flights, _ := raw["manage_flights"].(bool)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tapper.HubFlight{Namespace: "foldwise", Slug: raw["slug"].(string), Visibility: "private", ManageKegs: &kegs, ManageFlights: &flights})
	}))
	t.Cleanup(srv.Close)
	tap, fx := newFlightEditTap(t, srv.URL)

	managing, err := tap.CreateFlight(fx.Context(), tapper.CreateFlightOptions{Ref: "@foldwise/+desk", ManageKegs: true})
	require.NoError(t, err)
	require.True(t, managing.ManageKegs)
	require.False(t, managing.ManageFlights)
	plain, err := tap.CreateFlight(fx.Context(), tapper.CreateFlightOptions{Ref: "@foldwise/+plain"})
	require.NoError(t, err)
	require.False(t, plain.ManageKegs)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, true, bodies[0]["manage_kegs"])
	_, sent := bodies[0]["manage_flights"]
	require.False(t, sent, "a switch not set is left out")
	_, sent = bodies[1]["manage_kegs"]
	require.False(t, sent)
}

// An older Hub answers with the single manage flag; it reads as both
// switches.
func TestFlightFromAnOlderHubReadsManageAsBothSwitches(t *testing.T) {
	t.Parallel()
	hub := newFlightEditFixture(t)
	on := true
	hub.setCurrent(tapper.HubFlight{Namespace: "foldwise", Slug: "agent-work", Title: "Agent Work", Manage: &on})
	tap, fx := newFlightEditTap(t, hub.srv.URL)

	title := "Retitled"
	flight, err := tap.UpdateFlight(fx.Context(), tapper.UpdateFlightOptions{Ref: "@foldwise/+agent-work", Title: &title, ExpectedHash: "h"})
	require.NoError(t, err)
	require.True(t, flight.ManageKegs)
	require.True(t, flight.ManageFlights)
	require.Nil(t, hub.lastReceived(t).Manage, "the old flag is never sent")
}
