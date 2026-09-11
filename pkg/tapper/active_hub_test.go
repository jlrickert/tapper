package tapper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestActiveHubPinnedURLAndNewConnectionSelection(t *testing.T) {
	var firstCalls, secondCalls atomic.Int32
	hub := func(calls *atomic.Int32) *httptest.Server {
		return testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
			switch r.URL.Path {
			case "/api/v1/whoami":
				json.NewEncoder(w).Encode(WhoAmI{UserID: 1, Username: "team", DefaultNamespace: "team"})
			case "/api/v1/kegs":
				json.NewEncoder(w).Encode([]HubKeg{{Namespace: "team", Alias: "notes", Role: "admin"}})
			case "/api/v1/flights":
				json.NewEncoder(w).Encode([]HubFlight{})
			default:
				http.NotFound(w, r)
			}
		}))
	}
	first := hub(&firstCalls)
	defer first.Close()
	second := hub(&secondCalls)
	defer second.Close()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	rt := sb.Runtime()
	ctx := context.Background()
	tap, err := NewTap(TapOptions{Runtime: rt, Root: "/workspace"})
	require.NoError(t, err)
	config := func(active, a string) []byte {
		return []byte(fmt.Sprintf("hub: %s\nkeg: '@team/notes'\nhubs:\n  a: {url: %s}\n  b: {url: %s}\n", active, a, second.URL))
	}
	require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), config("a", first.URL), 0644))
	store, err := LoadAuthStore(ctx, rt, tap.PathService.AuthStorePath())
	require.NoError(t, err)
	store.Set(first.URL, AuthEntry{AccessToken: "token"})
	store.Set(second.URL, AuthEntry{AccessToken: "token"})
	require.NoError(t, store.Save(ctx, rt, tap.PathService.AuthStorePath()))
	require.NoError(t, tap.ConfigService.PinHub())
	direct, err := tap.ConfigService.ResolveTarget(first.URL+"/api/v1/@team/kegs/notes", "", "")
	require.NoError(t, err)
	require.Equal(t, first.URL, direct.HubURL)
	check := func(tap *Tap) {
		_, err := tap.HubListKegs(ctx, HubListOptions{})
		require.NoError(t, err)
		_, err = tap.SelectedHubIdentity(ctx)
		require.NoError(t, err)
		_, err = tap.ListFlights(ctx, ListFlightsOptions{})
		require.NoError(t, err)
	}
	check(tap)
	require.EqualValues(t, 3, firstCalls.Load())
	require.Zero(t, secondCalls.Load())
	require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), config("b", second.URL), 0644))
	tap.ConfigService.Reload()
	check(tap)
	require.EqualValues(t, 6, firstCalls.Load())
	require.Zero(t, secondCalls.Load())
	_, err = tap.ConfigService.ResolveTarget(second.URL+"/api/v1/@team/kegs/notes", "", "")
	require.ErrorIs(t, err, keg.ErrOrientationDenied)
	_, err = tap.HubListKegs(ctx, HubListOptions{Hub: "b"})
	require.ErrorIs(t, err, keg.ErrOrientationDenied)
	require.Zero(t, secondCalls.Load())
	next, err := NewTap(TapOptions{Runtime: rt, Root: "/workspace"})
	require.NoError(t, err)
	check(next)
	require.EqualValues(t, 6, firstCalls.Load())
	require.EqualValues(t, 3, secondCalls.Load())
	// A dead inactive Hub remains irrelevant for the new connection.
	first.Close()
	check(next)
	require.EqualValues(t, 6, secondCalls.Load())
}
