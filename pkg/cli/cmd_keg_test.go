package cli_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestKegRenameCommand(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]string{"namespace": "jlrickert", "alias": "renamed"})
	}))
	defer srv.Close()

	sb := NewSandbox(t)
	sb.MustWriteFile("~/.config/tapper/config.yaml", []byte(fmt.Sprintf(`hubs:
  atlas:
    kind: remote
    url: %s
    token: tok
defaultHub: atlas
defaultNamespace: jlrickert
namespaces:
  jlrickert:
    hub: atlas
`, srv.URL)), 0o644)

	res := NewProcess(t, false, "keg", "rename", "@jlrickert/example", "renamed").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "/api/v1/@jlrickert/kegs/example/rename", gotPath)
	require.Equal(t, map[string]string{"alias": "renamed"}, gotBody)
}

func TestKegRenameCompletion_OldArgListsKegs(t *testing.T) {
	t.Parallel()

	sb := NewRemoteKegListSandbox(t, remoteCompletionKegs())

	comp := NewCompletionProcess(t, false, 0, "keg", "rename", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "@team/example")
	require.Contains(t, suggestions, "@team/personal")
	require.Contains(t, suggestions, "@team/work")
	require.Contains(t, suggestions, "example")
	require.Contains(t, suggestions, "personal")
	require.Contains(t, suggestions, "work")
	require.Contains(t, string(comp.Stdout), fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp))
}

func TestKegRenameCompletion_NewArgSuppressesFileCompletion(t *testing.T) {
	t.Parallel()

	sb := NewRemoteKegListSandbox(t, remoteCompletionKegs())

	comp := NewCompletionProcess(t, false, 0, "keg", "rename", "@team/personal", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	require.Empty(t, parseCompletionSuggestions(string(comp.Stdout)))
	require.Contains(t, string(comp.Stdout), fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp))
}
