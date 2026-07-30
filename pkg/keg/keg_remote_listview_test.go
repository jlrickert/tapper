package keg_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	kegpkg "github.com/jlrickert/tapper/pkg/keg"
)

// pathRecorder records the path of every request it serves, so a test can
// assert how many round trips an operation costs.
type pathRecorder struct {
	mu     sync.Mutex
	paths  []string
	handle func(w http.ResponseWriter, r *http.Request)
	srv    *httptest.Server
}

func newPathRecorder(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) *pathRecorder {
	t.Helper()
	rec := &pathRecorder{handle: handle}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.paths = append(rec.paths, r.URL.Path)
		rec.mu.Unlock()
		rec.handle(w, r)
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

func (rec *pathRecorder) recorded() []string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]string(nil), rec.paths...)
}

func newRecorderKeg(t *testing.T, rec *pathRecorder) *kegpkg.RemoteKeg {
	t.Helper()
	fx := NewSandbox(t)
	return kegpkg.NewRemoteKeg(rec.srv.URL, "token", fx.Runtime())
}

// TestRemoteListViewIsOneRoundTrip is a regression guard for an N+1 that
// shipped once already: rendering a metadata column used to call GetMeta per
// node, so a 610-node listing made 610 sequential HTTP requests and took ~49s
// against a real hub. The whole point of ListView is that the server resolves
// the page, so the cost must not scale with the number of rows.
func TestRemoteListViewIsOneRoundTrip(t *testing.T) {
	t.Parallel()

	const nodeCount = 200
	rows := make([]kegpkg.ListViewRow, 0, nodeCount)
	for i := range nodeCount {
		rows = append(rows, kegpkg.ListViewRow{
			Entry:  kegpkg.NodeIndexEntry{ID: itoa(i), Title: "Node"},
			Fields: map[string]string{"type": "note"},
		})
	}

	rec := newPathRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(kegpkg.ListViewResult{
			Rows:         rows,
			TotalMatches: nodeCount,
			IndexedCount: nodeCount,
			NodeCount:    nodeCount,
		})
	})

	k := newRecorderKeg(t, rec)
	out, err := k.ListView(context.Background(), kegpkg.ListViewOptions{Fields: []string{"type"}})
	require.NoError(t, err)
	require.Len(t, out.Rows, nodeCount)
	require.Equal(t, "note", out.Rows[0].Fields["type"])

	paths := rec.recorded()
	require.Len(t, paths, 1, "ListView must cost exactly one request regardless of row count, got %v", paths)
	require.Equal(t, "/list/view", paths[0])

	// No per-node reads may leak in behind the projection.
	for _, path := range paths {
		require.NotContains(t, path, "/meta")
		require.NotContains(t, path, "/stats")
	}
}

// TestRemoteListViewUnsupportedIsDetectable proves a hub that predates the
// route is reported distinctly, so callers degrade to assembling the listing
// themselves instead of surfacing a bare 404.
func TestRemoteListViewUnsupportedIsDetectable(t *testing.T) {
	t.Parallel()

	rec := newPathRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	k := newRecorderKeg(t, rec)
	_, err := k.ListView(context.Background(), kegpkg.ListViewOptions{Fields: []string{"type"}})
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrListViewUnsupported)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
