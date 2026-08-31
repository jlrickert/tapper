package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestRootConfiguredFlightDoesNotBecomeExplicitDependency(t *testing.T) {
	t.Parallel()
	flight := tapper.HubFlight{Namespace: "team", Slug: "project", Title: "Project", Instructions: "Project instructions"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/@team/+project":
			_ = json.NewEncoder(w).Encode(flight)
		case "/api/v1/flights":
			_ = json.NewEncoder(w).Encode([]tapper.HubFlight{flight})
		case "/api/v1/kegs":
			_ = json.NewEncoder(w).Encode([]tapper.HubKeg{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Setwd("/home/testuser/project/child"))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/.config/tapper/config.yaml",
		[]byte(fmt.Sprintf("flight: +baseline\nfallbackHub: home\nfallbackNamespace: team\nhubs:\n  home:\n    kind: remote\n    url: %s\n    token: test-token\n", srv.URL)), 0o644))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/project/.tapper/config.yaml",
		[]byte("flight: +project\n"), 0o644))
	deps := &Deps{Profile: TapProfile(), Runtime: sb.Runtime()}
	cmd := NewRootCmd(deps)
	cmd.SetArgs([]string{"orient"})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	require.NoError(t, cmd.ExecuteContext(sb.Context()), stderr.String())
	require.Empty(t, deps.KegTargetOptions.Flight,
		"configured selection must not occupy the explicit --flight dependency")
	require.Contains(t, stdout.String(), "+project")
}
