package cli_test

import (
	"encoding/json"
	"fmt"
	"github.com/jlrickert/tapper/internal/testapi"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// hubCreateRequest is one item of the POST /nodes batch tapper sends.
type hubCreateRequest struct {
	Key     string `json:"key"`
	Schema  string `json:"schema,omitempty"`
	Content string `json:"content"`
	Meta    string `json:"meta,omitempty"`
}

// newTitleEnforcingHub stands up a hub that applies the one rule this bug ran
// into: POST /nodes refuses an item whose content carries no level-1 heading,
// because a node's only title source is its content's H1. It records every
// create it is asked to perform so a test can assert on what the client sent.
func newTitleEnforcingHub(t *testing.T, seen *[]hubCreateRequest) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	return testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/nodes") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			Nodes []hubCreateRequest `json:"nodes"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		mu.Lock()
		*seen = append(*seen, body.Nodes...)
		mu.Unlock()

		out := make([]map[string]any, 0, len(body.Nodes))
		for i, item := range body.Nodes {
			if !hasMarkdownTitle(item.Content) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": fmt.Sprintf("batch item %d (key %q): node title is required", i, item.Key),
					"code":  "BAD_REQUEST",
				})
				return
			}
			out = append(out, map[string]any{"key": item.Key, "id": i + 1, "hash": "hash-1"})
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(out)
	}))
}

func hasMarkdownTitle(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "# ") {
			return true
		}
	}
	return false
}

func writeHubConfig(t *testing.T, sb *testutils.Sandbox, url string) {
	t.Helper()
	sb.MustWriteFile("~/.config/tapper/config.yaml", []byte(fmt.Sprintf(`hub: test
keg: "@team/notes"
hubs:
  test:
    url: %s
    token: test-token
`, url)), 0o644)
}

// TestCreate_AgainstHubSendsATitle is the regression test for the reported bug:
//
//	❯ tap c
//	Error: unable to create node: CreateNodes POST .../nodes:
//	batch item 0 (key "node"): node title is required
//
// create used to persist an empty scaffold node and only then open the editor
// on it. A hub refuses that create outright, so the author was told to supply a
// title they were never given a chance to type. Building the node from the
// buffer instead means the first thing the hub ever sees already has one.
//
// This has to run against a hub-shaped server: the rest of the create suite
// resolves to an in-memory LocalKeg, which quietly substitutes a default
// heading and so cannot fail this way.
func TestCreate_AgainstHubSendsATitle(t *testing.T) {
	var seen []hubCreateRequest
	srv := newTitleEnforcingHub(t, &seen)
	defer srv.Close()

	sb := NewSandbox(t)
	writeHubConfig(t, sb, srv.URL)
	editorScript(t, sb, "hub-create", "#!/bin/sh\n"+
		"printf '%s' '---\ntags:\n  - remote\n---\n# Titled By The Author\n\nBody.\n' > \"$1\"\n")

	res := NewHubProcess(t, true, "create").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err, "stderr=%q", string(res.Stderr))

	require.Len(t, seen, 1, "exactly one create reaches the hub")
	require.Contains(t, seen[0].Content, "# Titled By The Author")
	require.Contains(t, seen[0].Meta, "remote", "frontmatter is sent as meta, not content")
	// The scaffold must never reach the hub: an untitled create is what the
	// server rejects, and nothing should be persisted before the author writes.
	require.True(t, hasMarkdownTitle(seen[0].Content))
}

// TestCreate_AgainstHubWithoutTerminalDoesNotCallIt pins the non-interactive
// half. With neither piped content nor a terminal there is nothing to title a
// node with, so the client says so instead of sending an empty create and
// surfacing the hub's 400.
func TestCreate_AgainstHubWithoutTerminalDoesNotCallIt(t *testing.T) {
	var seen []hubCreateRequest
	srv := newTitleEnforcingHub(t, &seen)
	defer srv.Close()

	sb := NewSandbox(t)
	writeHubConfig(t, sb, srv.URL)

	res := NewHubProcess(t, false, "create").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "no content to create a node from")
	require.Empty(t, seen, "a create with nothing to create from must not reach the hub at all")
}

// TestCreate_AgainstHubPipedKeepsWorking guards the path that was never broken,
// so the fix cannot regress it.
func TestCreate_AgainstHubPipedKeepsWorking(t *testing.T) {
	var seen []hubCreateRequest
	srv := newTitleEnforcingHub(t, &seen)
	defer srv.Close()

	sb := NewSandbox(t)
	writeHubConfig(t, sb, srv.URL)
	// An editor that would fail the test if it were ever launched.
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	res := NewHubProcess(t, true, "create").RunWithIO(sb.Context(), sb.Runtime(),
		strings.NewReader("# Piped Title\n\nPiped body.\n"))
	require.NoError(t, res.Err, "stderr=%q", string(res.Stderr))

	require.Len(t, seen, 1)
	require.Contains(t, seen[0].Content, "# Piped Title")
}
