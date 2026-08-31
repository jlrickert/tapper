package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// requireConnectionInstructions asserts what initialization is allowed to
// carry: the static KEG operating rules and the directive to orient. It must
// never carry session state — which flight is pinned, its cover, or the
// available KEGs all belong to orient, because only orient is re-callable
// after a context reset.
func requireConnectionInstructions(t *testing.T, instructions string) {
	t.Helper()
	require.Contains(t, instructions, "# KEG System")
	require.Contains(t, instructions, "Call `orient`")
	require.NotContains(t, instructions, "## Available KEGs")
	require.NotContains(t, instructions, "Active flight:")
}

func TestMCP_ConfigChangesDoNotReplaceConnectionPinnedRoot(t *testing.T) {
	ctx, srv, rt := newOrientationServer(t, "")
	var notifications atomic.Int64
	session := connectFlightSession(t, ctx, srv, &sdkmcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *sdkmcp.ToolListChangedRequest) {
			notifications.Add(1)
		},
	})

	requireConnectionInstructions(t, session.InitializeResult().Instructions)

	writeProjectFlight(t, rt, "beta")
	before := callCat(t, ctx, session)
	require.False(t, before.IsError, extractText(t, before), "config changes do not adopt authority before orient")

	oriented := callOrient(t, ctx, session)
	require.Contains(t, oriented, "+alpha")
	require.Contains(t, oriented, "Alpha instructions")
	require.NotContains(t, oriented, "Beta instructions")
	require.False(t, callCat(t, ctx, session).IsError)

	writeProjectFlight(t, rt, "alpha")
	oriented = callOrient(t, ctx, session)
	require.Contains(t, oriented, "+alpha")
	require.False(t, callCat(t, ctx, session).IsError)
	require.Never(t, func() bool { return notifications.Load() > 0 }, 100*time.Millisecond, 10*time.Millisecond,
		"read-only orientation must not publish session state or change the tool list")
}

func TestMCP_StaticFlightIgnoresConfiguredSelectionAndRefreshesManifest(t *testing.T) {
	ctx, srv, rt := newOrientationServer(t, "+alpha")
	session := connectFlightSession(t, ctx, srv, nil)

	writeProjectFlight(t, rt, "beta")
	require.Contains(t, callOrient(t, ctx, session), "Alpha instructions")

	writeFlightCover(t, rt, "alpha", "Alpha refreshed", "other")
	refreshed := callCat(t, ctx, session)
	require.True(t, refreshed.IsError)
	require.NotContains(t, extractText(t, refreshed), "ORIENTATION_STALE")
	oriented := callOrient(t, ctx, session)
	require.Contains(t, oriented, "Alpha refreshed")
	require.NotContains(t, oriented, "Beta instructions")
	require.True(t, callCat(t, ctx, session).IsError, "orientation publishes the refreshed cover")
}

func TestMCP_EnvironmentFlightOverridesProjectSelection(t *testing.T) {
	ctx, srv, rt := newOrientationServer(t, "")
	require.NoError(t, rt.Env().Set("TAP_FLIGHT", "+environment"))

	session := connectFlightSession(t, ctx, srv, nil)
	requireConnectionInstructions(t, session.InitializeResult().Instructions)
	require.Contains(t, callOrient(t, ctx, session), "+environment")
	require.Contains(t, callOrient(t, ctx, session), "Environment instructions")
}

func TestMCP_ParallelSessionsKeepTheirInitializedRoots(t *testing.T) {
	ctx, srv, rt := newOrientationServer(t, "")
	first := connectFlightSession(t, ctx, srv, nil)
	second := connectFlightSession(t, ctx, srv, nil)

	writeProjectFlight(t, rt, "beta")
	require.Contains(t, callOrient(t, ctx, first), "+alpha")
	require.False(t, callCat(t, ctx, first).IsError)
	require.False(t, callCat(t, ctx, second).IsError, "unoriented session retains alpha")

	require.Contains(t, callOrient(t, ctx, second), "+alpha")
	third := connectFlightSession(t, ctx, srv, nil)
	requireConnectionInstructions(t, third.InitializeResult().Instructions)
	require.Contains(t, callOrient(t, ctx, third), "+beta")
	require.True(t, callCat(t, ctx, third).IsError)
}

