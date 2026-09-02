package tapper_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
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
	res, err := tap.NamespaceList(fx.Context(), tapper.NamespaceListOptions{})
	require.NoError(t, err)
	// Rows are sorted by hub then namespace, and each names its source hub.
	require.Equal(t, []tapper.HubNamespace{
		{Name: "acme", Kind: "org", Role: "admin", Hub: "atlas"},
		{Name: "jlrickert", Kind: "user", Role: "owner", Hub: "atlas"},
	}, res.Namespaces)
	require.Empty(t, res.Warnings)
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

// newTwoHubTap wires two independent hub servers into one config, so aggregate
// listing has something to aggregate.
func newTwoHubTap(t *testing.T, atlas, homelab http.Handler) (*tapper.Tap, *sandbox.Sandbox) {
	t.Helper()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	atlasSrv := httptest.NewServer(atlas)
	t.Cleanup(atlasSrv.Close)
	homelabSrv := httptest.NewServer(homelab)
	t.Cleanup(homelabSrv.Close)

	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	cfg := fmt.Sprintf("hubs:\n"+
		"  atlas:\n    kind: remote\n    url: %s\n    token: tok\n"+
		"  homelab:\n    kind: remote\n    url: %s\n    token: tok\n"+
		"defaultHub: atlas\n", atlasSrv.URL, homelabSrv.URL)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(cfg), 0o644))
	return tap, fx
}

func namespaceHandler(t *testing.T, nss ...tapper.HubNamespace) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/namespaces", r.URL.Path)
		_ = json.NewEncoder(w).Encode(nss)
	})
}

// TestNamespaceList_AggregatesEveryHub covers tapper#73: listing showed only the
// selected hub's memberships, hiding every other configured hub and giving no
// way to attribute a row.
func TestNamespaceList_AggregatesEveryHub(t *testing.T) {
	t.Parallel()
	tap, fx := newTwoHubTap(t,
		namespaceHandler(t, tapper.HubNamespace{Name: "foldwise", Kind: "org", Role: "owner"}),
		namespaceHandler(t, tapper.HubNamespace{Name: "homestuff", Kind: "org", Role: "member"}),
	)

	res, err := tap.NamespaceList(fx.Context(), tapper.NamespaceListOptions{})
	require.NoError(t, err)
	require.Empty(t, res.Warnings)
	require.Equal(t, []tapper.HubNamespace{
		{Name: "foldwise", Kind: "org", Role: "owner", Hub: "atlas"},
		{Name: "homestuff", Kind: "org", Role: "member", Hub: "homelab"},
	}, res.Namespaces)
}

// TestNamespaceList_SameNameOnTwoHubsStaysDistinct guards the acceptance
// criterion that identical namespace names remain distinguishable.
func TestNamespaceList_SameNameOnTwoHubsStaysDistinct(t *testing.T) {
	t.Parallel()
	tap, fx := newTwoHubTap(t,
		namespaceHandler(t, tapper.HubNamespace{Name: "shared", Kind: "org", Role: "owner"}),
		namespaceHandler(t, tapper.HubNamespace{Name: "shared", Kind: "org", Role: "member"}),
	)

	res, err := tap.NamespaceList(fx.Context(), tapper.NamespaceListOptions{})
	require.NoError(t, err)
	require.Equal(t, []tapper.HubNamespace{
		{Name: "shared", Kind: "org", Role: "owner", Hub: "atlas"},
		{Name: "shared", Kind: "org", Role: "member", Hub: "homelab"},
	}, res.Namespaces)
}

// TestNamespaceList_UnreachableHubPreservesOthers covers the criterion that one
// bad hub must not blank the listing, and must be named in the report.
func TestNamespaceList_UnreachableHubPreservesOthers(t *testing.T) {
	t.Parallel()
	broken := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	tap, fx := newTwoHubTap(t,
		namespaceHandler(t, tapper.HubNamespace{Name: "foldwise", Kind: "org", Role: "owner"}),
		broken,
	)

	res, err := tap.NamespaceList(fx.Context(), tapper.NamespaceListOptions{})
	require.NoError(t, err)
	require.Equal(t, []tapper.HubNamespace{
		{Name: "foldwise", Kind: "org", Role: "owner", Hub: "atlas"},
	}, res.Namespaces)
	require.Len(t, res.Warnings, 1)
	require.Contains(t, res.Warnings[0], "homelab")
}

// TestNamespaceList_ExplicitHubNarrowsAndSurfacesErrors confirms --hub keeps its
// old single-hub behaviour, including propagating that hub's failure.
func TestNamespaceList_ExplicitHubNarrowsAndSurfacesErrors(t *testing.T) {
	t.Parallel()
	broken := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	tap, fx := newTwoHubTap(t,
		namespaceHandler(t, tapper.HubNamespace{Name: "foldwise", Kind: "org", Role: "owner"}),
		broken,
	)

	res, err := tap.NamespaceList(fx.Context(), tapper.NamespaceListOptions{Hub: "atlas"})
	require.NoError(t, err)
	require.Equal(t, []tapper.HubNamespace{
		{Name: "foldwise", Kind: "org", Role: "owner", Hub: "atlas"},
	}, res.Namespaces)

	// An explicit hub surfaces its own error rather than degrading to a warning.
	_, err = tap.NamespaceList(fx.Context(), tapper.NamespaceListOptions{Hub: "homelab"})
	require.Error(t, err)
}
