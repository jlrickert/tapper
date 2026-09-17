package parity_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/stretchr/testify/require"
)

func TestParity_KegCreateQualifiedReference(t *testing.T) {
	e := newParityEnv(t)
	var mu sync.Mutex
	var destinations []string
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		mu.Lock()
		destinations = append(destinations, r.URL.Path+"/"+body["alias"])
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	e.sb.MustWriteFile("~/.config/tapper/config.yaml", []byte(fmt.Sprintf("hub: home\ndefaultNamespace: ignored\nfallbackNamespace: ignored\nhubs:\n  home:\n    url: %s\n    token: test-token\n    defaultNamespace: ignored\n", srv.URL)), 0644)
	e.tap.ConfigService.Reload()
	for _, ref := range []string{"@acme/engineering", "bare", "@acme", "@/notes", "@acme/", "@acme/notes/extra", "acme/notes", "@Bad/notes", "@acme/bad.name"} {
		t.Run(ref, func(t *testing.T) {
			_, cliErr := e.runCLI("keg", "create", ref)
			_, mcpErr := e.runMCP("keg_create", map[string]any{"keg": ref})
			if ref == "@acme/engineering" {
				require.NoError(t, cliErr)
				require.NoError(t, mcpErr)
			} else {
				// Parity is in which references are rejected, not in the
				// wording: the CLI names the command, while the MCP surface
				// must not, because an agent has no CLI to run.
				require.ErrorContains(t, cliErr, "tap keg create @namespace/keg")
				require.ErrorContains(t, mcpErr, "expected @namespace/keg")
				require.NotContains(t, mcpErr.Error(), "tap keg create")
			}
		})
	}
	for _, args := range [][]string{
		{"keg", "create"},
		{"keg", "create", "@acme/engineering", "extra"},
		{"keg", "create", "bare", "--namespace", "acme"},
		{"--namespace", "other", "keg", "create", "@acme/engineering"},
		{"keg", "create", "@acme/engineering", "--namespace="},
	} {
		_, err := e.runCLI(args...)
		require.ErrorContains(t, err, "tap keg create @namespace/keg")
	}
	_, err := e.runMCP("keg_create", map[string]any{"keg": "@acme/engineering", "namespace": "acme"})
	require.Error(t, err)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"/api/v1/@acme/kegs/engineering", "/api/v1/@acme/kegs/engineering"}, destinations)
}