func TestMCP_SelectionChangesNeverMoveInitializedRoot(t *testing.T) {
	ctx, srv, rt := newOrientationServer(t, "")
	session := connectFlightSession(t, ctx, srv, nil)

	writeProjectFlight(t, rt, "missing")
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "orient", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Contains(t, extractText(t, res), "+alpha")
	require.False(t, callCat(t, ctx, session).IsError)

	writeProjectFlight(t, rt, "")
	require.Contains(t, callOrient(t, ctx, session), "+alpha",
		"clearing a selection does not replace an initialized root")
	require.False(t, callCat(t, ctx, session).IsError)

	writeUserFlight(t, rt, "")
	require.Contains(t, callOrient(t, ctx, session), "+alpha")
	require.False(t, callCat(t, ctx, session).IsError)
}

// Initialization now carries the static operating rules so a caller that
// already knows its flight can skip orienting. Orient must keep carrying them
// too: initialization instructions are captured once at connection and are
// discarded by a context reset, so orient is the only route back to them.
func TestMCP_OrientRepeatsTheRulesInitializationAlreadySent(t *testing.T) {
	ctx, srv, _ := newOrientationServer(t, "")
	session := connectFlightSession(t, ctx, srv, nil)

	requireConnectionInstructions(t, session.InitializeResult().Instructions)

	oriented := callOrient(t, ctx, session)
	require.Contains(t, oriented, "# KEG System")
	require.Contains(t, oriented, "never read or write node files directly")
	require.Contains(t, oriented, "## Available KEGs", "orient adds the session state on top")
}

