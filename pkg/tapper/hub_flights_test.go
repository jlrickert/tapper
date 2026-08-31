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

	_, err = tapper.UpdateHubFlight(context.Background(), srv.URL, "tok", "foldwise", "agent-work", *created, created.Hash)
	require.NoError(t, err)
	require.NoError(t, tapper.DeleteHubFlight(context.Background(), srv.URL, "tok", "foldwise", "agent-work", created.Hash))
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

// TestHubFlights_ClientSurfacesHubDiagnosis pins that the hub's own message
// survives the status translation. The flight endpoints answer 404 for an
// unresolvable *namespace* and 403 for an insufficient namespace role, so a
// status-only message ("flight not found" on a create) names the wrong subject
// and sends the reader off to fix something that was never wrong.
func TestHubFlights_ClientSurfacesHubDiagnosis(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status int
		body   string
		wants  []string
	}{
		{
			name:   "namespace missing is not a missing flight",
			status: http.StatusNotFound,
			body:   "namespace not found",
			wants:  []string{"namespace not found", "POST", "/api/v1/@foldwise/flights"},
		},
		{
			name:   "insufficient role is not a rejected token",
			status: http.StatusForbidden,
			body:   "namespace owner access required",
			wants:  []string{"namespace owner access required", "/api/v1/@foldwise/flights"},
		},
		{
			name:   "conflict names the request",
			status: http.StatusConflict,
			body:   "flight already exists",
			wants:  []string{"flight already exists", "/api/v1/@foldwise/flights"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": tc.body})
			}))
			defer srv.Close()

			_, err := tapper.CreateHubFlight(context.Background(), srv.URL, "tok", "foldwise",
				tapper.HubFlight{Namespace: "foldwise", Slug: "new"})
			require.Error(t, err)
			for _, want := range tc.wants {
				require.ErrorContains(t, err, want)
			}
			require.NotContains(t, err.Error(), "flight not found",
				"a create cannot fail because the flight it would create is missing")
		})
	}
}
