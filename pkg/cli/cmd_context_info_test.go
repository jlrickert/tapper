package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestInfoCLI_ShowsHubKegFlightAndFlightKegs(t *testing.T) {
	t.Parallel()
	hub := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/whoami":
			_ = json.NewEncoder(w).Encode(tapper.WhoAmI{UserID: 1, Username: "jared", DefaultNamespace: "foldwise"})
		case "GET /api/v1/@foldwise/+work":
			_ = json.NewEncoder(w).Encode(tapper.HubFlight{
				Namespace: "foldwise", Slug: "work", Title: "Work",
				Cover: []tapper.HubFlightCover{{Namespace: "foldwise", Keg: "dev", Role: "editor"}},
			})
		case "GET /api/v1/kegs":
			_ = json.NewEncoder(w).Encode([]tapper.HubKeg{
				{Namespace: "foldwise", Alias: "dev", Title: "Development", Role: "admin"},
				{Namespace: "foldwise", Alias: "private", Title: "Outside the flight", Role: "admin"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer hub.Close()
	sb := NewSandbox(t)
	cfg := fmt.Sprintf("hub: cloud\nkeg: \"@foldwise/dev\"\nflight: \"@foldwise/+work\"\nhubs:\n  cloud: {url: %s, token: tok}\n", hub.URL)
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(cfg), 0o600))

	res := NewProcess(t, false, "info", "--json").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	var info tapper.ContextInfoResult
	require.NoError(t, json.Unmarshal(res.Stdout, &info), string(res.Stdout))

	require.Equal(t, "cloud", info.Hub.Name)
	require.Equal(t, hub.URL, info.Hub.URL)
	require.Equal(t, "jared", info.Hub.User)
	require.Equal(t, "@foldwise/dev", info.Keg.Ref)
	require.Equal(t, "user", info.Keg.Scope)
	require.Equal(t, "@foldwise/+work", info.Flight.Ref)
	require.Equal(t, "Work", info.Flight.Title)
	require.Equal(t, []tapper.ContextInfoFlightKeg{{Ref: "@foldwise/dev", Role: "editor", Title: "Development"}}, info.Kegs,
		"only the kegs the flight covers, at the role the flight caps them to")
	require.Empty(t, info.Warnings)
}

func TestInfoCLI_UnreachableHubStillPrintsLocalDefaults(t *testing.T) {
	t.Parallel()
	hub := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer hub.Close()
	sb := NewSandbox(t)
	cfg := fmt.Sprintf("hub: cloud\nkeg: \"@foldwise/dev\"\nflight: \"@foldwise/+work\"\nhubs:\n  cloud: {url: %s, token: tok}\n", hub.URL)
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(cfg), 0o600))

	res := NewProcess(t, false, "info").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "an unreachable hub is a warning, not a failure")
	out := string(res.Stdout)
	require.Contains(t, out, "name: cloud")
	require.Contains(t, out, "ref: '@foldwise/dev'")
	require.Contains(t, out, "ref: '@foldwise/+work'")
	require.Contains(t, out, "warnings:")
}
