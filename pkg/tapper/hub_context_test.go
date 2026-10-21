package tapper_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// contextHub is a mock Hub that records the paths it serves. With
// serveContext false it answers /api/v1/info with 404, as a Hub that
// predates the endpoint does.
type contextHub struct {
	mu    sync.Mutex
	paths []string
}

func (h *contextHub) record(path string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paths = append(h.paths, path)
}

func (h *contextHub) seen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := append([]string(nil), h.paths...)
	sort.Strings(out)
	return out
}

// workEffectiveCover is the composed cover testapi.HubContextWork reports, as
// an older Hub's flight read carries it.
var workEffectiveCover = []tapper.HubEffectiveKeg{
	{Ref: "@foldwise/dev", Role: tapper.FlightRoleEditor, Source: "@foldwise/dev", Distance: 0, Path: []string{"@foldwise/dev"}},
	{Ref: "@foldwise/linked", Role: tapper.FlightRoleViewer, Source: "@foldwise/dev", Distance: 1, Path: []string{"@foldwise/dev", "@foldwise/linked"}},
}

// workFlight is the one flight named work the default mock Hub serves.
var workFlight = tapper.HubFlight{
	Namespace: "foldwise", Slug: "work", Title: "Work",
	Cover: []tapper.HubFlightCover{{Namespace: "foldwise", Keg: "dev", Role: "editor"}},
}

type contextHubOptions struct {
	serveContext   bool
	flightNotFound bool
	// flights is what the user can read, as GET /api/v1/flights lists it.
	// Nil means just workFlight.
	flights []tapper.HubFlight
}

func newContextHub(t *testing.T, serveContext, flightNotFound bool) (*contextHub, string) {
	t.Helper()
	return newContextHubWith(t, contextHubOptions{serveContext: serveContext, flightNotFound: flightNotFound})
}

