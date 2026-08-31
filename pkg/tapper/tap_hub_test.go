package tapper_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestHubListKegs_RemoteRequiresAuth(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	// A remote hub with no resolvable token: an explicit --hub surfaces the
	// missing-auth error directly.
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(),
		[]byte("hubs:\n  atlas:\n    kind: remote\n    url: https://atlas.foldwise.ai\n"), 0o644))

	_, err = tap.HubListKegs(fx.Context(), tapper.HubListOptions{Hub: "atlas"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "auth token")
}

func TestHubListKegs_RemoteAggregates(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/kegs", r.URL.Path)
		require.Equal(t, "Bearer remote-tok", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode([]tapper.HubKeg{
			{Namespace: "jlrickert", Alias: "example", Role: "admin"},
			{Namespace: "shared", Alias: "docs", Role: "editor"},
		})
	}))
	defer srv.Close()

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	// HubListKegs with no --hub aggregates configured remote hubs.
	cfg := fmt.Sprintf("hubs:\n"+
		"  atlas:\n    kind: remote\n    url: %s\n    token: remote-tok\n", srv.URL)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(cfg), 0o644))

	kegs, err := tap.HubListKegs(fx.Context(), tapper.HubListOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"@jlrickert/example", "@shared/docs"}, kegs)
}
