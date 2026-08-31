package tapper_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestHubFlightWritesSendIfMatch(t *testing.T) {
	t.Parallel()
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.Method] = r.Header.Get("If-Match")
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"namespace":"foldwise","slug":"agent-work","title":"Agent Work","visibility":"private","capabilities":[],"cover":[],"subflights":[],"hash":"next"}`))
	}))
	t.Cleanup(srv.Close)

	_, err := tapper.UpdateHubFlight(context.Background(), srv.URL, "tok", "foldwise", "agent-work", tapper.HubFlight{Namespace: "foldwise", Slug: "agent-work", Visibility: "private"}, "update-hash")
	require.NoError(t, err)
	require.NoError(t, tapper.DeleteHubFlight(context.Background(), srv.URL, "tok", "foldwise", "agent-work", "delete-hash"))
	require.Equal(t, "update-hash", seen[http.MethodPut])
	require.Equal(t, "delete-hash", seen[http.MethodDelete])
}

func TestHubFlightWritesDecodePreconditionErrors(t *testing.T) {
	t.Parallel()
	t.Run("required", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusPreconditionRequired)
		}))
		t.Cleanup(srv.Close)
		err := tapper.DeleteHubFlight(context.Background(), srv.URL, "tok", "foldwise", "agent-work", "")
		require.ErrorIs(t, err, keg.ErrPreconditionRequired)
	})

	t.Run("conflict", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte(`{"currentHash":"fresh","currentContent":"title: Current\n","operationPerformed":false}`))
		}))
		t.Cleanup(srv.Close)
		_, err := tapper.UpdateHubFlight(context.Background(), srv.URL, "tok", "foldwise", "agent-work", tapper.HubFlight{}, "stale")
		require.ErrorIs(t, err, keg.ErrConflict)
		var conflict *keg.PreconditionConflictError
		require.ErrorAs(t, err, &conflict)
		require.Equal(t, "fresh", conflict.CurrentHash)
		require.Equal(t, "title: Current\n", string(conflict.CurrentContent))
	})
}
