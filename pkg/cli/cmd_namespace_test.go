package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNamespaceCreateCmd_PrintsHubUIURL(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	sb.MustWriteFile("~/.config/tapper/config.yaml", []byte(`fallbackHub: atlas
hubs:
  atlas:
    kind: remote
    url: https://hub.example.com
`), 0o644)

	proc := newAuthProcess(t, nil, "namespace", "create", "acme")
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, "Create @acme in the hub UI:\nhttps://hub.example.com/namespaces/new?name=acme\n", string(res.Stdout))
}
