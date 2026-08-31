package tapper_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestKegSettingsBatch_UsesOrdinarySettingsAndPreservesInputOrder(t *testing.T) {
	t.Parallel()
	var aCalls, bCalls atomic.Int32
	newHub := func(calls *atomic.Int32, prefix string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			require.Equal(t, http.MethodGet, r.Method)
			require.True(t, strings.HasSuffix(r.URL.Path, "/settings"), r.URL.Path)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"kegv": "2025-07", "title": prefix + " " + r.URL.Path,
				"summary": "summary " + r.URL.Path, "instructions": "instructions " + r.URL.Path,
			})
		}))
	}
	hubA := newHub(&aCalls, "A")
	defer hubA.Close()
	hubB := newHub(&bCalls, "B")
	defer hubB.Close()

	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	cfg := fmt.Sprintf(`hubs:
  a: {kind: remote, url: %s, token: token-a}
  b: {kind: remote, url: %s, token: token-b}
namespaces:
  team-a: a
  team-b: b
`, hubA.URL, hubB.URL)
	require.NoError(t, sb.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(cfg), 0o644))

	refs := []string{"@team-a/one", "@team-b/two", "@team-a/three"}
	out, err := tap.KegSettings(sb.Context(), tapper.KegSettingsOptions{
		Kegs:    refs,
		Minimal: true,
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, aCalls.Load())
	require.EqualValues(t, 1, bCalls.Load())
	first := strings.Index(out, "keg: '@team-a/one'")
	second := strings.Index(out, "keg: '@team-b/two'")
	third := strings.Index(out, "keg: '@team-a/three'")
	require.GreaterOrEqual(t, first, 0)
	require.Greater(t, second, first)
	require.Greater(t, third, second)
}

func TestKegSettingsBatch_NeverCallsRemovedOrientationRoute(t *testing.T) {
	t.Parallel()
	var settingsCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/@legacy/kegs/one/settings":
			settingsCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kegv": "2025-07", "title": "One", "instructions": "One guidance",
			})
		case "/api/v1/@legacy/kegs/two/settings":
			settingsCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kegv": "2025-07", "title": "Two", "instructions": "Two guidance",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	cfg := fmt.Sprintf(`hubs:
  legacy: {kind: remote, url: %s, token: token}
namespaces:
  legacy: legacy
`, srv.URL)
	require.NoError(t, sb.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(cfg), 0o644))

	out, err := tap.KegSettings(sb.Context(), tapper.KegSettingsOptions{
		Kegs:    []string{"@legacy/one", "@legacy/two"},
		Minimal: true,
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, settingsCalls.Load())
	require.Contains(t, out, "One guidance")
	require.Contains(t, out, "Two guidance")
}
