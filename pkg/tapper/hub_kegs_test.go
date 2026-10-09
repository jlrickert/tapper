package tapper_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"testing"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestCreateKeg_Success(t *testing.T) {
	t.Parallel()

	var gotAuth, gotPath, gotBody string
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		require.Equal(t, "/api/v1/kegs", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]tapper.HubKeg{
			{Namespace: "jlrickert", Alias: "example", Title: "Example", Description: "Example summary.", Visibility: "private", Role: "admin"},
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
	require.Equal(t, "Example", kegs[0].Title)
	require.Equal(t, "Example summary.", kegs[0].Description)
	require.Equal(t, "admin", kegs[0].Role)
}

func TestListUserKegs_Forbidden(t *testing.T) {
	t.Parallel()

	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := tapper.ListUserKegs(context.Background(), srv.URL, "tok")
	require.Error(t, err)
	require.True(t, errors.Is(err, keg.ErrForbidden))
}

func TestRenameKeg_Success(t *testing.T) {
	t.Parallel()

	var gotAuth, gotMethod, gotPath string
	var gotBody map[string]string
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]string{"namespace": "jlrickert", "alias": "renamed"})
	}))
	defer srv.Close()

	err := tapper.RenameKeg(context.Background(), srv.URL, "tok123", "jlrickert", "example", "renamed")
	require.NoError(t, err)
	require.Equal(t, "Bearer tok123", gotAuth)
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "/api/v1/@jlrickert/kegs/example/rename", gotPath)
	require.Equal(t, map[string]string{"alias": "renamed"}, gotBody)
}

func TestCreateKeg_ForbiddenPreservesExplanation(t *testing.T) {
	server := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "FORBIDDEN", "error": "namespace membership required"})
	}))
	defer server.Close()
	err := tapper.CreateKeg(context.Background(), server.URL, "token", "org", "notes", "", "")
	require.ErrorIs(t, err, keg.ErrForbidden)
	require.NotErrorIs(t, err, tapper.ErrTokenRejected)
	require.ErrorContains(t, err, "namespace membership required")
}

func TestCreateHubKeg_SendsKegVersion(t *testing.T) {
	t.Parallel()

	var body map[string]any
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	err := tapper.CreateHubKeg(context.Background(), srv.URL, "tok", "acme", tapper.HubKegCreate{Alias: "graph", KegVersion: keg.KegVersionV2})
	require.NoError(t, err)
	require.Equal(t, keg.KegVersionV2, body["kegv"])
	require.Equal(t, "graph", body["alias"])
}

func TestCreateKeg_OmitsKegVersionByDefault(t *testing.T) {
	t.Parallel()

	var body map[string]any
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	require.NoError(t, tapper.CreateKeg(context.Background(), srv.URL, "tok", "acme", "notes", "", ""))
	_, present := body["kegv"]
	require.False(t, present, "an unversioned create must not send kegv, so Hubs that predate versioned KEGs accept it")
}

func TestCreateHubKeg_BadRequestMapsToErrInvalid(t *testing.T) {
	t.Parallel()

	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "KEGv2 preview is disabled", "code": "INVALID"})
	}))
	defer srv.Close()

	err := tapper.CreateHubKeg(context.Background(), srv.URL, "tok", "acme", tapper.HubKegCreate{Alias: "graph", KegVersion: keg.KegVersionV2})
	require.ErrorIs(t, err, keg.ErrInvalid)
	require.ErrorContains(t, err, "KEGv2 preview is disabled")
}

func TestListUserKegs_DecodesKegVersion(t *testing.T) {
	t.Parallel()

	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"namespace":"acme","alias":"graph","kegv":"2026-09"},{"namespace":"acme","alias":"notes"}]`))
	}))
	defer srv.Close()

	kegs, err := tapper.ListUserKegs(context.Background(), srv.URL, "tok")
	require.NoError(t, err)
	require.Equal(t, keg.KegVersionV2, kegs[0].KegVersion)
	require.Empty(t, kegs[1].KegVersion, "an unreported version stays empty and reads as KEGv1")
}

func TestKegTitleWithVersion(t *testing.T) {
	t.Parallel()

	require.Equal(t, "Notes", tapper.KegTitleWithVersion("Notes", ""))
	require.Equal(t, "Notes", tapper.KegTitleWithVersion("Notes", keg.KegVersionV1))
	require.Equal(t, "Graph (KEGv2 preview)", tapper.KegTitleWithVersion("Graph", keg.KegVersionV2))
	require.Equal(t, "KEGv2 preview", tapper.KegTitleWithVersion(" ", keg.KegVersionV2))
}

func TestNormalizeKegVersion(t *testing.T) {
	t.Parallel()

	v, err := tapper.NormalizeKegVersion(" 2026-09 ")
	require.NoError(t, err)
	require.Equal(t, keg.KegVersionV2, v)
	v, err = tapper.NormalizeKegVersion("")
	require.NoError(t, err)
	require.Empty(t, v)
	_, err = tapper.NormalizeKegVersion("2027-01")
	require.ErrorIs(t, err, keg.ErrInvalid)
	require.ErrorContains(t, err, "keg version")
}
