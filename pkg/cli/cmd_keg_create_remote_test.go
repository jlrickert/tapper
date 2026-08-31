package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKegCreateUsesConfiguredHubExclusively(t *testing.T) {
	t.Parallel()

	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/v1/@team/kegs", r.URL.Path)
		require.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	sb := NewSandbox(t)
	sb.MustWriteFile("~/.config/tapper/config.yaml", []byte(fmt.Sprintf(`fallbackHub: test
fallbackNamespace: team
hubs:
  test:
    kind: remote
    url: %s
    token: test-token
`, srv.URL)), 0o644)

	res := NewProcess(t, false, "keg", "create", "notes", "--title", "Team Notes", "--visibility", "private").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "stderr=%q", res.Stderr)
	require.Equal(t, map[string]string{
		"alias": "notes", "title": "Team Notes", "visibility": "private",
	}, got)
	require.Contains(t, string(res.Stdout), "keg notes created")
	require.Contains(t, string(res.Stdout), "keg:@team/notes")

	config := string(sb.MustReadFile("~/.config/tapper/config.yaml"))
	require.Contains(t, config, "team:")
	require.Contains(t, config, "hub: test")
}

func TestKegCreateRejectsRemovedLocalSurfaces(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"init", "notes"},
		{"keg", "create", "notes", "--project"},
		{"keg", "create", "notes", "--user"},
		{"keg", "create", "notes", "--cwd"},
		{"keg", "create", "notes", "--path", "/tmp/notes"},
	} {
		args := args
		t.Run(fmt.Sprintf("%v", args), func(t *testing.T) {
			t.Parallel()
			sb := NewSandbox(t)
			res := NewProcess(t, false, args...).Run(sb.Context(), sb.Runtime())
			require.Error(t, res.Err)
		})
	}
}

func TestKegCreateRejectsReadonlyAndInvalidAlias(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)
	sb.MustWriteFile("~/.config/tapper/config.yaml", []byte(`fallbackHub: archive
fallbackNamespace: team
hubs:
  archive:
    kind: readonly
    url: https://archive.example.com
`), 0o644)

	readonly := NewProcess(t, false, "keg", "create", "notes").Run(sb.Context(), sb.Runtime())
	require.Error(t, readonly.Err)
	require.Contains(t, readonly.Err.Error(), "does not support KEG creation")

	invalid := NewProcess(t, false, "keg", "create", "Bad.Name").Run(sb.Context(), sb.Runtime())
	require.Error(t, invalid.Err)
	require.Contains(t, invalid.Err.Error(), "invalid keg alias")
}
