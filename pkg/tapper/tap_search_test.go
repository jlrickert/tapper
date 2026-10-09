package tapper_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapapi"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// fakeSearchHub records every ranked-search request body and answers with a
// fixed status and body. Like the Hub's search route, it marks its responses
// with tapapi.SearchRouteHeader unless unrouted is set, which mimics a Hub
// answering a route it does not have.
type fakeSearchHub struct {
	mu       sync.Mutex
	bodies   []string
	proofs   []string
	calls    atomic.Int32
	status   int
	reply    string
	unrouted bool
}

func newFakeSearchHub(t *testing.T, status int, reply string) (*fakeSearchHub, string) {
	t.Helper()
	return newFakeSearchHubRouted(t, status, reply, true)
}

func newFakeSearchHubRouted(t *testing.T, status int, reply string, routed bool) (*fakeSearchHub, string) {
	t.Helper()
	hub := &fakeSearchHub{status: status, reply: reply, unrouted: !routed}
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1"+tapapi.PathSearch || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		hub.calls.Add(1)
		raw, _ := io.ReadAll(r.Body)
		hub.mu.Lock()
		hub.bodies = append(hub.bodies, string(raw))
		hub.proofs = append(hub.proofs, r.Header.Get(keg.OrientationHeaderName))
		hub.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !hub.unrouted {
			w.Header().Set(tapapi.SearchRouteHeader, tapapi.SearchRouteHeaderValue)
		}
		w.WriteHeader(hub.status)
		_, _ = w.Write([]byte(hub.reply))
	}))
	t.Cleanup(srv.Close)
	return hub, srv.URL
}

func (h *fakeSearchHub) last(t *testing.T) tapapi.SearchRequest {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	require.NotEmpty(t, h.bodies, "the Hub received no search request")
	var got tapapi.SearchRequest
	require.NoError(t, json.Unmarshal([]byte(h.bodies[len(h.bodies)-1]), &got))
	return got
}

func (h *fakeSearchHub) lastProof(t *testing.T) string {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	require.NotEmpty(t, h.proofs, "the Hub received no search request")
	return h.proofs[len(h.proofs)-1]
}

func (h *fakeSearchHub) lastRaw(t *testing.T) map[string]any {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	require.NotEmpty(t, h.bodies)
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(h.bodies[len(h.bodies)-1]), &raw))
	return raw
}

// newSearchTap returns a Tap whose default Hub is hubURL.
func newSearchTap(t *testing.T, hubURL string) *tapper.Tap {
	t.Helper()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	config := fmt.Sprintf("hub: atlas\nhubs:\n  atlas:\n    url: %s\n    token: tok\n", hubURL)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(config), 0o644))
	tap.ConfigService.Reload()
	return tap
}

func TestSearch_WholeHubPassesRequestThroughAndDecodesResponse(t *testing.T) {
	t.Parallel()

	reply := `{"hits":[{"ref":"keg:@acme/notes/12","keg":"@acme/notes","node_id":"12","title":"Festival trip","score":4.25,` +
		`"fields":["title","meta"],"sources":["keyword","semantic"],"snippet":"festival in Los Angeles","highlights":[{"start":12,"end":23}]}],` +
		`"next_cursor":"c2","terms":[{"term":"angelos","lexemes":["angel"],"typo":true}],"mode":"hybrid","warnings":["partial"]}`
	hub, url := newFakeSearchHub(t, http.StatusOK, reply)
	tap := newSearchTap(t, url)

	cursor := " opaque/cursor+== "
	resp, err := tap.Search(t.Context(), tapper.SearchOptions{Query: "los angelos", Cursor: cursor, Limit: 5})
	require.NoError(t, err)

	require.Equal(t, tapapi.SearchRequest{Query: "los angelos", Cursor: cursor, Limit: 5}, hub.last(t),
		"a whole-Hub search names no KEG and passes the cursor through unchanged")
	require.Len(t, resp.Hits, 1)
	hit := resp.Hits[0]
	require.Equal(t, "keg:@acme/notes/12", hit.Ref)
	require.Equal(t, "@acme/notes", hit.Keg)
	require.Equal(t, "12", hit.NodeID)
	require.Equal(t, 4.25, hit.Score)
	require.Equal(t, []string{tapapi.SearchFieldTitle, tapapi.SearchFieldMeta}, hit.Fields)
	require.Equal(t, []string{tapapi.SearchSourceKeyword, tapapi.SearchSourceSemantic}, hit.Sources)
	require.Equal(t, []tapapi.Span{{Start: 12, End: 23}}, hit.Highlights)
	require.Equal(t, "c2", resp.NextCursor)
	require.Equal(t, tapapi.SearchModeHybrid, resp.Mode)
	require.Equal(t, []string{"partial"}, resp.Warnings)
	require.Equal(t, []tapapi.TermExpansion{{Term: "angelos", Lexemes: []string{"angel"}, Typo: true}}, resp.Terms)
}