func TestMCP_OrientRejectsKegInputAndInitializationOmitsSessionState(t *testing.T) {
	ctx, srv, _ := newOrientationServer(t, "")
	session := connectFlightSession(t, ctx, srv, nil)
	initial := session.InitializeResult().Instructions
	requireConnectionInstructions(t, initial)
	// Initialization shares the static rules with orient but stops there;
	// orient adds the flight, its cover, and the KEG listing.
	require.NotEqual(t, initial, callOrient(t, ctx, session))

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "orient",
		Arguments: map[string]any{"keg": "personal"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
}

func TestMCP_RemoteAliasCoverlessRootActivatesFullSurfaceAndCrossFlightKegList(t *testing.T) {
	ctx := context.Background()
	var catalogRequests atomic.Int64
	root := tapper.HubFlight{
		Namespace: "admin", Slug: "admin", Title: "Admin", Visibility: "private",
		Subflights: []string{"+test"},
	}
	child := tapper.HubFlight{
		Namespace: "admin", Slug: "test", Title: "Test", Visibility: "private",
		Cover: []tapper.HubFlightCover{{Namespace: "admin", Keg: "private", Role: "editor"}},
	}
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/flights":
			_ = json.NewEncoder(w).Encode([]tapper.HubFlight{root, child})
		case "/api/v1/@admin/+admin":
			_ = json.NewEncoder(w).Encode(root)
		case "/api/v1/@admin/+test":
			_ = json.NewEncoder(w).Encode(child)
		case "/api/v1/kegs":
			catalogRequests.Add(1)
			_ = json.NewEncoder(w).Encode([]tapper.HubKeg{
				{Namespace: "admin", Alias: "ecw", Title: "ECW", Summary: "Delivery system", Visibility: "private", Role: "admin"},
				{Namespace: "admin", Alias: "example", Title: "Example", Summary: "Reference material", Visibility: "private", Role: "viewer"},
				{Namespace: "admin", Alias: "private", Title: "Private", Summary: "Covered child keg", Visibility: "private", Role: "editor"},
			})
		case "/api/v1/@admin/kegs/private/settings":
			_ = json.NewEncoder(w).Encode(map[string]any{"kegv": "2025-07", "title": "Private", "summary": "Covered child keg"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer hub.Close()

	sb := newTestSandbox(t)
	require.NoError(t, sb.Setwd("/home/testuser/project"))
	rt := sb.Runtime()
	config := "flight: \"@admin/+admin\"\n" +
		"fallbackHub: tapper-2-jlrickert\n" +
		"fallbackNamespace: admin\n" +
		"disableAtlasHub: true\n" +
		"namespaces:\n  admin:\n    hub: tapper-2-jlrickert\n" +
		"hubs:\n  tapper-2-jlrickert:\n    kind: remote\n    url: " + hub.URL + "\n"
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: rt})
	require.NoError(t, err)
	require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), []byte(config), 0o644))
	tap.ConfigService.Reload()
	cfg, err := tap.ConfigService.Config()
	require.NoError(t, err)
	configuredHub, ok := cfg.Hub("tapper-2-jlrickert")
	require.True(t, ok, "configured hub missing after writing %s", tap.PathService.UserConfig())
	require.Equal(t, hub.URL, configuredHub.URL)
	store, err := tapper.LoadAuthStore(ctx, rt, tap.PathService.AuthStorePath())
	require.NoError(t, err)
	store.Set(tapper.CanonicalHubURL(hub.URL), tapper.AuthEntry{AccessToken: "test-token"})
	require.NoError(t, store.Save(ctx, rt, tap.PathService.AuthStorePath()))
	tap.AuthValidateFn = func(context.Context, *toolkit.Runtime, string, string) (*tapper.WhoAmI, error) {
		return &tapper.WhoAmI{UserID: 1, Username: "admin", DefaultNamespace: "admin", Namespaces: []string{"admin"}}, nil
	}

	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{KegTargetOptions: tapper.KegTargetOptions{Flight: "@admin/+admin"}})
	session := connectFlightSession(t, ctx, srv, nil)
	requireConnectionInstructions(t, session.InitializeResult().Instructions)
	initialOrientation := callOrient(t, ctx, session)
	require.Contains(t, initialOrientation, "Active flight: `@admin/+admin`")
	require.Contains(t, initialOrientation, "`@admin/+test`")
	require.Contains(t, initialOrientation, "`@admin/private`")
	require.NotContains(t, initialOrientation, "`@admin/ecw`")
	require.NotContains(t, initialOrientation, "`@admin/example`")

	names := listedToolNames(t, ctx, session)
	for _, want := range []string{"cat", "create", "edit", "keg_list", "keg_settings", "schema_list", "validate", "flight_create"} {
		require.Contains(t, names, want, "coverless active root must publish the complete registered inventory")
	}
	require.Greater(t, len(names), 40)

	rootList, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, rootList.IsError, extractText(t, rootList))
	require.Equal(t, "@admin/private\teditor\t@admin/+test", extractText(t, rootList))
	rootStructured, err := json.Marshal(rootList.StructuredContent)
	require.NoError(t, err)
	require.JSONEq(t, `{"kegs":[{"ref":"@admin/private","role":"editor","flights":["@admin/+test"]}]}`, string(rootStructured))

	explicitRoot, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{"flight": "@admin/+admin"}})
	require.NoError(t, err)
	require.False(t, explicitRoot.IsError, extractText(t, explicitRoot))
	require.Empty(t, extractText(t, explicitRoot))

	beforeSelected := catalogRequests.Load()
	selected, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{"flight": "+test"}})
	require.NoError(t, err)
	require.Equal(t, "@admin/private\teditor\t@admin/+test", extractText(t, selected))
	selectedStructured, err := json.Marshal(selected.StructuredContent)
	require.NoError(t, err)
	require.JSONEq(t, `{"kegs":[{"ref":"@admin/private","role":"editor","flights":["@admin/+test"]}]}`, string(selectedStructured))
	require.Equal(t, beforeSelected+1, catalogRequests.Load(), "selected projection discovers the Hub once")

	deniedOperation, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_settings", Arguments: map[string]any{"keg": "@admin/private"}})
	require.NoError(t, err)
	require.True(t, deniedOperation.IsError)
	allowedOperation, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_settings", Arguments: map[string]any{"flight": "+test", "keg": "@admin/private"}})
	require.NoError(t, err)
	require.False(t, allowedOperation.IsError, extractText(t, allowedOperation))
	require.Contains(t, extractText(t, allowedOperation), "title: Private")

	defaultOrient, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "orient", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.Contains(t, extractText(t, defaultOrient), "`@admin/private`")
	require.NotContains(t, extractText(t, defaultOrient), "`@admin/ecw`")

	explicitRootOrient, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "orient", Arguments: map[string]any{"flight": "@admin/+admin"}})
	require.NoError(t, err)
	require.NotContains(t, extractText(t, explicitRootOrient), "`@admin/private`")

	for _, query := range []string{"ecw", "EXAMPLE", "covered child"} {
		found, searchErr := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_search", Arguments: map[string]any{"query": query}})
		require.NoError(t, searchErr)
		require.False(t, found.IsError, extractText(t, found))
		require.NotEmpty(t, extractText(t, found))
	}

	beforeInvalid := catalogRequests.Load()
	invalid, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{"all": true}})
	require.NoError(t, err)
	require.True(t, invalid.IsError)
	require.Contains(t, extractText(t, invalid), "unexpected additional properties")
	require.Equal(t, beforeInvalid, catalogRequests.Load(), "removed all must fail schema validation before discovery")
}

