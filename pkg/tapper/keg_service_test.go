package tapper_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestResolve_RemoteFallbackHubWithoutNamespaceDoesNotFallBackProjectAlias(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("example", "/home/testuser"))
	root := "/home/testuser/repos/github.com/jlrickert/tapper"
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().Mkdir(filepath.Join(root, "kegs", "dev"), 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(filepath.Join(root, "kegs", "dev", "keg"), []byte(""), 0o644))
	require.NoError(t, fx.Runtime().Mkdir(filepath.Dir(tap.PathService.UserConfig()), 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(`fallbackHub: atlas
hubs:
  atlas:
    kind: remote
    url: https://hub.example.com
`), 0o644))

	_, err = tap.KegService.Resolve(context.Background(), tapper.ResolveKegOptions{
		Root: root,
		Keg:  "dev",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no namespace")
}
