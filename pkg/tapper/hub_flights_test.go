package tapper_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestHubFlights_ClientPaths(t *testing.T) {
	t.Parallel()

	var gotAuth string
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/flights":
			_ = json.NewEncoder(w).Encode([]tapper.HubFlight{{Namespace: "foldwise", Slug: "agent-work"}})
		case "GET /api/v1/@foldwise/+agent-work":
			_ = json.NewEncoder(w).Encode(tapper.HubFlight{
				Namespace:    "foldwise",
				Slug:         "agent-work",
				Title:        "Agent Work",
				Instructions: "Stay inside the cover.",
				Visibility:   tapper.FlightVisibilityPublic,
				Capabilities: []tapper.FlightCapability{tapper.FlightCapabilityManageFlights},
				Cover:        []tapper.HubFlightCover{{Namespace: "foldwise", Keg: "docs", Role: "viewer"}},
			})
		case "POST /api/v1/@foldwise/flights", "PUT /api/v1/@foldwise/+agent-work":
			var body tapper.HubFlight
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			body.Namespace = "foldwise"
			body.Slug = "agent-work"
			_ = json.NewEncoder(w).Encode(body)
		case "DELETE /api/v1/@foldwise/+agent-work":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	flights, err := tapper.ListUserFlights(context.Background(), srv.URL, "tok")
	require.NoError(t, err)
	require.Equal(t, []tapper.HubFlight{{Namespace: "foldwise", Slug: "agent-work"}}, flights)

	flight, err := tapper.GetHubFlight(context.Background(), srv.URL, "tok", "foldwise", "agent-work")
	require.NoError(t, err)
	require.Equal(t, "Agent Work", flight.Title)
	require.Equal(t, "viewer", flight.Cover[0].Role)
	require.Equal(t, tapper.FlightVisibilityPublic, flight.Visibility)
	require.Equal(t, []tapper.FlightCapability{tapper.FlightCapabilityManageFlights}, flight.Capabilities)

	created, err := tapper.CreateHubFlight(context.Background(), srv.URL, "tok", "foldwise", tapper.HubFlight{
		Slug:  "agent-work",
		Title: "Agent Work",
		Cover: []tapper.HubFlightCover{{Namespace: "foldwise", Keg: "docs", Role: "admin"}},
	})
	require.NoError(t, err)
	require.Equal(t, "admin", created.Cover[0].Role)

	_, err = tapper.UpdateHubFlight(context.Background(), srv.URL, "tok", "foldwise", "agent-work", *created)
	require.NoError(t, err)
	require.NoError(t, tapper.DeleteHubFlight(context.Background(), srv.URL, "tok", "foldwise", "agent-work"))
	require.Equal(t, "Bearer tok", gotAuth)
	require.Equal(t, []string{
		"GET /api/v1/flights",
		"GET /api/v1/@foldwise/+agent-work",
		"POST /api/v1/@foldwise/flights",
		"PUT /api/v1/@foldwise/+agent-work",
		"DELETE /api/v1/@foldwise/+agent-work",
	}, seen)
}

func TestHubFlights_ClientRejectsUnknownCoverRole(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tapper.HubFlight{
			Namespace: "foldwise",
			Slug:      "bad",
			Cover:     []tapper.HubFlightCover{{Namespace: "foldwise", Keg: "docs", Role: "owner"}},
		})
	}))
	defer srv.Close()

	_, err := tapper.GetHubFlight(context.Background(), srv.URL, "tok", "foldwise", "bad")
	require.ErrorContains(t, err, `invalid flight cover role "owner"`)
}
