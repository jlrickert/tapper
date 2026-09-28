package tapper_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapapi"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// hubRecorder is a Hub stand-in that records every request path, so a test
// can assert what an operation costs in round trips.
type hubRecorder struct {
	mu     sync.Mutex
	paths  []string
	bodies map[string]map[string]any
}

func (h *hubRecorder) record(r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paths = append(h.paths, r.URL.Path)
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	if h.bodies == nil {
		h.bodies = map[string]map[string]any{}
	}
	h.bodies[r.URL.Path] = body
}

func (h *hubRecorder) recorded() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.paths...)
}

func remoteTap(t *testing.T, handler http.HandlerFunc) (*tapper.Tap, *hubRecorder) {
	t.Helper()
	rec := &hubRecorder{}
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	sb := NewSandbox(t)
	tap := newTestTap(t, sb)
	k := keg.NewRemoteKeg(srv.URL, "token", sb.Runtime())
	tap.KegResolver = func(context.Context, tapper.KegTargetOptions, tapper.FlightRole) (keg.Keg, error) { return k, nil }
	return tap, rec
}

// A formatted grep over a Hub keg used to read metadata once per matching
// node, so one agent grep made hundreds of sequential requests (#123). The
// Hub pages the matches and resolves the format's fields itself.
func TestFormattedGrepIsOneRequest(t *testing.T) {
	t.Parallel()
	const matches = 200
	tap, rec := remoteTap(t, func(w http.ResponseWriter, r *http.Request) {
		out := make([]keg.GrepMatch, matches)
		for i := range out {
			out[i] = keg.GrepMatch{
				Entry:  keg.NodeIndexEntry{ID: strconv.Itoa(i), Title: "Node"},
				Lines:  []string{"1:hit"},
				Fields: map[string]string{"type": "note"},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"matches": out})
	})

	lines, err := tap.Grep(t.Context(), tapper.GrepOptions{Query: "hit", Format: "%i %{type}", Limit: 500, Offset: 3})
	require.NoError(t, err)
	require.Len(t, lines, matches)
	require.Equal(t, "0 note", lines[0])

	require.Equal(t, []string{"/grep"}, rec.recorded())
	body := rec.bodies["/grep"]
	require.Equal(t, []any{"type"}, body["fields"])
	require.EqualValues(t, 500, body["limit"])
	require.EqualValues(t, 3, body["offset"])
}

// Links rendered with a metadata column resolve every row's fields in one
// listing call after the relationship lookup, never per node.
func TestFormattedLinksAreTwoRequests(t *testing.T) {
	t.Parallel()
	const related = 50
	tap, rec := remoteTap(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/related":
			out := keg.RelatedNodesResult{}
			for i := 1; i <= related; i++ {
				id := strconv.Itoa(i)
				out.Entries = append(out.Entries, keg.NodeIndexEntry{ID: id, Title: "Node"})
				out.Pairs = append(out.Pairs, keg.RelatedPair{From: "0", To: id})
			}
			_ = json.NewEncoder(w).Encode(out)
		case "/list/view":
			out := keg.ListViewResult{}
			for i := 1; i <= related; i++ {
				out.Rows = append(out.Rows, keg.ListViewRow{
					Entry:  keg.NodeIndexEntry{ID: strconv.Itoa(i)},
					Fields: map[string]string{"type": "idea"},
				})
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	lines, err := tap.Links(t.Context(), tapper.LinksOptions{NodeIDs: []string{"0"}, Format: "%i %{type}"})
	require.NoError(t, err)
	require.Len(t, lines, related)
	require.Equal(t, "1 idea", lines[0])
	require.Equal(t, []string{"/related", "/list/view"}, rec.recorded())
	require.Len(t, rec.bodies["/list/view"]["node_ids"], related)
}

// A Hub keg answers links with its fields and relationship pairs in one
// request to the Hub's TAP operation.
func TestHubLinksAreOneTapRequest(t *testing.T) {
	t.Parallel()
	rec := &hubRecorder{}
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/v1/tap/links" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(tapapi.RelatedResponse{
			Rows:  []keg.ListViewRow{{Entry: keg.NodeIndexEntry{ID: "7", Title: "Seven"}, Fields: map[string]string{"type": "idea"}}},
			Pairs: []keg.RelatedPair{{From: "0", To: "7"}},
			Total: 1,
		})
	}))
	t.Cleanup(srv.Close)
	sb := NewSandbox(t)
	tap := newTestTap(t, sb)
	k := keg.NewRemoteKeg(srv.URL+"/api/v1/@team/notes", "token", sb.Runtime())
	k.SetTarget(&keg.Target{Namespace: "team", KegName: "notes"})
	tap.KegResolver = func(context.Context, tapper.KegTargetOptions, tapper.FlightRole) (keg.Keg, error) { return k, nil }

	var observed []tapper.Relationship
	ctx := tapper.WithRelationshipResultObserver(t.Context(), func(_ context.Context, _ keg.Keg, rows []tapper.Relationship) { observed = rows })
	lines, err := tap.Links(ctx, tapper.LinksOptions{NodeIDs: []string{"0"}, Format: "%i %{type}", Limit: 5})
	require.NoError(t, err)
	require.Equal(t, []string{"7 idea"}, lines)
	require.Equal(t, []string{"/api/v1/tap/links"}, rec.recorded())
	body := rec.bodies["/api/v1/tap/links"]
	require.Equal(t, "@team/notes", body["keg"])
	require.Equal(t, []any{"type"}, body["fields"])
	require.EqualValues(t, 5, body["limit"])
	require.Equal(t, []tapper.Relationship{{Source: keg.NodeId{ID: 0}, Target: keg.NodeId{ID: 7}}}, observed)
}
