package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/tapapi"
)

const cliSearchReply = `{"hits":[` +
	`{"ref":"keg:@team/notes/12","keg":"@team/notes","node_id":"12","title":"Festival trip","score":4.25,"fields":["title"],"snippet":"festival in Los Angeles","highlights":[{"start":0,"end":8}]},` +
	`{"ref":"keg:@team/eng/3","keg":"@team/eng","node_id":"3","title":"Release","score":1,"fields":["body"]}` +
	`],"next_cursor":"c2","mode":"keyword","warnings":["one KEG was skipped"]}`

func newSearchHubSandbox(t *testing.T) (*searchHubRecorder, func(args ...string) searchRun) {
	t.Helper()
	rec := &searchHubRecorder{}
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1"+tapapi.PathSearch {
			http.NotFound(w, r)
			return
		}
		var req tapapi.SearchRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		rec.mu.Lock()
		rec.requests = append(rec.requests, req)
		rec.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(tapapi.SearchRouteHeader, tapapi.SearchRouteHeaderValue)
		_, _ = w.Write([]byte(cliSearchReply))
	}))
	t.Cleanup(srv.Close)
	sb := NewSandbox(t)
	sb.MustWriteFile("~/.config/tapper/config.yaml", []byte(fmt.Sprintf("hub: test\nhubs:\n  test:\n    url: %s\n    token: test-token\n", srv.URL)), 0o644)
	return rec, func(args ...string) searchRun {
		res := NewProcess(t, false, args...).Run(sb.Context(), sb.Runtime())
		return searchRun{Err: res.Err, Stdout: string(res.Stdout), Stderr: string(res.Stderr)}
	}
}

type searchHubRecorder struct {
	mu       sync.Mutex
	requests []tapapi.SearchRequest
}

func (r *searchHubRecorder) recorded() []tapapi.SearchRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]tapapi.SearchRequest(nil), r.requests...)
}

// searchRun is a string view of a finished CLI process.
type searchRun struct {
	Err            error
	Stdout, Stderr string
}

func TestSearchCommand_PrintsRankedHits(t *testing.T) {
	t.Parallel()
	rec, run := newSearchHubSandbox(t)

	res := run("node", "search", "festival", "trip", "--kegs", "@team/notes,@team/eng", "--limit", "2", "--cursor", "c1")
	require.NoError(t, res.Err, "stderr=%q", res.Stderr)
	require.Equal(t, "keg:@team/notes/12\t4.250\tFestival trip\t**festival** in Los Angeles\n"+
		"keg:@team/eng/3\t1.000\tRelease\t\n", res.Stdout)
	require.Contains(t, res.Stderr, "warning: one KEG was skipped")
	require.Contains(t, res.Stderr, "next cursor: c2")
	require.Equal(t, []tapapi.SearchRequest{{
		Query: "festival trip", Kegs: []string{"@team/notes", "@team/eng"}, Cursor: "c1", Limit: 2,
	}}, rec.recorded())
}

func TestSearchCommand_IDOnlyAndJSON(t *testing.T) {
	t.Parallel()
	_, run := newSearchHubSandbox(t)

	res := run("node", "search", "festival", "--id-only")
	require.NoError(t, res.Err, "stderr=%q", res.Stderr)
	require.Equal(t, "keg:@team/notes/12\nkeg:@team/eng/3\n", res.Stdout)

	res = run("node", "search", "festival", "--json")
	require.NoError(t, res.Err, "stderr=%q", res.Stderr)
	var resp tapapi.SearchResponse
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &resp))
	require.Len(t, resp.Hits, 2)
	require.Equal(t, "c2", resp.NextCursor)
	require.Equal(t, tapapi.SearchModeKeyword, resp.Mode)
	require.NotContains(t, res.Stderr, "next cursor", "--json carries the cursor in the document")

	res = run("node", "search", "festival", "--json", "--id-only")
	require.Error(t, res.Err)
}

func TestSearchCommand_RequiresAQuery(t *testing.T) {
	t.Parallel()
	_, run := newSearchHubSandbox(t)

	res := run("node", "search")
	require.Error(t, res.Err)
	require.Contains(t, res.Stderr, "requires at least 1 arg")
}
