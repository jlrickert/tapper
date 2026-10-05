package keg_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"testing"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestRemoteKegDocumentWritesSendIfMatch(t *testing.T) {
	t.Parallel()
	wants := map[string]string{
		"PUT /settings":        "settings-hash",
		"PUT /schemas/task":    "schema-write-hash",
		"DELETE /schemas/task": "schema-delete-hash",
	}
	seen := map[string]string{}
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.Method+" "+r.URL.Path] = r.Header.Get("If-Match")
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	rk := keg.NewRemoteKeg(srv.URL, "", nil)
	ctx := context.Background()

	require.NoError(t, rk.SetSettings(ctx, []byte("kegv: 2025-07\n"), keg.SettingsWriteOptions{ExpectedHash: wants["PUT /settings"]}))
	require.NoError(t, rk.WriteSchema(ctx, "task", []byte("type: task\n"), keg.SchemaWriteOptions{ExpectedHash: wants["PUT /schemas/task"]}))
	require.NoError(t, rk.DeleteSchema(ctx, "task", keg.SchemaWriteOptions{ExpectedHash: wants["DELETE /schemas/task"]}))
	require.Equal(t, wants, seen)
}

func TestRemoteKegDecodesPreconditionErrorsWithRecoveryFields(t *testing.T) {
	t.Parallel()
	t.Run("required", func(t *testing.T) {
		srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPreconditionRequired)
			_, _ = w.Write([]byte(`{"error":"If-Match is required","code":"PRECONDITION_REQUIRED","operationPerformed":false}`))
		}))
		t.Cleanup(srv.Close)
		rk := keg.NewRemoteKeg(srv.URL, "", nil)
		err := rk.SetSettings(context.Background(), []byte("kegv: 2025-07\n"), keg.SettingsWriteOptions{})
		require.ErrorIs(t, err, keg.ErrPreconditionRequired)
	})

	t.Run("conflict", func(t *testing.T) {
		srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte(`{"error":"stale","code":"CONFLICT","operationPerformed":false,"currentHash":"fresh","currentContent":"type: task\n"}`))
		}))
		t.Cleanup(srv.Close)
		rk := keg.NewRemoteKeg(srv.URL, "", nil)
		err := rk.WriteSchema(context.Background(), "task", []byte("type: task\nsummary: stale\n"), keg.SchemaWriteOptions{ExpectedHash: "stale"})
		require.ErrorIs(t, err, keg.ErrConflict)
		var conflict *keg.PreconditionConflictError
		require.True(t, errors.As(err, &conflict))
		require.Equal(t, "fresh", conflict.CurrentHash)
		require.Equal(t, "type: task\n", string(conflict.CurrentContent))
	})
}

func TestRemoteKegUpdateNodesSendsPartTokensAndDecodesPartConflicts(t *testing.T) {
	t.Parallel()
	var body map[string]any
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"error":"stale","code":"CONFLICT","operationPerformed":false,"currentHash":"fresh","currentContentHash":"c2","currentMetaHash":"m2","currentContent":"# Now\n"}`))
	}))
	t.Cleanup(srv.Close)
	rk := keg.NewRemoteKeg(srv.URL, "", nil)
	_, err := rk.UpdateNodes(context.Background(), []keg.NodeUpdateOptions{{
		ID: keg.NodeId{ID: 3}, Content: []byte("# New\n"), HasContent: true, ExpectedContentHash: "c1",
	}})
	require.ErrorIs(t, err, keg.ErrConflict)
	var conflict *keg.PreconditionConflictError
	require.True(t, errors.As(err, &conflict))
	require.Equal(t, "fresh", conflict.CurrentHash)
	require.Equal(t, "c2", conflict.CurrentContentHash)
	require.Equal(t, "m2", conflict.CurrentMetaHash)

	updates := body["updates"].([]any)
	require.Len(t, updates, 1)
	update := updates[0].(map[string]any)
	require.Equal(t, "c1", update["expected_content_hash"])
	require.NotContains(t, update, "expected_meta_hash")
	require.NotContains(t, update, "expected_hash")
}
