package tapper_test

import (
	"context"
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

func TestKegSettingsBatch_GroupsByHubAndPreservesInputOrder(t *testing.T) {
	t.Parallel()
	type detailRequest struct {
		Kegs []string `json:"kegs"`
	}
	var aCalls, bCalls atomic.Int32
	newHub := func(calls *atomic.Int32, prefix string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			require.Equal(t, "/api/v1/orient/details", r.URL.Path)
			var request detailRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
			out := make([]tapper.HubOrientationDetail, 0, len(request.Kegs))
			for _, ref := range request.Kegs {
				out = append(out, tapper.HubOrientationDetail{
					Keg:          ref,
					Title:        prefix + " " + ref,
					Summary:      "summary " + ref,
					Instructions: "instructions " + ref,
				})
			}
			_ = json.NewEncoder(w).Encode(out)
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
	require.EqualValues(t, 1, aCalls.Load())
	require.EqualValues(t, 1, bCalls.Load())
	first := strings.Index(out, "keg: '@team-a/one'")
	second := strings.Index(out, "keg: '@team-b/two'")
	third := strings.Index(out, "keg: '@team-a/three'")
	require.GreaterOrEqual(t, first, 0)
	require.Greater(t, second, first)
	require.Greater(t, third, second)
}

func TestKegSettingsBatch_OlderHubFallsBackToPerKegConfig(t *testing.T) {
	t.Parallel()
	var batchCalls, configCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/orient/details":
			batchCalls.Add(1)
			http.NotFound(w, r)
		case "/api/v1/@legacy/kegs/one/config":
			configCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kegv": "2025-07", "title": "One", "instructions": "One guidance",
			})
		case "/api/v1/@legacy/kegs/two/config":
			configCalls.Add(1)
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
	require.EqualValues(t, 1, batchCalls.Load())
	require.EqualValues(t, 2, configCalls.Load())
	require.Contains(t, out, "One guidance")
	require.Contains(t, out, "Two guidance")
}

func TestKegSettingsBatch_HostedResolverCalledOnce(t *testing.T) {
	t.Parallel()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	var calls atomic.Int32
	tap.OrientationDetailsResolver = func(_ context.Context, refs []string) ([]tapper.HubOrientationDetail, error) {
		calls.Add(1)
		out := make([]tapper.HubOrientationDetail, len(refs))
		for i, ref := range refs {
			out[i] = tapper.HubOrientationDetail{Keg: ref, Title: ref}
		}
		return out, nil
	}
	refs := []string{"@foldwise/dev", "@foldwise/engineering"}
	out, err := tap.KegSettings(sb.Context(), tapper.KegSettingsOptions{
		Kegs:    refs,
		Minimal: true,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load())
	require.Less(t, strings.Index(out, refs[0]), strings.Index(out, refs[1]))
}