func TestSearch_OmittedLimitLetsTheHubChoose(t *testing.T) {
	t.Parallel()

	hub, url := newFakeSearchHub(t, http.StatusOK, `{"mode":"keyword"}`)
	tap := newSearchTap(t, url)

	resp, err := tap.Search(t.Context(), tapper.SearchOptions{Query: "release"})
	require.NoError(t, err)
	raw := hub.lastRaw(t)
	for _, key := range []string{"limit", "cursor", "kegs"} {
		_, present := raw[key]
		require.False(t, present, "%s must be omitted so the Hub applies its own default", key)
	}
	require.NotNil(t, resp.Hits, "a response without hits decodes to an empty list")
	require.Empty(t, resp.Hits)
	require.Equal(t, tapapi.SearchModeKeyword, resp.Mode)
}

func TestSearch_NamedKegsNarrowTheSearch(t *testing.T) {
	t.Parallel()

	hub, url := newFakeSearchHub(t, http.StatusOK, `{"hits":[]}`)
	tap := newSearchTap(t, url)

	_, err := tap.Search(t.Context(), tapper.SearchOptions{Query: "release", Kegs: []string{"@acme/notes", " @acme/eng ", "@acme/notes"}})
	require.NoError(t, err)
	require.Equal(t, []string{"@acme/notes", "@acme/eng"}, hub.last(t).Kegs)
}

func TestSearch_RejectsInvalidRequestsBeforeTheHub(t *testing.T) {
	t.Parallel()

	hub, url := newFakeSearchHub(t, http.StatusOK, `{"hits":[]}`)
	tap := newSearchTap(t, url)

	cases := map[string]tapper.SearchOptions{
		"empty query":    {Query: "   "},
		"negative limit": {Query: "x", Limit: -1},
		"bare keg ref":   {Query: "x", Kegs: []string{"notes"}},
		"keg and kegs":   {Query: "x", KegTargetOptions: tapper.KegTargetOptions{Keg: "@acme/notes"}, Kegs: []string{"@acme/eng"}},
	}
	for name, opts := range cases {
		_, err := tap.Search(t.Context(), opts)
		require.ErrorIs(t, err, keg.ErrInvalid, name)
	}
	require.Zero(t, hub.calls.Load())
}

