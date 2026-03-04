package tapper_test

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// setupTapWithKeg creates a Tap instance with a keg initialized at
// ~/kegs/test inside the sandbox.
func setupTapWithKeg(t *testing.T, fx *sandbox.Sandbox) *tapper.Tap {
	t.Helper()

	root := "/home/testuser/work"
	require.NoError(t, fx.Runtime().Mkdir(root, 0o755, true))
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	// Write user config with kegSearchPaths and fallback.
	userCfg := `fallbackKeg: test
kegs: {}
defaultRegistry: ""
kegSearchPaths:
  - /home/testuser/kegs
`
	require.NoError(t, fx.Runtime().Mkdir(tap.PathService.ConfigRoot, 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))

	// Create keg directory. Discovery needs a keg file, but Init writes one.
	// We use Resolve with explicit URL to skip discovery, then Init creates
	// a proper config file.
	kegDir := "/home/testuser/kegs/test"
	require.NoError(t, fx.Runtime().Mkdir(kegDir, 0o755, true))

	k, err := keg.NewKegFromTarget(fx.Context(), kegurl.NewFile(kegDir), fx.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(fx.Context()))

	return tap
}

// TestTapCreate_Concurrent verifies concurrent Tap.Create calls with piped
// stdin all produce unique nodes.
func TestTapCreate_Concurrent(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	tap := setupTapWithKeg(t, fx)

	const N = 10
	ids := make([]keg.NodeId, N)
	errs := make([]error, N)

	var wg sync.WaitGroup
	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			content := fmt.Sprintf("# Piped Node %d\n\nCreated via piped stdin.\n", idx)
			stream := &toolkit.Stream{
				In:      io.NopCloser(bytes.NewReader([]byte(content))),
				IsPiped: true,
			}
			id, err := tap.Create(fx.Context(), tapper.CreateOptions{
				Stream: stream,
			})
			ids[idx] = id
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d failed Create", i)
	}

	seen := make(map[int]bool)
	for i, id := range ids {
		require.False(t, seen[id.ID], "duplicate ID %d from goroutine %d", id.ID, i)
		seen[id.ID] = true
	}
}

// TestTapEdit_ConcurrentDifferentNodes verifies concurrent Tap edit operations
// on different nodes via piped stdin.
func TestTapEdit_ConcurrentDifferentNodes(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	tap := setupTapWithKeg(t, fx)

	// Pre-create nodes.
	const N = 5
	nodeIDs := make([]string, N)
	for i := range N {
		stream := &toolkit.Stream{
			In:      io.NopCloser(bytes.NewReader([]byte(fmt.Sprintf("# Node %d\n\nInitial.\n", i)))),
			IsPiped: true,
		}
		id, err := tap.Create(fx.Context(), tapper.CreateOptions{
			Stream: stream,
		})
		require.NoError(t, err)
		nodeIDs[i] = id.String()
	}

	// Concurrent edits.
	var wg sync.WaitGroup
	errs := make([]error, N)
	for i := range N {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			content := fmt.Sprintf("# Edited Node %d\n\nEdited content.\n", idx)
			stream := &toolkit.Stream{
				In:      io.NopCloser(bytes.NewReader([]byte(content))),
				IsPiped: true,
			}
			err := tap.Edit(fx.Context(), tapper.EditOptions{
				NodeID: nodeIDs[idx],
				Stream: stream,
			})
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "goroutine %d failed Edit", i)
	}
}
