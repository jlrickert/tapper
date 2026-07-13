package tapper

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

func TestIntegratePluginsFromFSDiscoversThirdMarketplaceFixture(t *testing.T) {
	t.Parallel()
	marketplace := []byte(`{"plugins":[{"name":"tapper-dev"},{"name":"fixture-third"},{"name":"tapper"}]}`)
	plugins, err := integratePluginsFromFS(fstest.MapFS{
		"rendered/claude/.claude-plugin/marketplace.json": &fstest.MapFile{Data: marketplace},
	}, "claude")
	require.NoError(t, err)
	require.Equal(t, []string{"fixture-third", "tapper", "tapper-dev"}, plugins)
}
