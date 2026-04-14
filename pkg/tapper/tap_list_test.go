package tapper_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// TestList_ReflectsExternalWrites is the regression test for F1 of report
// node 842 (and for the original [issue 327] reproduction). It verifies
// that Tap.List — which in a long-lived MCP server is called repeatedly
// against a cached dex — observes nodes written by another process between
// calls.
//
// The bug: Tap.List used k.Dex(ctx), the cache-only fast path, so after
// an external writer added nodes the next List call on the MCP server
// returned the stale cached view. The fix flips Tap.List (and the other
// MCP read surfaces) to k.DexFresh(ctx), which checks the mtime of
// dex/nodes.tsv and reloads when the on-disk index has changed.
//
// The two Tap instances below share the same FsRepo root but each has
// its own KegService cache, so they materialise distinct *keg.Keg
// objects with independent dex caches — this faithfully simulates the
// cross-process scenario where the MCP server and a CLI writer both
// hold their own Keg instance.
//
// [issue 327]: ../../../keg-dev/keg/nodes/327 (resolved) — original stale
// dex reproduction.
func TestList_ReflectsExternalWrites(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	// Build the shared keg on disk via a setup Tap — this writes both the
	// tap user config and the keg config/zero node.
	setup := setupTapWithKeg(t, fx)
	_ = setup

	// Two independent Tap instances with their own KegService caches,
	// each resolving to the same FsRepo root configured by the setup
	// above (kegSearchPaths → /home/testuser/kegs/test).
	newTap := func() *tapper.Tap {
		tap, err := tapper.NewTap(tapper.TapOptions{
			Root:    "/home/testuser/work",
			Runtime: fx.Runtime(),
		})
		require.NoError(t, err)
		return tap
	}
	tapA := newTap()
	tapB := newTap()

	// Tap A lists first, populating its cached dex with the initial
	// (empty-apart-from-zero-node) state.
	initial, err := tapA.List(fx.Context(), tapper.ListOptions{})
	require.NoError(t, err)
	// Initial list may contain the zero node — the assertion below cares
	// only about the three new nodes not the starting state.
	_ = initial

	// Tap B creates three nodes. Because tapB has its own *Keg and
	// therefore its own dex cache, this is structurally equivalent to a
	// separate CLI process writing through FsRepo.
	createVia := func(tp *tapper.Tap, title string) string {
		content := "# " + title + "\n\nBody for " + title + ".\n"
		stream := &toolkit.Stream{
			In:      io.NopCloser(bytes.NewReader([]byte(content))),
			IsPiped: true,
		}
		id, err := tp.Create(fx.Context(), tapper.CreateOptions{
			Title:  title,
			Stream: stream,
		})
		require.NoError(t, err)
		return id.String()
	}
	idX := createVia(tapB, "X")
	idY := createVia(tapB, "Y")
	idZ := createVia(tapB, "Z")

	// Tap A lists again. With the pre-fix code (k.Dex cache-only), this
	// second list returns the stale snapshot from before Tap B's writes
	// and the three new nodes are missing. With the fix (k.DexFresh),
	// Tap A detects the mtime change on dex/nodes.tsv and reloads.
	out, err := tapA.List(fx.Context(), tapper.ListOptions{})
	require.NoError(t, err)

	// Build an id-only set from the list output. The default list
	// format is tab-delimited with the node ID in the first column;
	// splitting on the first tab gives an exact ID without matching
	// substrings inside timestamps or titles.
	seen := make(map[string]struct{}, len(out))
	for _, line := range out {
		if idx := strings.IndexByte(line, '\t'); idx >= 0 {
			seen[line[:idx]] = struct{}{}
		} else {
			seen[line] = struct{}{}
		}
	}

	for _, want := range []string{idX, idY, idZ} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("Tap A second List should reflect node %q created by Tap B "+
				"(F1 / issue 327 regression); got output:\n%s",
				want, strings.Join(out, "\n"))
		}
	}
}
