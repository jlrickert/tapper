package keg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOrientationRoutingForRequestsAndEvents(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "root", true: "foreign"}[foreign], func(t *testing.T) {
			count := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count++
				require.Equal(t, "Bearer destination-token", r.Header.Get("Authorization"))
				require.Equal(t, !foreign, r.Header.Get(OrientationHeaderName) != "")
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": "ORIENTATION_STALE", "error": "authority changed"})
			}))
			defer server.Close()
			base := server.URL + "/api/v1/@team/kegs/notes"
			root := server.URL
			if foreign {
				root = "https://root.example"
			}
			ctx := WithOrientationState(context.Background(), OrientationState{Root: "@team/+root", Active: "@team/+root", Revision: "revision", RootHub: root, AllowedTargets: []string{base}})
			remote := NewRemoteKeg(base, "destination-token", nil)
			_, err := remote.Settings(ctx)
			if foreign {
				require.ErrorIs(t, err, ErrOrientationDenied)
				_, err = remote.Watch(ctx, NodeId{ID: 1})
				require.ErrorIs(t, err, ErrOrientationDenied)
				_, err = remote.do(ctx, http.MethodPost, "/nodes", nil, "", nil)
				require.ErrorIs(t, err, ErrOrientationDenied)
				require.Zero(t, count)
				return
			}

			require.ErrorIs(t, err, ErrOrientationStale)
			require.Equal(t, 1, count, "same-Hub rejection must never retry without its header")
			require.Equal(t, !foreign, remote.eventsHeader(ctx).Get(OrientationHeaderName) != "")
			require.Equal(t, "Bearer destination-token", remote.eventsHeader(ctx).Get("Authorization"))
			watchCtx, cancel := context.WithTimeout(ctx, time.Second)
			defer cancel()
			events, err := remote.Watch(watchCtx, NodeId{ID: 1})
			require.NoError(t, err)
			select {
			case _, open := <-events:
				require.False(t, open)
			case <-watchCtx.Done():
				t.Fatal("watch did not terminate on stale orientation")
			}
			require.Equal(t, 2, count, "event handshake must use the same routing and never drop a rejected header")
			state, _ := OrientationStateFromContext(ctx)
			state.AllowedTargets = []string{}
			denied := WithOrientationState(ctx, state)
			_, err = remote.do(denied, http.MethodPost, "/nodes", nil, "", nil)
			require.ErrorIs(t, err, ErrOrientationDenied)
			_, err = remote.Watch(denied, NodeId{ID: 1})
			require.ErrorIs(t, err, ErrOrientationDenied)
			require.Equal(t, 2, count, "denied mutations and streams dispatch nothing")
		})
	}
}

func TestGovernedRequestsDoNotFollowRedirects(t *testing.T) {
	calls := 0
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusNoContent) }))
	defer foreign.Close()
	root := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer root.Close()
	base := root.URL + "/api/v1/@team/kegs/notes"
	ctx := WithOrientationState(context.Background(), OrientationState{Root: "@team/+root", Active: "@team/+root", Revision: "revision", RootHub: root.URL, AllowedTargets: []string{base}})
	remote := NewRemoteKeg(base, "root-token", nil)
	response, err := remote.do(ctx, http.MethodPost, "/nodes", nil, "", nil)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusTemporaryRedirect, response.StatusCode)
	require.Zero(t, calls, "neither credentials, orientation, nor mutations may follow a redirect")
}