func TestSearch_FlightCoverScopesTheSearch(t *testing.T) {
	t.Parallel()

	hub, url := newFakeSearchHub(t, http.StatusOK, `{"hits":[]}`)
	tap := newSearchTap(t, url)
	flight := &tapper.Flight{Name: "@acme/+work", EffectiveCover: []tapper.HubEffectiveKeg{
		{Ref: "@acme/notes", Role: tapper.FlightRoleViewer},
		{Ref: "@acme/eng", Role: tapper.FlightRoleEditor},
	}}
	inFlight := tapper.KegTargetOptions{FlightContext: flight}

	_, err := tap.Search(t.Context(), tapper.SearchOptions{KegTargetOptions: inFlight, Query: "release"})
	require.NoError(t, err)
	require.Equal(t, []string{"@acme/notes", "@acme/eng"}, hub.last(t).Kegs)

	// A named KEG outside the cover is dropped with a warning.
	resp, err := tap.Search(t.Context(), tapper.SearchOptions{KegTargetOptions: inFlight, Kegs: []string{"@acme/eng", "@other/private"}, Query: "release"})
	require.NoError(t, err)
	require.Equal(t, []string{"@acme/eng"}, hub.last(t).Kegs)
	require.Len(t, resp.Warnings, 1)
	require.Contains(t, resp.Warnings[0], "@other/private")

	// The direct CLI bypasses the flight and searches what it names.
	_, err = tap.Search(t.Context(), tapper.SearchOptions{
		KegTargetOptions: tapper.KegTargetOptions{FlightContext: flight, BypassFlightRestrictions: true},
		Kegs:             []string{"@other/private"}, Query: "release",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"@other/private"}, hub.last(t).Kegs)
}

// wideCoverFlight returns a flight whose cover is wider than one search can
// name.
func wideCoverFlight(n int) (*tapper.Flight, []string) {
	flight := &tapper.Flight{Name: "@acme/+wide", EffectiveCover: []tapper.HubEffectiveKeg{}}
	refs := make([]string, 0, n)
	for i := range n {
		ref := fmt.Sprintf("@acme/k%02d", i)
		refs = append(refs, ref)
		flight.EffectiveCover = append(flight.EffectiveCover, tapper.HubEffectiveKeg{Ref: ref, Role: tapper.FlightRoleViewer})
	}
	return flight, refs
}

func TestSearch_GovernedUnscopedSearchLetsTheHubScopeToTheCover(t *testing.T) {
	t.Parallel()

	hub, url := newFakeSearchHub(t, http.StatusOK, `{"hits":[]}`)
	tap := newSearchTap(t, url)
	flight, refs := wideCoverFlight(tapapi.SearchMaxKegs + 10)
	ctx := keg.WithOrientationState(t.Context(), keg.OrientationState{RootHub: url, Root: "@acme/+root", Active: "@acme/+wide"})
	inFlight := tapper.KegTargetOptions{FlightContext: flight}

	_, err := tap.Search(ctx, tapper.SearchOptions{KegTargetOptions: inFlight, Query: "release"})
	require.NoError(t, err)
	_, named := hub.lastRaw(t)["kegs"]
	require.False(t, named, "with the orientation proof attached, the Hub scopes an unscoped search to the cover")
	require.NotEmpty(t, hub.lastProof(t), "the cover is only implied by the proof, so the proof must travel")

	// Explicit kegs still narrow, and still only within the cover.
	resp, err := tap.Search(ctx, tapper.SearchOptions{KegTargetOptions: inFlight, Kegs: []string{refs[3], "@other/private"}, Query: "release"})
	require.NoError(t, err)
	require.Equal(t, []string{refs[3]}, hub.last(t).Kegs)
	require.Contains(t, strings.Join(resp.Warnings, "\n"), "@other/private")

	// A governed flight that covers nothing still never reaches the Hub.
	before := hub.calls.Load()
	empty := tapper.KegTargetOptions{FlightContext: &tapper.Flight{Name: "@acme/+empty", EffectiveCover: []tapper.HubEffectiveKeg{}}}
	resp, err = tap.Search(ctx, tapper.SearchOptions{KegTargetOptions: empty, Query: "release"})
	require.NoError(t, err)
	require.Empty(t, resp.Hits)
	require.Equal(t, before, hub.calls.Load())
}

