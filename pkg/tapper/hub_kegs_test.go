package tapper_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestCreateKeg_Success(t *testing.T) {
	t.Parallel()

	var gotAuth, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotBody = body["alias"]
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"namespace": "jlrickert", "alias": "example"})
	}))
	defer srv.Close()

	err := tapper.CreateKeg(context.Background(), srv.URL, "tok123", "jlrickert", "example", "My Example", "private")
	require.NoError(t, err)
	require.Equal(t, "Bearer tok123", gotAuth)
	require.Equal(t, "/api/v1/@jlrickert/kegs", gotPath)
	require.Equal(t, "example", gotBody)
}

func TestCreateKeg_Conflict(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "keg already exists", "code": "CONFLICT"})
	}))
	defer srv.Close()

	err := tapper.CreateKeg(context.Background(), srv.URL, "tok", "jlrickert", "example", "", "")
	require.Error(t, err)
	require.ErrorIs(t, err, keg.ErrExist, "409 must map to keg.ErrExist so callers detect 'already exists'")
}

func TestCreateKeg_Unauthorized(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	err := tapper.CreateKeg(context.Background(), srv.URL, "bad", "jlrickert", "example", "", "")
	require.Error(t, err)
	require.ErrorIs(t, err, tapper.ErrTokenRejected)
}

func TestListUserKegs_Success(t *testing.T) {
	t.Parallel()

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		require.Equal(t, "/api/v1/kegs", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]tapper.HubKeg{
			{Namespace: "jlrickert", Alias: "example", Visibility: "private", Role: "admin"},
			{Namespace: "shared", Alias: "docs", Visibility: "public", Role: "editor"},
		})
	}))
	defer srv.Close()

	kegs, err := tapper.ListUserKegs(context.Background(), srv.URL, "tok")
	require.NoError(t, err)
	require.Equal(t, "Bearer tok", gotAuth)
	require.Len(t, kegs, 2)
	require.Equal(t, "jlrickert", kegs[0].Namespace)
	require.Equal(t, "example", kegs[0].Alias)
	require.Equal(t, "admin", kegs[0].Role)
}

func TestListUserKegs_Unauthorized(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := tapper.ListUserKegs(context.Background(), srv.URL, "tok")
	require.Error(t, err)
	require.True(t, errors.Is(err, tapper.ErrTokenRejected))
}

func TestOrientationEndpoints(t *testing.T) {
	t.Parallel()

	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/v1/orient":
			_ = json.NewEncoder(w).Encode([]tapper.HubOrientationKeg{{
				Namespace: "foldwise",
				Alias:     "dev",
				Title:     "Development",
				Summary:   "Engineering system of record.",
				Role:      "admin",
			}})
		case "/api/v1/orient/details":
			var body struct {
				Kegs []string `json:"kegs"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, []string{"@foldwise/dev"}, body.Kegs)
			_ = json.NewEncoder(w).Encode([]tapper.HubOrientationDetail{{
				Keg:          "@foldwise/dev",
				Title:        "Development",
				Summary:      "Engineering system of record.",
				Updated:      "2026-07-29T00:00:00Z",
				Instructions: "Operate carefully.",
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	discovered, err := tapper.DiscoverOrientationKegs(context.Background(), srv.URL, "tok")
	require.NoError(t, err)
	require.Equal(t, "Development", discovered[0].Title)
	require.Equal(t, "Engineering system of record.", discovered[0].Summary)

	details, err := tapper.FetchOrientationDetails(context.Background(), srv.URL, "tok", []string{"@foldwise/dev"})
	require.NoError(t, err)
	require.Equal(t, "Operate carefully.", details[0].Instructions)
	require.Equal(t, []string{"GET /api/v1/orient", "POST /api/v1/orient/details"}, requests)
}

func TestOrientationEndpoints_UnsupportedAndUnavailableAreDistinct(t *testing.T) {
	t.Parallel()

	oldHub := httptest.NewServer(http.NotFoundHandler())
	defer oldHub.Close()
	_, err := tapper.DiscoverOrientationKegs(context.Background(), oldHub.URL, "tok")
	require.ErrorIs(t, err, tapper.ErrOrientationUnsupported)
	_, err = tapper.FetchOrientationDetails(context.Background(), oldHub.URL, "tok", []string{"@foldwise/dev"})
	require.ErrorIs(t, err, tapper.ErrOrientationUnsupported)

	newHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "one or more requested kegs are unavailable",
			"code":  "UNAVAILABLE",
		})
	}))
	defer newHub.Close()
	_, err = tapper.FetchOrientationDetails(context.Background(), newHub.URL, "tok", []string{"@foldwise/dev"})
	require.Error(t, err)
	require.NotErrorIs(t, err, tapper.ErrOrientationUnsupported)
	require.Contains(t, err.Error(), "unavailable")
}

func TestRenameKeg_Success(t *testing.T) {
	t.Parallel()

	var gotAuth, gotPath string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]string{"namespace": "jlrickert", "alias": "renamed"})
	}))
	defer srv.Close()

	err := tapper.RenameKeg(context.Background(), srv.URL, "tok123", "jlrickert", "example", "renamed")
	require.NoError(t, err)
	require.Equal(t, "Bearer tok123", gotAuth)
	require.Equal(t, "/api/v1/@jlrickert/kegs/example/settings", gotPath)
	require.Equal(t, map[string]string{"alias": "renamed"}, gotBody)
}
