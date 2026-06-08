package tapper_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// remoteInitConfig writes a user config whose default namespace routes bare
// keg names to a remote hub backed by srvURL, authenticated via TEST_TOK.
func remoteInitConfig(srvURL string) string {
	return fmt.Sprintf("defaultNamespace: teamns\n"+
		"namespaces:\n  teamns:\n    hub: atlas\n"+
		"hubs:\n  atlas:\n    kind: remote\n    url: %s\n    tokenEnv: TEST_TOK\n", srvURL)
}

func TestInitKeg_RemoteCreate_Success(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	require.NoError(t, fx.Runtime().Env().Set("TEST_TOK", "tok"))

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"namespace": "teamns", "alias": "example"})
	}))
	defer srv.Close()

	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(remoteInitConfig(srv.URL)), 0o644))

	// A bare name resolves to the default namespace + its hub → remote create.
	target, err := tap.InitKeg(fx.Context(), tapper.InitOptions{Keg: "example"})
	require.NoError(t, err)
	require.Equal(t, "/api/v1/@teamns/kegs", gotPath)
	require.Equal(t, "atlas", target.Hub)
	require.Equal(t, "teamns", target.Namespace)
	require.Equal(t, "example", target.KegName)

	// recordInitKeg no longer writes a kegs alias entry; with the alias table
	// removed it records only the namespace→hub mapping for the remote keg, so
	// future bare-name references route through the namespace-centric chain.
	cfg := string(fx.MustReadFile(tap.PathService.UserConfig()))
	require.Contains(t, cfg, "teamns:")
	require.Contains(t, cfg, "hub: atlas")
}

func TestInitKeg_RemoteCreate_Conflict(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	require.NoError(t, fx.Runtime().Env().Set("TEST_TOK", "tok"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "keg already exists", "code": "CONFLICT"})
	}))
	defer srv.Close()

	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(remoteInitConfig(srv.URL)), 0o644))

	_, err = tap.InitKeg(fx.Context(), tapper.InitOptions{Keg: "example"})
	require.Error(t, err)
	require.ErrorIs(t, err, keg.ErrExist, "an existing remote keg must fail with ErrExist (409)")
}

func TestInitKeg_LocalViaNamespace(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(),
		[]byte("hubs:\n  home:\n    kind: local\n    namespace: local\n    basePath: /home/testuser/kegs\n"), 0o644))

	// The reserved @local namespace pins this machine's filesystem hub.
	target, err := tap.InitKeg(fx.Context(), tapper.InitOptions{Keg: "notes", Namespace: tapper.LocalHubName})
	require.NoError(t, err)
	require.Equal(t, keg.SchemeFile, target.Scheme())
	require.Contains(t, target.String(), "@local/notes")

	// The zero node and keg config were written to disk.
	require.Contains(t, string(fx.MustReadFile("/home/testuser/kegs/@local/notes/keg")), "$schema=")
}