func TestSearch_TooManyKegsAreRefusedBeforeTheHub(t *testing.T) {
	t.Parallel()

	hub, url := newFakeSearchHub(t, http.StatusOK, `{"hits":[]}`)
	tap := newSearchTap(t, url)
	flight, refs := wideCoverFlight(tapapi.SearchMaxKegs + 1)

	// Without the orientation proof the client names the cover, so a cover
	// wider than the Hub's cap is refused clearly instead of with a Hub 400.
	_, err := tap.Search(t.Context(), tapper.SearchOptions{KegTargetOptions: tapper.KegTargetOptions{FlightContext: flight}, Query: "release"})
	require.ErrorIs(t, err, keg.ErrInvalid)
	require.ErrorContains(t, err, "flight @acme/+wide covers 51 KEGs")
	require.ErrorContains(t, err, "--kegs")

	// Naming more KEGs than one search can carry is refused on every path.
	governed := keg.WithOrientationState(t.Context(), keg.OrientationState{RootHub: url, Root: "@acme/+root", Active: "@acme/+wide"})
	_, err = tap.Search(governed, tapper.SearchOptions{KegTargetOptions: tapper.KegTargetOptions{FlightContext: flight}, Kegs: refs, Query: "release"})
	require.ErrorIs(t, err, keg.ErrInvalid)
	require.ErrorContains(t, err, "at most 50")
	require.Zero(t, hub.calls.Load())

	// Narrowing a wide cover with explicit kegs works.
	_, err = tap.Search(t.Context(), tapper.SearchOptions{KegTargetOptions: tapper.KegTargetOptions{FlightContext: flight}, Kegs: refs[:2], Query: "release"})
	require.NoError(t, err)
	require.Equal(t, refs[:2], hub.last(t).Kegs)
	require.Empty(t, hub.lastProof(t))
}

func TestSearch_ExplicitKegsNeedANamespacedCoverEntry(t *testing.T) {
	t.Parallel()

	hub, url := newFakeSearchHub(t, http.StatusOK, `{"hits":[]}`)
	tap := newSearchTap(t, url)
	flight := &tapper.Flight{Name: "@acme/+work", FlightManifest: tapper.FlightManifest{Cover: []tapper.FlightCover{
		{Keg: "notes", Role: tapper.FlightRoleViewer},
		{Namespace: "acme", Keg: "eng", Role: tapper.FlightRoleViewer},
	}}}
	inFlight := tapper.KegTargetOptions{FlightContext: flight}

	// A namespace-less cover entry matches no namespace, explicit or implicit.
	resp, err := tap.Search(t.Context(), tapper.SearchOptions{KegTargetOptions: inFlight, Kegs: []string{"@other/notes", "@acme/eng"}, Query: "release"})
	require.NoError(t, err)
	require.Equal(t, []string{"@acme/eng"}, hub.last(t).Kegs)
	require.Contains(t, strings.Join(resp.Warnings, "\n"), "@other/notes is not in flight")

	resp, err = tap.Search(t.Context(), tapper.SearchOptions{KegTargetOptions: inFlight, Query: "release"})
	require.NoError(t, err)
	require.Equal(t, []string{"@acme/eng"}, hub.last(t).Kegs)
	require.Contains(t, strings.Join(resp.Warnings, "\n"), `cover entry "notes" names no namespace`)
}

func TestSearch_LegacyAllowedKegsScopeBothPaths(t *testing.T) {
	t.Parallel()

	hub, url := newFakeSearchHub(t, http.StatusOK, `{"hits":[]}`)
	tap := newSearchTap(t, url)
	flight := &tapper.Flight{Name: "@acme/+legacy", FlightManifest: tapper.FlightManifest{
		AllowedKegs: []string{"@acme/notes", "@acme/eng=viewer", "bare"},
	}}
	inFlight := tapper.KegTargetOptions{FlightContext: flight}

	_, err := tap.Search(t.Context(), tapper.SearchOptions{KegTargetOptions: inFlight, Query: "release"})
	require.NoError(t, err)
	require.Equal(t, []string{"@acme/notes", "@acme/eng"}, hub.last(t).Kegs, "an unscoped search covers the legacy allowlist")

	_, err = tap.Search(t.Context(), tapper.SearchOptions{KegTargetOptions: inFlight, Kegs: []string{"@acme/eng", "@other/bare"}, Query: "release"})
	require.NoError(t, err)
	require.Equal(t, []string{"@acme/eng"}, hub.last(t).Kegs, "an explicit search honors the same allowlist")
}