// newContextHubWith serves a mock Hub. Its /api/v1/info resolves a bare
// +slug the way the real Hub and orient do: across the flights the user can
// read, so +work resolves to @foldwise/+work only because that is the user's
// one readable flight named work, even though jared is the personal
// namespace.
func newContextHubWith(t *testing.T, opts contextHubOptions) (*contextHub, string) {
	t.Helper()
	flights := opts.flights
	if flights == nil {
		flights = []tapper.HubFlight{workFlight}
	}
	serveContext, flightNotFound := opts.serveContext, opts.flightNotFound
	rec := &contextHub{}
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/info":
			if !serveContext {
				http.NotFound(w, r)
				return
			}
			flight := r.URL.Query().Get("flight")
			if r.Header.Get("Authorization") != "Bearer tok" {
				http.Error(w, "unexpected context request", http.StatusTeapot)
				return
			}
			if flightNotFound {
				_, _ = w.Write([]byte(testapi.HubContextFlightNotFound))
				return
			}
			resolved := flight
			if slug, bare := strings.CutPrefix(flight, "+"); bare {
				var matches []string
				for _, f := range flights {
					if f.Slug == slug {
						matches = append(matches, "@"+f.Namespace+"/+"+f.Slug)
					}
				}
				switch len(matches) {
				case 0:
					_, _ = w.Write([]byte(testapi.HubContextFlightNotFound))
					return
				case 1:
					resolved = matches[0]
				default:
					_, _ = w.Write([]byte(testapi.HubContextFlightAmbiguous))
					return
				}
			}
			if resolved != "@foldwise/+work" {
				http.Error(w, "unexpected context request", http.StatusTeapot)
				return
			}
			_, _ = w.Write([]byte(testapi.HubContextWork))
		case "GET /api/v1/whoami":
			_ = json.NewEncoder(w).Encode(tapper.WhoAmI{UserID: 1, Username: "jared", DefaultNamespace: "foldwise"})
		case "GET /api/v1/@foldwise/+work":
			_ = json.NewEncoder(w).Encode(tapper.HubFlight{
				Namespace: "foldwise", Slug: "work", Title: "Work",
				Cover:          []tapper.HubFlightCover{{Namespace: "foldwise", Keg: "dev", Role: "editor"}},
				EffectiveCover: workEffectiveCover,
			})
		case "GET /api/v1/flights":
			_ = json.NewEncoder(w).Encode(flights)
		case "GET /api/v1/kegs":
			_ = json.NewEncoder(w).Encode([]tapper.HubKeg{
				{Namespace: "foldwise", Alias: "dev", Title: "Development", Role: "admin"},
				{Namespace: "foldwise", Alias: "linked", Title: "Linked", Role: "admin"},
				{Namespace: "foldwise", Alias: "private", Title: "Outside the flight", Role: "admin"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return rec, srv.URL
}

func writeContextConfig(t *testing.T, sb *sandbox.Sandbox, hubURL string) {
	t.Helper()
	cfg := fmt.Sprintf("hub: cloud\nkeg: \"@foldwise/dev\"\nflight: \"@foldwise/+work\"\nhubs:\n  cloud: {url: %s, token: tok}\n", hubURL)
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(cfg), 0o600))
}

func wantWorkInfo(t *testing.T, info tapper.ContextInfoResult) {
	t.Helper()
	require.Equal(t, "jared", info.Hub.User)
	require.Equal(t, "@foldwise/+work", info.Flight.Ref)
	require.Equal(t, "Work", info.Flight.Title)
	require.Equal(t, []tapper.ContextInfoFlightKeg{
		{Ref: "@foldwise/dev", Role: "editor", Title: "Development", Source: "@foldwise/dev"},
		{Ref: "@foldwise/linked", Role: "viewer", Title: "Linked", Source: "@foldwise/dev", Distance: 1, Path: []string{"@foldwise/dev", "@foldwise/linked"}},
	}, info.Kegs, "the composed cover, at the role the flight caps each KEG to, with the path that decided it")
	require.Empty(t, info.Warnings)
}

func TestContextInfo_UsesOneContextRequest(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	rec, url := newContextHub(t, true, false)
	writeContextConfig(t, sb, url)
	tap := newTestTap(t, sb)

	info, err := tap.ContextInfo(sb.Context(), tapper.KegTargetOptions{})
	require.NoError(t, err)
	wantWorkInfo(t, info)
	require.Equal(t, []string{"/api/v1/info"}, rec.seen(), "the context endpoint replaces whoami, flight, and kegs")
}

func TestContextInfo_SendsBareSlugToContext(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	rec, url := newContextHub(t, true, false)
	cfg := fmt.Sprintf("hub: cloud\nkeg: \"@foldwise/dev\"\nflight: \"+work\"\nhubs:\n  cloud: {url: %s, token: tok}\n", url)
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(cfg), 0o600))
	tap := newTestTap(t, sb)

	info, err := tap.ContextInfo(sb.Context(), tapper.KegTargetOptions{})
	require.NoError(t, err)
	wantWorkInfo(t, info)
	require.Equal(t, []string{"/api/v1/info"}, rec.seen(), "the Hub resolves a bare slug itself")
}

func TestContextInfo_FallsBackOnOlderHubAndRemembersIt(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	rec, url := newContextHub(t, false, false)
	writeContextConfig(t, sb, url)
	tap := newTestTap(t, sb)

	info, err := tap.ContextInfo(sb.Context(), tapper.KegTargetOptions{})
	require.NoError(t, err)
	wantWorkInfo(t, info)
	require.Equal(t, []string{"/api/v1/@foldwise/+work", "/api/v1/info", "/api/v1/kegs", "/api/v1/whoami"}, rec.seen())

	info, err = tap.ContextInfo(sb.Context(), tapper.KegTargetOptions{})
	require.NoError(t, err)
	wantWorkInfo(t, info)
	seen := rec.seen()
	contextCalls := 0
	for _, p := range seen {
		if p == "/api/v1/info" {
			contextCalls++
		}
	}
	require.Equal(t, 1, contextCalls, "a 404 is remembered for the life of the Tap")
}

func TestContextInfo_FlightErrorIsAWarning(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	_, url := newContextHub(t, true, true)
	writeContextConfig(t, sb, url)
	tap := newTestTap(t, sb)

	info, err := tap.ContextInfo(sb.Context(), tapper.KegTargetOptions{})
	require.NoError(t, err)
	require.Equal(t, "jared", info.Hub.User)
	require.Equal(t, "@foldwise/+work", info.Flight.Ref)
	require.Empty(t, info.Kegs)
	require.Equal(t, []string{`Flight "@foldwise/+work" is unavailable: flight not found`}, info.Warnings)
}

