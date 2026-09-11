package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"strings"
	"sync"
	"testing"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestTwoHubFlightAuthorityAndRouting(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	revoked, foreignDenied, rootDown := false, false, false
	foreignCalls, mutations := 0, 0
	foreignRole := "admin"
	rootRole := "admin"
	rootFlight := func() *tapper.Flight {
		cover := []tapper.FlightCover{{Namespace: "root", Keg: "notes", Role: tapper.FlightRoleAdmin}}
		if revoked {
			cover = nil
		}
		return &tapper.Flight{Name: "@root/+work", Namespace: "root", Slug: "work", Source: "root-alias", FlightManifest: tapper.FlightManifest{Visibility: "private", Cover: cover}}
	}
	identity, _ := mcp.CanonicalOrientationIdentity(mcp.AuthIdentity{UserID: 1, Username: "root", DefaultNamespace: "root", Namespaces: []string{"root"}})
	rootProjection := func() *mcp.Orientation {
		flight := rootFlight()
		rows := tapper.ProjectOrientationKegs(flight, []tapper.OrientationKeg{{Ref: "@root/notes", Namespace: "root", Alias: "notes", Role: rootRole, Visibility: "private"}})
		projection := &mcp.Orientation{Root: flight, Flight: flight, Path: []string{flight.Name}, Identity: identity, Kegs: rows}
		require.NoError(t, mcp.FinalizeOrientation(projection))
		return projection
	}
	root := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, "Bearer root-token", r.Header.Get("Authorization"))
		if rootDown {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/api/v1/flights":
			f := rootFlight()
			_ = json.NewEncoder(w).Encode([]tapper.HubFlight{{Namespace: f.Namespace, Slug: f.Slug, Visibility: f.Visibility, Capabilities: f.Capabilities, Cover: func() []tapper.HubFlightCover {
				var rows []tapper.HubFlightCover
				for _, c := range f.Cover {
					rows = append(rows, tapper.HubFlightCover{Namespace: c.Namespace, Keg: c.Keg, Role: string(c.Role), Depth: c.Depth})
				}
				return rows
			}()}})
		case "/api/v1/@root/+work":
			f := rootFlight()
			_ = json.NewEncoder(w).Encode(tapper.HubFlight{Namespace: f.Namespace, Slug: f.Slug, Visibility: f.Visibility, Capabilities: f.Capabilities, Cover: func() []tapper.HubFlightCover {
				var rows []tapper.HubFlightCover
				for _, c := range f.Cover {
					rows = append(rows, tapper.HubFlightCover{Namespace: c.Namespace, Keg: c.Keg, Role: string(c.Role), Depth: c.Depth})
				}
				return rows
			}()})
		case "/api/v1/kegs":
			_ = json.NewEncoder(w).Encode([]tapper.HubKeg{{Namespace: "root", Alias: "notes", Role: rootRole, Visibility: "private"}})
		case "/api/v1/@root/kegs/notes/settings":
			state, err := keg.DecodeOrientationState(r.Header.Get(keg.OrientationHeaderName))
			if err != nil || state.Root != "@root/+work" || state.Active != "@root/+work" {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": "ORIENTATION_STALE", "error": "root revision mismatch"})
				return
			}
			_, _ = fmt.Fprint(w, "kegv: \"2025-07\"\ntitle: Root notes\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer root.Close()
	foreign := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		require.Equal(t, "Bearer foreign-token", r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get(keg.OrientationHeaderName))
		foreignCalls++
		if r.URL.Path == "/api/v1/kegs" {
			_ = json.NewEncoder(w).Encode([]tapper.HubKeg{{Namespace: "foreign", Alias: "notes", Role: foreignRole, Visibility: "private"}})
			return
		}
		if r.Method != http.MethodGet {
			mutations++
		}
		if foreignDenied {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "FORBIDDEN", "error": "destination ACL denied"})
			return
		}
		_, _ = fmt.Fprint(w, "kegv: \"2025-07\"\ntitle: Foreign notes\n")
	}))
	defer foreign.Close()
	sb := newTestSandbox(t)
	rt := sb.Runtime()
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: rt})
	require.NoError(t, err)
	config := fmt.Sprintf("disableAtlasHub: true\nflight: '@root/+work'\nhub: root-alias\nfallbackNamespace: root\nnamespaces:\n  root: {hub: root-alias}\n  foreign: {hub: homelab}\nhubs:\n  root-alias: {url: '%s'}\n  homelab: {url: '%s'}\n", root.URL, foreign.URL)
	require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), []byte(config), 0644))
	tap.ConfigService.Reload()
	store, err := tapper.LoadAuthStore(ctx, rt, tap.PathService.AuthStorePath())
	require.NoError(t, err)
	store.Set(root.URL, tapper.AuthEntry{AccessToken: "root-token"})
	store.Set(foreign.URL, tapper.AuthEntry{AccessToken: "foreign-token"})
	require.NoError(t, store.Save(ctx, rt, tap.PathService.AuthStorePath()))
	tap.AuthValidateFn = func(_ context.Context, _ *toolkit.Runtime, hub, token string) (*tapper.WhoAmI, error) {
		ns := "root"
		if hub == foreign.URL {
			ns = "foreign"
		}
		return &tapper.WhoAmI{UserID: 1, Username: ns, DefaultNamespace: ns, Namespaces: []string{ns}}, nil
	}
	session := connectFlightSession(t, ctx, mcp.NewServer(tap, "test", mcp.KegDefaults{}), nil)
	call := func(tool string, args map[string]any) *sdkmcp.CallToolResult {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: tool, Arguments: args})
		require.NoError(t, err)
		return result
	}
	settings := func(ref string) *sdkmcp.CallToolResult { return call("keg_settings", map[string]any{"keg": ref}) }
	oriented := callOrient(t, ctx, session)
	require.NotContains(t, oriented, "@foreign/notes")
	result := settings("@root/notes")
	require.False(t, result.IsError, extractText(t, result))
	result = settings("@foreign/notes")
	require.True(t, result.IsError)
	// Editing both selection and the old alias cannot retarget this connection.
	changed := strings.ReplaceAll(config, "hub: root-alias", "hub: homelab")
	changed = strings.ReplaceAll(changed, root.URL, foreign.URL)
	require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), []byte(changed), 0644))
	result = settings("@root/notes")
	require.False(t, result.IsError, extractText(t, result))
	direct := settings(foreign.URL + "/api/v1/@root/kegs/notes")
	require.True(t, direct.IsError)
	mu.Lock()
	require.Zero(t, foreignCalls)
	mu.Unlock()
	require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), []byte(config), 0644))
	mu.Lock()
	old := rootProjection().Revision
	foreignRole = "viewer"
	mu.Unlock()
	result = settings("@root/notes")
	require.False(t, result.IsError, extractText(t, result))
	require.Contains(t, callOrient(t, ctx, session), "@root/notes", "foreign catalog must not change root scope")
	mu.Lock()
	foreignDenied = true
	mu.Unlock()
	result = settings("@foreign/notes")
	require.True(t, result.IsError)
	require.Contains(t, extractText(t, result), "orientation denied")
	require.NotContains(t, extractText(t, result), "partial write")
	// Still-sufficient current permissions remain usable without a revision handshake.
	mu.Lock()
	rootRole = "viewer"
	mu.Unlock()
	remote := keg.NewRemoteKeg(root.URL+"/api/v1/@root/kegs/notes", "root-token", nil)
	stale := keg.WithOrientationState(ctx, keg.OrientationState{Root: "@root/+work", Active: "@root/+work", Revision: old, RootHub: root.URL})
	_, err = remote.Settings(stale)
	require.NoError(t, err)
	// Cover revocation must affect the next call before even a foreign read or write is sent.
	mu.Lock()
	revoked = true
	before := foreignCalls
	mu.Unlock()
	result = settings("@foreign/notes")
	require.True(t, result.IsError)
	result = call("create", map[string]any{"keg": "@foreign/notes", "nodes": []map[string]any{{"key": "new", "content": "# Denied"}}})
	require.True(t, result.IsError)
	mu.Lock()
	require.Equal(t, before, foreignCalls)
	require.Zero(t, mutations)
	revoked = false
	rootDown = true
	mu.Unlock()
	result = settings("@foreign/notes")
	require.True(t, result.IsError)
	require.True(t, strings.Contains(extractText(t, result), "ORIENTATION_UNAVAILABLE"))
	mu.Lock()
	require.Equal(t, before, foreignCalls)
	mu.Unlock()
}