func TestSearch_EmptyRestrictedScopeNeverReachesTheHub(t *testing.T) {
	t.Parallel()

	hub, url := newFakeSearchHub(t, http.StatusOK, `{"hits":[]}`)
	tap := newSearchTap(t, url)

	empty := tapper.KegTargetOptions{FlightContext: &tapper.Flight{Name: "@acme/+empty", EffectiveCover: []tapper.HubEffectiveKeg{}}}
	resp, err := tap.Search(t.Context(), tapper.SearchOptions{KegTargetOptions: empty, Query: "release"})
	require.NoError(t, err)
	require.NotNil(t, resp.Hits)
	require.Empty(t, resp.Hits)

	narrow := tapper.KegTargetOptions{FlightContext: &tapper.Flight{Name: "@acme/+work", EffectiveCover: []tapper.HubEffectiveKeg{{Ref: "@acme/notes", Role: tapper.FlightRoleViewer}}}}
	resp, err = tap.Search(t.Context(), tapper.SearchOptions{KegTargetOptions: narrow, Kegs: []string{"@other/private"}, Query: "release"})
	require.NoError(t, err)
	require.Empty(t, resp.Hits)
	require.NotEmpty(t, resp.Warnings)

	require.Zero(t, hub.calls.Load(), "an empty KEG list means everything to the Hub, so a scope narrowed to nothing must not be sent")
}

func TestSearch_OneKegNamesOnlyThatKeg(t *testing.T) {
	t.Parallel()

	hub, url := newFakeSearchHub(t, http.StatusOK, `{"hits":[]}`)
	tap := newSearchTap(t, url)

	_, err := tap.Search(t.Context(), tapper.SearchOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: "@acme/notes"},
		Query:            "release", Cursor: "c1", Limit: 3,
	})
	require.NoError(t, err)
	require.Equal(t, tapapi.SearchRequest{Query: "release", Kegs: []string{"@acme/notes"}, Cursor: "c1", Limit: 3}, hub.last(t))
}

func TestSearch_OneKegOutsideTheFlightIsRefused(t *testing.T) {
	t.Parallel()

	hub, url := newFakeSearchHub(t, http.StatusOK, `{"hits":[]}`)
	tap := newSearchTap(t, url)

	flight := &tapper.Flight{Name: "@acme/+work", EffectiveCover: []tapper.HubEffectiveKeg{{Ref: "@acme/notes", Role: tapper.FlightRoleViewer}}}
	_, err := tap.Search(t.Context(), tapper.SearchOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: "@acme/private", FlightContext: flight},
		Query:            "release",
	})
	var restriction *tapper.FlightRestrictionError
	require.ErrorAs(t, err, &restriction)
	require.Zero(t, hub.calls.Load(), "a KEG outside the cover must not reach the Hub")

	_, err = tap.Search(t.Context(), tapper.SearchOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: "@acme/notes", FlightContext: flight},
		Query:            "release",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"@acme/notes"}, hub.last(t).Kegs)
}

func TestSearch_EndpointKegTargets(t *testing.T) {
	t.Parallel()

	hub, url := newFakeSearchHub(t, http.StatusOK, `{"hits":[]}`)
	tap := newSearchTap(t, url)

	// A KEG endpoint on the selected Hub is searched by its canonical ref.
	_, err := tap.Search(t.Context(), tapper.SearchOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: url + "/api/v1/@acme/kegs/notes"},
		Query:            "release",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"@acme/notes"}, hub.last(t).Kegs)

	// An endpoint that names no namespaced KEG cannot be ranked.
	before := hub.calls.Load()
	_, err = tap.Search(t.Context(), tapper.SearchOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: url + "/api/v1/notes"},
		Query:            "release",
	})
	require.ErrorIs(t, err, tapper.ErrRankedSearchUnavailable)
	require.ErrorContains(t, err, "tap node grep")
	require.Equal(t, before, hub.calls.Load())
}

