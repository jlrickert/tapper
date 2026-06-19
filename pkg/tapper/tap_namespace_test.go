package tapper_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestNamespaceList(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/v1/namespaces", r.URL.Path)
		require.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode([]tapper.HubNamespace{
			{Name: "jlrickert", Kind: "user", Role: "owner"},
			{Name: "acme", Kind: "org", Role: "admin"},
		})
	})
	tap, fx, _ := newRemoteHubTap(t, h)
	nss, err := tap.NamespaceList(fx.Context(), tapper.NamespaceListOptions{})
	require.NoError(t, err)
	require.Equal(t, []tapper.HubNamespace{
		{Name: "jlrickert", Kind: "user", Role: "owner"},
		{Name: "acme", Kind: "org", Role: "admin"},
	}, nss)
}

func TestNamespaceMembers(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/v1/@acme/members", r.URL.Path)
		_ = json.NewEncoder(w).Encode([]tapper.HubMember{
			{Username: "alice", Role: "owner"},
			{Username: "bob", Role: "member"},
		})
	})
	tap, fx, _ := newRemoteHubTap(t, h)
	members, err := tap.NamespaceMembers(fx.Context(), tapper.NamespaceMembersOptions{Namespace: "acme"})
	require.NoError(t, err)
	require.Equal(t, []tapper.HubMember{
		{Username: "alice", Role: "owner"},
		{Username: "bob", Role: "member"},
	}, members)
}

func TestNamespaceAddMember(t *testing.T) {
	t.Parallel()
	var gotBody map[string]string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/@acme/members", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"username": "bob", "role": "member"})
	})
	tap, fx, _ := newRemoteHubTap(t, h)
	require.NoError(t, tap.NamespaceAddMember(fx.Context(), tapper.NamespaceAddMemberOptions{Namespace: "acme", User: "@bob", Role: "member"}))
	require.Equal(t, map[string]string{"username": "bob", "role": "member"}, gotBody)
}

func TestNamespaceAddMember_InvalidRole(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("hub should not be contacted for an invalid role")
	})
	tap, fx, _ := newRemoteHubTap(t, h)
	// "viewer" is a keg grant role, not a namespace membership role.
	err := tap.NamespaceAddMember(fx.Context(), tapper.NamespaceAddMemberOptions{Namespace: "acme", User: "bob", Role: "viewer"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid role")
}

func TestNamespaceSetRole(t *testing.T) {
	t.Parallel()
	var gotBody map[string]string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPatch, r.Method)
		require.Equal(t, "/api/v1/@acme/members/@bob", r.URL.Path)
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_ = json.NewEncoder(w).Encode(map[string]string{"username": "bob", "role": "admin"})
	})
	tap, fx, _ := newRemoteHubTap(t, h)
	require.NoError(t, tap.NamespaceSetRole(fx.Context(), tapper.NamespaceSetRoleOptions{Namespace: "acme", User: "bob", Role: "admin"}))
	require.Equal(t, map[string]string{"role": "admin"}, gotBody)
}

func TestNamespaceRemoveMember(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.Equal(t, "/api/v1/@acme/members/@bob", r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	})
	tap, fx, _ := newRemoteHubTap(t, h)
	require.NoError(t, tap.NamespaceRemoveMember(fx.Context(), tapper.NamespaceRemoveMemberOptions{Namespace: "acme", User: "bob"}))
}

func TestNamespaceCreate(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("namespace create should hand off to the UI, not call the hub API: %s %s", r.Method, r.URL.Path)
	})
	tap, fx, srv := newRemoteHubTap(t, h)
	ns, err := tap.NamespaceCreate(fx.Context(), tapper.NamespaceCreateOptions{Name: "acme"})
	require.NoError(t, err)
	require.Equal(t, &tapper.NamespaceCreateResult{
		Name: "acme",
		Hub:  "atlas",
		URL:  srv.URL + "/namespaces/new?name=acme",
	}, ns)
}

func TestCreateNamespaceDisabled(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("CreateNamespace should not call the hub API: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	ns, err := tapper.CreateNamespace(context.Background(), srv.URL, "tok", "acme")
	require.Nil(t, ns)
	require.ErrorIs(t, err, kegpkg.ErrNotSupported)
	require.Contains(t, err.Error(), "disabled for remote clients")
}
