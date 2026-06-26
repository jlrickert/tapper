package tapper_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// flightEditFixture is a fake hub serving GET/PUT for @foldwise/+agent-work,
// recording every PUT body so tests can assert exactly what was applied.
type flightEditFixture struct {
	srv *httptest.Server

	mu      sync.Mutex
	current tapper.HubFlight
	puts    []tapper.HubFlight
}

func newFlightEditFixture(t *testing.T) *flightEditFixture {
	t.Helper()
	fx := &flightEditFixture{
		current: tapper.HubFlight{
			Namespace:    "foldwise",
			Slug:         "agent-work",
			Title:        "Agent Work",
			Instructions: "Stay inside the cover.",
			Cover:        []tapper.HubFlightCover{{Namespace: "foldwise", Keg: "docs", Role: "viewer"}},
		},
	}
	fx.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/@foldwise/+agent-work":
			fx.mu.Lock()
			current := fx.current
			fx.mu.Unlock()
			_ = json.NewEncoder(w).Encode(current)
		case "PUT /api/v1/@foldwise/+agent-work":
			var body tapper.HubFlight
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			body.Namespace = "foldwise"
			body.Slug = "agent-work"
			fx.mu.Lock()
			fx.puts = append(fx.puts, body)
			fx.current = body
			fx.mu.Unlock()
			_ = json.NewEncoder(w).Encode(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fx.srv.Close)
	return fx
}

func (fx *flightEditFixture) setCurrent(hf tapper.HubFlight) {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	fx.current = hf
}

func (fx *flightEditFixture) putCount() int {
	fx.mu.Lock()
	defer fx.mu.Unlock()
	return len(fx.puts)
}

func (fx *flightEditFixture) lastPut(t *testing.T) tapper.HubFlight {
	t.Helper()
	fx.mu.Lock()
	defer fx.mu.Unlock()
	require.NotEmpty(t, fx.puts)
	return fx.puts[len(fx.puts)-1]
}

func newFlightEditTap(t *testing.T, hubURL string) (*tapper.Tap, *sandbox.Sandbox) {
	t.Helper()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))
	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)
	cfg := fmt.Sprintf("hubs:\n  cloud:\n    kind: remote\n    url: %s\n    token: tok\n", hubURL)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(cfg), 0o644))
	return tap, fx
}

func pipedStream(raw string) *toolkit.Stream {
	return &toolkit.Stream{In: strings.NewReader(raw), IsPiped: true}
}

func TestEditFlight_PipedAppliesManifest(t *testing.T) {
	t.Parallel()
	hub := newFlightEditFixture(t)
	tap, fx := newFlightEditTap(t, hub.srv.URL)

	manifest := `title: Reworked
cover:
  - namespace: foldwise
    keg: docs
    role: editor
  - keg: notes
    role: viewer
instructions: |
  New instructions.
`
	flight, err := tap.EditFlight(fx.Context(), tapper.EditFlightOptions{
		Ref:    "@foldwise/+agent-work",
		Stream: pipedStream(manifest),
	})
	require.NoError(t, err)
	require.Equal(t, "@foldwise/+agent-work", flight.Name)
	require.Equal(t, "Reworked", flight.Title)

	put := hub.lastPut(t)
	require.Equal(t, "Reworked", put.Title)
	require.Equal(t, "New instructions.\n", put.Instructions)
	require.Equal(t, []tapper.HubFlightCover{
		{Namespace: "foldwise", Keg: "docs", Role: "editor"},
		{Keg: "notes", Role: "viewer"},
	}, put.Cover)
}

func TestEditFlight_PipedIdenticalManifestIsNoop(t *testing.T) {
	t.Parallel()
	hub := newFlightEditFixture(t)
	tap, fx := newFlightEditTap(t, hub.srv.URL)

	// Reconstruct the exact document EditFlight renders for the current
	// flight: marshal the same manifest shape the hub fixture serves.
	current, err := yaml.Marshal(tapper.FlightManifest{
		Title:        "Agent Work",
		Cover:        []tapper.FlightCover{{Namespace: "foldwise", Keg: "docs", Role: tapper.FlightRoleViewer}},
		Instructions: "Stay inside the cover.",
	})
	require.NoError(t, err)

	flight, err := tap.EditFlight(fx.Context(), tapper.EditFlightOptions{
		Ref:    "@foldwise/+agent-work",
		Stream: pipedStream(string(current)),
	})
	require.NoError(t, err)
	require.Equal(t, "Agent Work", flight.Title)
	require.Zero(t, hub.putCount(), "identical manifest must not PUT")
}

func TestEditFlight_PipedModelineAndCommentsParseCleanly(t *testing.T) {
	t.Parallel()
	hub := newFlightEditFixture(t)
	tap, fx := newFlightEditTap(t, hub.srv.URL)

	manifest := fmt.Sprintf(`# yaml-language-server: $schema=%s
# Comments and modelines are editor guidance, not manifest fields.
title: Reworked
cover: []
instructions: from stdin
`, tapper.FlightManifestSchemaURL)

	flight, err := tap.EditFlight(fx.Context(), tapper.EditFlightOptions{
		Ref:    "@foldwise/+agent-work",
		Stream: pipedStream(manifest),
	})
	require.NoError(t, err)
	require.Equal(t, "Reworked", flight.Title)

	put := hub.lastPut(t)
	require.Equal(t, "Reworked", put.Title)
	require.Equal(t, "from stdin", put.Instructions)
	require.Empty(t, put.Cover)
}