func newOrientationServer(t *testing.T, static string) (context.Context, *sdkmcp.Server, *toolkit.Runtime) {
	t.Helper()
	ctx := context.Background()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Setwd("/home/testuser/project"))
	rt := sb.Runtime()
	installOrientationTestHub(t, rt)
	writeUserFlight(t, rt, "baseline")
	writeProjectFlight(t, rt, "alpha")
	writeFlight(t, rt, "baseline", "Baseline instructions")
	writeFlight(t, rt, "alpha", "Alpha instructions")
	writeFlight(t, rt, "beta", "Beta instructions")
	writeFlight(t, rt, "environment", "Environment instructions")

	tap := newMemoryTap(t, ctx, rt)
	_, err := tap.FlightService.GetFlightFresh(ctx, "+alpha")
	require.NoError(t, err)
	require.Equal(t, "+alpha", tap.ActiveFlightName(""))
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{
		KegTargetOptions: tapper.KegTargetOptions{Flight: static},
	})
	return ctx, srv, rt
}

func writeProjectFlight(t *testing.T, rt *toolkit.Runtime, flight string) {
	t.Helper()
	body := ""
	if flight != "" {
		body += "flight: +" + flight + "\n"
	}
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/project/.tapper/config.yaml", []byte(body), 0o644))
}

func writeUserFlight(t *testing.T, rt *toolkit.Runtime, flight string) {
	t.Helper()
	hub := orientationTestHubFor(t, rt)
	body := "defaultKeg: personal\nfallbackHub: home\nfallbackNamespace: local\ndisableAtlasHub: true\n" +
		"namespaces:\n  local:\n    hub: home\n" +
		"hubs:\n  home:\n    kind: remote\n    url: " + hub.server.URL + "\n    tokenEnv: TAPPER_TEST_HUB_TOKEN\n"
	if flight != "" {
		body += "flight: +" + flight + "\n"
	}
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(body), 0o644))
}

func writeFlight(t *testing.T, rt *toolkit.Runtime, slug, instructions string) {
	t.Helper()
	kegName := "personal"
	if slug == "beta" {
		kegName = "other"
	}
	writeFlightCover(t, rt, slug, instructions, kegName)
}

func writeFlightCover(t *testing.T, rt *toolkit.Runtime, slug, instructions, kegName string) {
	t.Helper()
	hub := orientationTestHubFor(t, rt)
	hub.putFlight(tapper.HubFlight{
		Namespace: "local", Slug: slug, Title: strings.ToUpper(slug[:1]) + slug[1:],
		Visibility: tapper.FlightVisibilityPrivate, Instructions: instructions,
		Cover: []tapper.HubFlightCover{{Namespace: "local", Keg: kegName, Role: "editor"}},
	})
}

type orientationTestHub struct {
	mu      sync.RWMutex
	flights map[string]tapper.HubFlight
	kegs    map[string]tapper.HubKeg
	server  *httptest.Server
}

var orientationTestHubs sync.Map