func TestSearch_GovernedCallRefusesAnotherHub(t *testing.T) {
	t.Parallel()

	hub, url := newFakeSearchHub(t, http.StatusOK, `{"hits":[]}`)
	tap := newSearchTap(t, url)

	ctx := keg.WithOrientationState(t.Context(), keg.OrientationState{
		RootHub: "https://pinned.example.invalid", Root: "@acme/+root", Active: "@acme/+root",
	})
	_, err := tap.Search(ctx, tapper.SearchOptions{Query: "release"})
	require.ErrorIs(t, err, keg.ErrOrientationDenied)
	require.Zero(t, hub.calls.Load())
}

func TestSearch_HubErrorsMapToSentinels(t *testing.T) {
	t.Parallel()

	envelope := func(code string) string {
		raw, _ := json.Marshal(map[string]string{"error": "refused", "code": code})
		return string(raw)
	}
	cases := []struct {
		name     string
		status   int
		body     string
		wholeHub bool
		routed   bool
		want     error
	}{
		{"405 without the route header is a missing route", http.StatusMethodNotAllowed, "", false, false, tapper.ErrRankedSearchUnavailable},
		{"bare 404 without the route header is a missing route", http.StatusNotFound, "404 page not found", false, false, tapper.ErrRankedSearchUnavailable},
		// Hubs answer unknown /api/v1 routes with an enveloped NOT_FOUND, the
		// same body the route uses for an unreadable KEG.
		{"enveloped scoped 404 without the route header is a missing route", http.StatusNotFound, envelope(keg.RemoteCodeNotFound), false, false, tapper.ErrRankedSearchUnavailable},
		{"enveloped whole-Hub 404 without the route header is a missing route", http.StatusNotFound, envelope(keg.RemoteCodeNotFound), true, false, tapper.ErrRankedSearchUnavailable},
		{"routed 404 for a named KEG", http.StatusNotFound, envelope(keg.RemoteCodeNotFound), false, true, keg.ErrNotExist},
		{"routed whole-Hub 404", http.StatusNotFound, envelope(keg.RemoteCodeNotFound), true, true, keg.ErrNotExist},
		{"routed 405", http.StatusMethodNotAllowed, envelope(keg.RemoteCodeBadRequest), false, true, nil},
		{"401", http.StatusUnauthorized, envelope("UNAUTHORIZED"), false, true, tapper.ErrTokenRejected},
		{"403", http.StatusForbidden, envelope("FORBIDDEN"), false, true, keg.ErrForbidden},
		{"400", http.StatusBadRequest, envelope(keg.RemoteCodeInvalid), false, true, keg.ErrInvalid},
		{"409 version", http.StatusConflict, envelope(keg.RemoteCodeKegVersionUnsupported), false, true, keg.ErrKegVersionUnsupported},
		{"429", http.StatusTooManyRequests, envelope("RATE_LIMITED"), false, true, nil},
		{"500", http.StatusInternalServerError, "", false, true, nil},
		{"unrouted 500 is not a missing route", http.StatusInternalServerError, "", false, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, url := newFakeSearchHubRouted(t, tc.status, tc.body, tc.routed)
			tap := newSearchTap(t, url)
			opts := tapper.SearchOptions{Query: "release"}
			if !tc.wholeHub {
				opts.Kegs = []string{"@acme/notes"}
			}
			_, err := tap.Search(t.Context(), opts)
			require.Error(t, err)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
			}
			if tc.want != tapper.ErrRankedSearchUnavailable {
				require.NotErrorIs(t, err, tapper.ErrRankedSearchUnavailable)
			}
		})
	}
}

