package tapper_test

import (
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestHubListKegs_LocalScan(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(),
		[]byte("hubs:\n  home:\n    kind: local\n    namespace: local\n    basePath: /home/testuser/kegs\n"), 0o644))

	// Two kegs in two namespaces (a keg dir is one carrying a "keg" config file).
	require.NoError(t, fx.Runtime().AtomicWriteFile("/home/testuser/kegs/@local/notes/keg", []byte("kegv: 2023-01\n"), 0o644))
	require.NoError(t, fx.Runtime().AtomicWriteFile("/home/testuser/kegs/@work/blog/keg", []byte("kegv: 2023-01\n"), 0o644))
	// A directory that is not a keg (no keg config) must be ignored.
	require.NoError(t, fx.Runtime().AtomicWriteFile("/home/testuser/kegs/@local/scratch/notes.txt", []byte("x"), 0o644))
	// flights.d sits beside the namespaces and must never be listed as a keg.
	require.NoError(t, fx.Runtime().AtomicWriteFile("/home/testuser/kegs/flights.d/backend.yaml", []byte("title: B\n"), 0o644))

	kegs, err := tap.HubListKegs(fx.Context(), tapper.HubListOptions{})
	require.NoError(t, err)
	require.Equal(t, []string{"@local/notes", "@work/blog"}, kegs)
}

func TestHubListKegs_RemoteNotSupported(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	require.NoError(t, fx.Setwd("/home/testuser"))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    "/home/testuser",
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(),
		[]byte("hubs:\n  atlas:\n    kind: remote\n    url: https://atlas.foldwise.ai\n"), 0o644))

	_, err = tap.HubListKegs(fx.Context(), tapper.HubListOptions{Hub: "atlas"})
	require.Error(t, err, "remote hub listing is not yet supported")
}