func TestContextInfo_UnreachableHubWarnsForEachPart(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	writeContextConfig(t, sb, srv.URL)
	tap := newTestTap(t, sb)

	info, err := tap.ContextInfo(sb.Context(), tapper.KegTargetOptions{})
	require.NoError(t, err)
	require.Len(t, info.Warnings, 2)
	require.Contains(t, info.Warnings[0], "hub identity: ")
	require.Contains(t, info.Warnings[1], `Flight "@foldwise/+work" is unavailable: `)
}

func TestOrient_NoFlightListsKegsOnce(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	rec, url := newContextHub(t, true, false)
	cfg := fmt.Sprintf("hub: cloud\nhubs:\n  cloud: {url: %s, token: tok}\n", url)
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(cfg), 0o600))
	tap := newTestTap(t, sb)

	out, err := tap.Orient(sb.Context(), tapper.OrientOptions{})
	require.NoError(t, err)
	require.Contains(t, out, "No flight is active.")
	require.Equal(t, []string{"/api/v1/kegs"}, rec.seen(), "the identity catalog is fetched once")
}

func TestOrient_FlightLoadsGraphAndCatalog(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	rec, url := newContextHub(t, true, false)
	writeContextConfig(t, sb, url)
	tap := newTestTap(t, sb)

	out, err := tap.Orient(sb.Context(), tapper.OrientOptions{})
	require.NoError(t, err)
	require.Contains(t, out, "Selected flight: `@foldwise/+work`")
	require.Contains(t, out, "`@foldwise/dev`")
	require.NotContains(t, out, "`@foldwise/private`")
	require.Equal(t, []string{"/api/v1/@foldwise/+work", "/api/v1/flights", "/api/v1/kegs"}, rec.seen())
}

func writeBareSlugConfig(t *testing.T, sb *sandbox.Sandbox, hubURL string) {
	t.Helper()
	cfg := fmt.Sprintf("hub: cloud\nkeg: \"@foldwise/dev\"\nflight: \"+work\"\nhubs:\n  cloud: {url: %s, token: tok}\n", hubURL)
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(cfg), 0o600))
}

// A bare +slug means the same flight whether the Hub resolves it through
// /api/v1/info or Tapper resolves it itself against an older Hub.
func TestContextInfo_BareSlugAgreesWithFallback(t *testing.T) {
	t.Parallel()
	var infos []tapper.ContextInfoResult
	for _, serveContext := range []bool{true, false} {
		sb := NewSandbox(t)
		_, url := newContextHub(t, serveContext, false)
		writeBareSlugConfig(t, sb, url)
		tap := newTestTap(t, sb)
		info, err := tap.ContextInfo(sb.Context(), tapper.KegTargetOptions{})
		require.NoError(t, err)
		require.Empty(t, info.Warnings, "serveContext=%v", serveContext)
		infos = append(infos, info)
	}
	require.Equal(t, infos[0].Flight, infos[1].Flight)
	require.Equal(t, "@foldwise/+work", infos[0].Flight.Ref)
	require.Equal(t, "Work", infos[0].Flight.Title)
}

// When the user can read two flights named work, a bare +work is ambiguous
// on both paths, and both report it in the same words.
func TestContextInfo_AmbiguousBareSlugAgreesWithFallback(t *testing.T) {
	t.Parallel()
	personal := workFlight
	personal.Namespace = "jared"
	var warnings [][]string
	for _, serveContext := range []bool{true, false} {
		sb := NewSandbox(t)
		_, url := newContextHubWith(t, contextHubOptions{serveContext: serveContext, flights: []tapper.HubFlight{workFlight, personal}})
		writeBareSlugConfig(t, sb, url)
		tap := newTestTap(t, sb)
		info, err := tap.ContextInfo(sb.Context(), tapper.KegTargetOptions{})
		require.NoError(t, err)
		require.Empty(t, info.Kegs)
		warnings = append(warnings, info.Warnings)
	}
	want := []string{`Flight "+work" is unavailable: flight "+work" is ambiguous; use @namespace/+work`}
	require.Equal(t, want, warnings[0], "through /api/v1/info")
	require.Equal(t, want, warnings[1], "through the fallback")
}