func TestEditFlight_PipedRejectsInvalidManifests(t *testing.T) {
	t.Parallel()
	hub := newFlightEditFixture(t)
	tap, fx := newFlightEditTap(t, hub.srv.URL)

	cases := []struct {
		name     string
		manifest string
		wantErr  string
	}{
		{
			name:     "broken yaml",
			manifest: "title: [\n",
			wantErr:  "flight manifest is invalid",
		},
		{
			name:     "slug is not editable",
			manifest: "slug: other-flight\ntitle: Hijack\n",
			wantErr:  "field slug not found",
		},
		{
			name:     "unknown cover role",
			manifest: "cover:\n  - keg: docs\n    role: admin\n",
			wantErr:  `invalid flight cover role "admin"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tap.EditFlight(fx.Context(), tapper.EditFlightOptions{
				Ref:    "@foldwise/+agent-work",
				Stream: pipedStream(tc.manifest),
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
	require.Zero(t, hub.putCount(), "invalid manifests must not PUT")
}

func TestEditFlight_EditorSaveApplies(t *testing.T) {
	t.Parallel()
	hub := newFlightEditFixture(t)
	tap, fx := newFlightEditTap(t, hub.srv.URL)

	jail := fx.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	scriptPath := filepath.Join(jail, "edit-flight.sh")
	script := `#!/bin/sh
cat > "$1" <<'EOF'
title: Edited In Editor
cover:
  - keg: docs
    role: editor
instructions: From the editor.
EOF
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, fx.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	fx.Runtime().Unset("VISUAL")

	flight, err := tap.EditFlight(fx.Context(), tapper.EditFlightOptions{Ref: "@foldwise/+agent-work"})
	require.NoError(t, err)
	require.Equal(t, "Edited In Editor", flight.Title)

	put := hub.lastPut(t)
	require.Equal(t, "Edited In Editor", put.Title)
	require.Equal(t, "From the editor.", put.Instructions)
	require.Equal(t, []tapper.HubFlightCover{{Keg: "docs", Role: "editor"}}, put.Cover)
}

func TestEditFlight_EditorStartsWithSchemaBackedManifest(t *testing.T) {
	t.Parallel()
	hub := newFlightEditFixture(t)
	hub.setCurrent(tapper.HubFlight{Namespace: "foldwise", Slug: "agent-work"})
	tap, fx := newFlightEditTap(t, hub.srv.URL)

	jail := fx.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	capturePath := filepath.Join(jail, "captured-flight.yaml")
	scriptPath := filepath.Join(jail, "capture-flight.sh")
	script := fmt.Sprintf("#!/bin/sh\ncp \"$1\" %q\n", capturePath)
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, fx.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	fx.Runtime().Unset("VISUAL")

	flight, err := tap.EditFlight(fx.Context(), tapper.EditFlightOptions{Ref: "@foldwise/+agent-work"})
	require.NoError(t, err)
	require.Equal(t, "@foldwise/+agent-work", flight.Name)

	raw, err := os.ReadFile(capturePath)
	require.NoError(t, err)
	opened := string(raw)
	require.True(t, strings.HasPrefix(opened, "# yaml-language-server: $schema="+tapper.FlightManifestSchemaURL+"\n"))
	require.Contains(t, opened, "# Flight @foldwise/+agent-work. Ref is immutable; edit title, cover, and instructions.")
	require.Contains(t, opened, `title: ""`)
	require.Contains(t, opened, "cover: []")
	require.Contains(t, opened, `instructions: ""`)
	require.NotContains(t, opened, "{}")
	require.Zero(t, hub.putCount(), "unchanged starter manifest must not PUT")
}

func TestEditFlight_EditorNoChangeIsNoop(t *testing.T) {
	t.Parallel()
	hub := newFlightEditFixture(t)
	tap, fx := newFlightEditTap(t, hub.srv.URL)

	require.NoError(t, fx.Runtime().Set("EDITOR", "/bin/sh -c true"))
	fx.Runtime().Unset("VISUAL")

	flight, err := tap.EditFlight(fx.Context(), tapper.EditFlightOptions{Ref: "@foldwise/+agent-work"})
	require.NoError(t, err)
	require.Equal(t, "Agent Work", flight.Title)
	require.Zero(t, hub.putCount(), "exiting without changes must not PUT")
}

func TestEditFlight_EditorCommentOnlyChangeIsSemanticNoop(t *testing.T) {
	t.Parallel()
	hub := newFlightEditFixture(t)
	tap, fx := newFlightEditTap(t, hub.srv.URL)

	jail := fx.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	scriptPath := filepath.Join(jail, "flight-comment-only.sh")
	script := fmt.Sprintf(`#!/bin/sh
cat > "$1" <<'EOF'
# yaml-language-server: $schema=%s
# Different editor-only comment.
title: Agent Work
cover:
  - namespace: foldwise
    keg: docs
    role: viewer
instructions: Stay inside the cover.
EOF
`, tapper.FlightManifestSchemaURL)
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, fx.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	fx.Runtime().Unset("VISUAL")

	flight, err := tap.EditFlight(fx.Context(), tapper.EditFlightOptions{Ref: "@foldwise/+agent-work"})
	require.NoError(t, err)
	require.Equal(t, "Agent Work", flight.Title)
	require.Zero(t, hub.putCount(), "comment/modeline-only edits must not PUT")
}