func TestSearch_UnavailableIsNotSupported(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, tapper.ErrRankedSearchUnavailable, keg.ErrNotSupported)
	_, url := newFakeSearchHubRouted(t, http.StatusNotFound, "404 page not found", false)
	_, err := newSearchTap(t, url).Search(t.Context(), tapper.SearchOptions{Query: "release"})
	require.ErrorIs(t, err, tapper.ErrRankedSearchUnavailable)
	code, _ := keg.RemoteErrorCode(err)
	require.Equal(t, keg.RemoteCodeNotSupported, code, "a Hub without ranked search is an unsupported operation, not an unknown failure")
	require.ErrorContains(t, err, "node_grep")
}

func TestSearch_MissingHubOrLoginMapToCodes(t *testing.T) {
	t.Parallel()

	hub, url := newFakeSearchHub(t, http.StatusOK, `{"hits":[]}`)
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	config := fmt.Sprintf("hub: atlas\nhubs:\n  atlas:\n    url: %s\n", url)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(config), 0o644))
	tap.ConfigService.Reload()

	for name, opts := range map[string]tapper.SearchOptions{
		"whole hub": {Query: "release"},
		"one keg":   {Query: "release", KegTargetOptions: tapper.KegTargetOptions{Keg: "@acme/notes"}},
	} {
		_, err := tap.Search(t.Context(), opts)
		require.ErrorIs(t, err, keg.ErrUnauthorized, name)
		require.ErrorContains(t, err, "tap auth login", name)
	}

	_, err = tap.Search(t.Context(), tapper.SearchOptions{Query: "release", KegTargetOptions: tapper.KegTargetOptions{Hub: "nowhere"}})
	require.ErrorIs(t, err, keg.ErrInvalid)
	require.ErrorContains(t, err, `hub "nowhere" is not configured`)
	require.Zero(t, hub.calls.Load())
}

func TestHighlightSnippet(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		snippet string
		spans   []tapapi.Span
		want    string
	}{
		{"one span", "a bc d", []tapapi.Span{{Start: 2, End: 4}}, "a [bc] d"},
		{"no spans", "plain", nil, "plain"},
		{"rune offsets", "é ñ", []tapapi.Span{{Start: 2, End: 3}}, "é [ñ]"},
		{"overlap skipped", "abc", []tapapi.Span{{Start: 0, End: 2}, {Start: 1, End: 2}}, "[ab]c"},
		{"overlap clamped", "abcd", []tapapi.Span{{Start: 0, End: 2}, {Start: 1, End: 3}}, "[ab][c]d"},
		{"end clamped", "abc", []tapapi.Span{{Start: 1, End: 9}}, "a[bc]"},
		{"out of range skipped", "abc", []tapapi.Span{{Start: 5, End: 9}}, "abc"},
		{"negative start clamped", "abc", []tapapi.Span{{Start: -3, End: 1}}, "[a]bc"},
		{"inverted skipped", "abc", []tapapi.Span{{Start: 2, End: 1}}, "abc"},
		{"empty skipped", "abc", []tapapi.Span{{Start: 1, End: 1}}, "abc"},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, tapper.HighlightSnippet(tc.snippet, tc.spans, "[", "]"), tc.name)
	}
}

func TestSearchHitLines(t *testing.T) {
	t.Parallel()

	lines := tapper.SearchHitLines([]tapapi.SearchHit{
		{Ref: "keg:@acme/notes/12", Title: "Trip\tplan", Score: 4.25, Snippet: "festival in\nLos Angeles", Highlights: []tapapi.Span{{Start: 0, End: 8}}},
		{Ref: "keg:@acme/eng/3", Title: "Release", Score: 1},
	}, "**", "**")
	require.Equal(t, []string{
		"keg:@acme/notes/12\t4.250\tTrip plan\t**festival** in Los Angeles",
		"keg:@acme/eng/3\t1.000\tRelease\t",
	}, lines)
}
