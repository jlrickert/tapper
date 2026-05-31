package tapper_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestValidateToken_Success(t *testing.T) {
	t.Parallel()
	var gotAuth string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/whoami", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"user_id":  42,
			"username": "alice",
			"email":    "alice@example.com",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	rt, _ := toolkit.NewRuntime()
	who, err := tapper.ValidateToken(context.Background(), rt, srv.URL, "thub_abc")
	require.NoError(t, err)
	require.Equal(t, "Bearer thub_abc", gotAuth, "the token must be sent as a bearer credential")
	require.Equal(t, int64(42), who.UserID)
	require.Equal(t, "alice", who.Username)
	require.Equal(t, "alice@example.com", who.Email)
}

func TestValidateToken_Unauthorized(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/whoami", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	rt, _ := toolkit.NewRuntime()
	_, err := tapper.ValidateToken(context.Background(), rt, srv.URL, "bad")
	require.Error(t, err)
	require.Contains(t, err.Error(), "rejected the token")
}

func TestValidateToken_OtherStatusSurfacesCode(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/whoami", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	rt, _ := toolkit.NewRuntime()
	_, err := tapper.ValidateToken(context.Background(), rt, srv.URL, "tok")
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
}

func TestValidateToken_EmptyToken(t *testing.T) {
	t.Parallel()
	rt, _ := toolkit.NewRuntime()
	_, err := tapper.ValidateToken(context.Background(), rt, "https://hub.example.com", "  ")
	require.Error(t, err)
	require.Contains(t, err.Error(), "token is required")
}

func TestValidateToken_BadHubURL(t *testing.T) {
	t.Parallel()
	rt, _ := toolkit.NewRuntime()
	_, err := tapper.ValidateToken(context.Background(), rt, "ftp://hub.example.com", "tok")
	require.Error(t, err)
	require.Contains(t, err.Error(), "http or https")
}