func installOrientationTestHub(t *testing.T, rt *toolkit.Runtime) *orientationTestHub {
	t.Helper()
	hub := &orientationTestHub{
		flights: map[string]tapper.HubFlight{},
		kegs: map[string]tapper.HubKeg{
			"personal": {Namespace: "local", Alias: "personal", Title: "Personal KEG", Summary: "Personal test knowledge", Visibility: "private", Role: "admin"},
			"other":    {Namespace: "local", Alias: "other", Title: "Other KEG", Summary: "Other test knowledge", Visibility: "private", Role: "admin"},
			"private":  {Namespace: "local", Alias: "private", Title: "Private KEG", Summary: "Private test knowledge", Visibility: "private", Role: "admin"},
		},
	}
	hub.server = httptest.NewServer(http.HandlerFunc(hub.serveHTTP))
	orientationTestHubs.Store(rt, hub)
	require.NoError(t, rt.Env().Set("TAPPER_TEST_HUB_TOKEN", "test-token"))
	t.Cleanup(func() {
		orientationTestHubs.Delete(rt)
		hub.server.Close()
	})
	return hub
}

func orientationTestHubFor(t *testing.T, rt *toolkit.Runtime) *orientationTestHub {
	t.Helper()
	value, ok := orientationTestHubs.Load(rt)
	require.True(t, ok, "orientation test hub is not installed")
	return value.(*orientationTestHub)
}

func (h *orientationTestHub) putFlight(flight tapper.HubFlight) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if flight.Namespace == "" {
		flight.Namespace = "local"
	}
	if flight.Visibility == "" {
		flight.Visibility = tapper.FlightVisibilityPrivate
	}
	flight.Hash = tapper.FlightManifestHash(tapper.FlightManifest{
		Title: flight.Title, Visibility: flight.Visibility, Capabilities: flight.Capabilities,
		Cover: hubFlightCover(flight.Cover), Subflights: flight.Subflights, Instructions: flight.Instructions,
	})
	h.flights[flight.Slug] = flight
}

func hubFlightCover(rows []tapper.HubFlightCover) []tapper.FlightCover {
	out := make([]tapper.FlightCover, 0, len(rows))
	for _, row := range rows {
		out = append(out, tapper.FlightCover{Namespace: row.Namespace, Keg: row.Keg, Role: tapper.FlightRole(row.Role)})
	}
	return out
}

func (h *orientationTestHub) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/flights":
		rows := make([]tapper.HubFlight, 0, len(h.flights))
		for _, flight := range h.flights {
			rows = append(rows, flight)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Slug < rows[j].Slug })
		_ = json.NewEncoder(w).Encode(rows)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/@local/+"):
		slug := strings.TrimPrefix(r.URL.Path, "/api/v1/@local/+")
		flight, ok := h.flights[slug]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(flight)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/@local/flights":
		var flight tapper.HubFlight
		if err := json.NewDecoder(r.Body).Decode(&flight); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		flight.Namespace = "local"
		if _, exists := h.flights[flight.Slug]; exists {
			http.Error(w, `{"error":"already exists"}`, http.StatusConflict)
			return
		}
		h.flights[flight.Slug] = flight
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(flight)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/kegs":
		rows := make([]tapper.HubKeg, 0, len(h.kegs))
		for _, kegRow := range h.kegs {
			rows = append(rows, kegRow)
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Alias < rows[j].Alias })
		_ = json.NewEncoder(w).Encode(rows)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/@local/kegs":
		var payload struct {
			Alias      string `json:"alias"`
			Title      string `json:"title"`
			Visibility string `json:"visibility"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		h.kegs[payload.Alias] = tapper.HubKeg{Namespace: "local", Alias: payload.Alias, Title: payload.Title, Visibility: payload.Visibility, Role: "admin"}
		w.WriteHeader(http.StatusCreated)
	default:
		http.NotFound(w, r)
	}
}

func connectFlightSession(t *testing.T, ctx context.Context, srv *sdkmcp.Server, opts *sdkmcp.ClientOptions) *sdkmcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := sdkmcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "flight-test", Version: "0.1"}, opts)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callOrient(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession) string {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "orient", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, result.IsError, extractText(t, result))
	return extractText(t, result)
}

func callCat(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession) *sdkmcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"keg":          "@local/personal",
			"node_ids":     []string{"0"},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	return result
}

func listedToolNames(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession) []string {
	t.Helper()
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	return names
}
