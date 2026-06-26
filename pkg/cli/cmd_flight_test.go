package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestFlightEdit_PipedStdinAppliesManifest(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	var put *tapper.HubFlight
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/@foldwise/+agent-work":
			_ = json.NewEncoder(w).Encode(tapper.HubFlight{
				Namespace: "foldwise",
				Slug:      "agent-work",
				Title:     "Agent Work",
			})
		case "PUT /api/v1/@foldwise/+agent-work":
			var body tapper.HubFlight
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			put = &body
			body.Namespace = "foldwise"
			body.Slug = "agent-work"
			_ = json.NewEncoder(w).Encode(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := fmt.Sprintf("hubs:\n  cloud:\n    kind: remote\n    url: %s\n    token: tok\n", srv.URL)
	sb.MustWriteFile("~/.config/tapper/config.yaml", []byte(cfg), 0o644)
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	stdin := strings.NewReader("title: Piped Title\ninstructions: piped instructions\n")
	res := NewProcess(t, false, "flight", "edit", "@foldwise/+agent-work").
		RunWithIO(sb.Context(), sb.Runtime(), stdin)
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "@foldwise/+agent-work")

	require.NotNil(t, put, "piped flight edit must PUT the manifest")
	require.Equal(t, "Piped Title", put.Title)
	require.Equal(t, "piped instructions", put.Instructions)
}

func TestFlightEdit_EditorStartsWithSchemaManifest(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	var putCalled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/@foldwise/+agent-work":
			_ = json.NewEncoder(w).Encode(tapper.HubFlight{
				Namespace: "foldwise",
				Slug:      "agent-work",
			})
		case "PUT /api/v1/@foldwise/+agent-work":
			putCalled.Store(true)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := fmt.Sprintf("hubs:\n  cloud:\n    kind: remote\n    url: %s\n    token: tok\n", srv.URL)
	sb.MustWriteFile("~/.config/tapper/config.yaml", []byte(cfg), 0o644)

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	capturePath := filepath.Join(jail, "flight-edit-opened.yaml")
	captureBasenamePath := filepath.Join(jail, "flight-edit-opened.basename")
	scriptPath := filepath.Join(jail, "capture-flight-edit.sh")
	script := fmt.Sprintf("#!/bin/sh\ncp \"$1\" %q\nbasename \"$1\" > %q\n", capturePath, captureBasenamePath)
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	res := NewProcess(t, false, "flight", "edit", "@foldwise/+agent-work").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "@foldwise/+agent-work")

	raw, err := os.ReadFile(capturePath)
	require.NoError(t, err)
	basenameRaw, err := os.ReadFile(captureBasenamePath)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(strings.TrimSpace(string(basenameRaw)), "tap-flight-edit-foldwise-agent-work-"))
	opened := string(raw)
	require.True(t, strings.HasPrefix(opened, "# yaml-language-server: $schema="+tapper.FlightManifestSchemaURL+"\n"))
	require.Contains(t, opened, `title: ""`)
	require.Contains(t, opened, "cover: []")
	require.Contains(t, opened, `instructions: ""`)
	require.NotContains(t, opened, "{}")
	require.False(t, putCalled.Load(), "unchanged schema-backed starter must not PUT")
}

func TestFlightUpdate_CommandRemoved(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	// An unknown flight subcommand prints the group help; the listing must
	// offer edit and no longer offer update.
	res := NewProcess(t, false, "flight", "update", "@foldwise/+agent-work").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)
	out := string(res.Stdout)
	require.Contains(t, out, "edit a Hub-backed flight's manifest")
	require.NotContains(t, out, "update a Hub-backed flight",
		"flight update was replaced by flight edit")
}
