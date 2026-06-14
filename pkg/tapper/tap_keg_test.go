package tapper_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// newRemoteHubTap builds a Tap whose default namespace `jlrickert` routes to a
// remote hub served by handler (inline token "tok"). Shared by the keg + the
// namespace admin tests.
func newRemoteHubTap(t *testing.T, handler http.Handler) (*tapper.Tap, *sandbox.Sandbox, *httptest.Server) {
	t.Helper()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	cfg := fmt.Sprintf("hubs:\n"+
		"  atlas:\n    kind: remote\n    url: %s\n    token: tok\n"+
		"defaultHub: atlas\n"+
		"defaultNamespace: jlrickert\n"+
		"namespaces:\n  jlrickert:\n    hub: atlas\n", srv.URL)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(cfg), 0o644))
	return tap, fx, srv
}

func TestKegGrants_List(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/v1/@jlrickert/kegs/example/grants", r.URL.Path)
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode([]tapper.HubGrant{{Username: "bob", Role: "editor"}})
	})
	tap, fx, _ := newRemoteHubTap(t, h)
	grants, err := tap.KegGrants(fx.Context(), tapper.KegGrantsOptions{Keg: "@jlrickert/example"})
	require.NoError(t, err)
	require.Equal(t, []tapper.HubGrant{{Username: "bob", Role: "editor"}}, grants)
}

func TestKegGrant_Upsert(t *testing.T) {
	t.Parallel()
	var gotBody map[string]string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/@jlrickert/kegs/example/grants", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"username": "bob", "role": "editor"})
	})
	tap, fx, _ := newRemoteHubTap(t, h)
	require.NoError(t, tap.KegGrant(fx.Context(), tapper.KegGrantOptions{Keg: "@jlrickert/example", User: "@bob", Role: "editor"}))
	require.Equal(t, map[string]string{"username": "bob", "role": "editor"}, gotBody)
}

func TestKegGrant_InvalidRole(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Errorf("hub should not be contacted for an invalid role") })
	tap, fx, _ := newRemoteHubTap(t, h)
	err := tap.KegGrant(fx.Context(), tapper.KegGrantOptions{Keg: "@jlrickert/example", User: "bob", Role: "superuser"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid role")
}

func TestKegRevoke(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/api/v1/@jlrickert/kegs/example/grants/@bob", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})
	tap, fx, _ := newRemoteHubTap(t, h)
	require.NoError(t, tap.KegRevoke(fx.Context(), tapper.KegRevokeOptions{Keg: "@jlrickert/example", User: "bob"}))
}

func TestKegVisibility(t *testing.T) {
	t.Parallel()
	var gotBody map[string]string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPatch, r.Method)
		require.Equal(t, "/api/v1/@jlrickert/kegs/example/settings", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]string{"visibility": "public"})
	})
	tap, fx, _ := newRemoteHubTap(t, h)
	require.NoError(t, tap.KegVisibility(fx.Context(), tapper.KegVisibilityOptions{Keg: "@jlrickert/example", Visibility: "public"}))
	require.Equal(t, map[string]string{"visibility": "public"}, gotBody)
}

func TestKegVisibility_Invalid(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Errorf("hub should not be contacted for an invalid visibility") })
	tap, fx, _ := newRemoteHubTap(t, h)
	err := tap.KegVisibility(fx.Context(), tapper.KegVisibilityOptions{Keg: "@jlrickert/example", Visibility: "secret"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid visibility")
}
