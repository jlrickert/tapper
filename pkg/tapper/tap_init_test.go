package tapper_test

import (
	"encoding/json"
	"fmt"
	"net/http"

	"testing"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// remoteInitConfig configures a remote Hub with retired namespace settings.
func remoteInitConfig(srvURL string) string {
	return fmt.Sprintf("defaultNamespace: ignored\n"+
		"namespaces:\n  teamns:\n    hub: atlas\n"+
		"hubs:\n  atlas:\n    kind: remote\n    url: %s\n    tokenEnv: TEST_TOK\n", srvURL)
}

func TestInitKeg_RemoteCreate_Success(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	require.NoError(t, fx.Runtime().Env().Set("TEST_TOK", "tok"))

	var gotPath string
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"namespace": "teamns", "alias": "example"})
	}))
	defer srv.Close()

	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(remoteInitConfig(srv.URL)), 0o644))

	// The qualified reference determines the creation destination.
	target, err := tap.InitKeg(fx.Context(), tapper.InitOptions{Keg: "@teamns/example"})
	require.NoError(t, err)
	require.Equal(t, "/api/v1/@teamns/kegs", gotPath)
	require.Equal(t, "atlas", target.Hub)
	require.Equal(t, "teamns", target.Namespace)
	require.Equal(t, "example", target.KegName)

	// Creation leaves the saved configuration intact.
	cfg := string(fx.MustReadFile(tap.PathService.UserConfig()))
	require.Contains(t, cfg, "teamns:")
	require.Contains(t, cfg, "hub: atlas")
}

func TestInitKeg_RemoteCreate_IgnoresRetiredHubNamespace(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	require.NoError(t, fx.Runtime().Env().Set("TEST_TOK", "tok"))

	var gotPath string
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"namespace": "teamns", "alias": "example"})
	}))
	defer srv.Close()

	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	cfg := fmt.Sprintf("hub: atlas\n"+
		"hubs:\n  atlas:\n    kind: remote\n    defaultNamespace: ignored\n    url: %s\n    tokenEnv: TEST_TOK\n", srv.URL)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(cfg), 0o644))

	target, err := tap.InitKeg(fx.Context(), tapper.InitOptions{Keg: "@teamns/example", RequireBootstrap: true})
	require.NoError(t, err)
	require.Equal(t, "/api/v1/@teamns/kegs", gotPath)
	require.Equal(t, "atlas", target.Hub)
	require.Equal(t, "teamns", target.Namespace)
}

func TestInitKeg_RemoteFallbackHubWithoutNamespaceDoesNotFallBackLocal(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	require.NoError(t, fx.Runtime().Env().Set("TEST_TOK", "tok"))

	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("remote create should fail before contacting the hub when no namespace is configured: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	cfg := fmt.Sprintf("hub: atlas\n"+
		"hubs:\n  atlas:\n    kind: remote\n    url: %s\n    tokenEnv: TEST_TOK\n", srv.URL)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(cfg), 0o644))

	_, err = tap.InitKeg(fx.Context(), tapper.InitOptions{Keg: "example", RequireBootstrap: true})
	require.Error(t, err)
	require.Contains(t, err.Error(), "expected @namespace/keg")
	_, statErr := fx.Runtime().Stat("/home/testuser/.local/share/tapper/kegs/@local/example/keg", false)
	require.Error(t, statErr, "remote bootstrap state must not silently create a local keg")
}

func TestInitKeg_RemoteCreate_Conflict(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	require.NoError(t, fx.Runtime().Env().Set("TEST_TOK", "tok"))

	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "keg already exists", "code": "CONFLICT"})
	}))
	defer srv.Close()

	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(remoteInitConfig(srv.URL)), 0o644))

	_, err = tap.InitKeg(fx.Context(), tapper.InitOptions{Keg: "@teamns/example"})
	require.Error(t, err)
	require.ErrorIs(t, err, keg.ErrExist, "an existing remote keg must fail with ErrExist (409)")
}
