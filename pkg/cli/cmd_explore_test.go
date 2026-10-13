package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestExploreCLI_SearchesEachPublicDirectorySection(t *testing.T) {
	hub := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer hub-token", r.Header.Get("Authorization"))
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/explore" {
			http.NotFound(w, r)
			return
		}
		var in tapper.ExploreRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&in))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tapper.ExploreResult{
			Kind:      in.Kind,
			Results:   []tapper.ExploreRow{{Ref: "@world/" + string(in.Kind), Name: "Match for " + in.Query, Description: "public"}},
			Truncated: in.Kind == tapper.ExplorePeople,
		})
	}))
	defer hub.Close()
	sb := NewSandbox(t)
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(fmt.Sprintf("hub: test\nhubs:\n  test: {url: %s, token: hub-token}\n", hub.URL)), 0600))

	for _, kind := range []string{"kegs", "flights", "people"} {
		res := NewProcess(t, false, "explore", kind, "go").Run(sb.Context(), sb.Runtime())
		require.NoError(t, res.Err, kind)
		require.Equal(t, "@world/"+kind+"\tMatch for go\tpublic\t\n", string(res.Stdout), kind)
		if kind == "people" {
			require.Contains(t, string(res.Stderr), "results truncated")
		}
	}

	missing := NewProcess(t, false, "explore", "kegs").Run(sb.Context(), sb.Runtime())
	require.Error(t, missing.Err, "the public directory is searched, never browsed in full")
}
