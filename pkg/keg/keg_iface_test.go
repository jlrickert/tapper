package keg_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// newLiftedKeg returns an initialized memory-backed keg with two linked,
// tagged nodes for exercising the lifted Keg interface operations.
func newLiftedKeg(t *testing.T) (*sandbox.Sandbox, *kegpkg.LocalKeg) {
	t.Helper()
	f := NewSandbox(t)
	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	_, err := k.Create(f.Context(), &kegpkg.CreateOptions{
		Title: "Alpha node",
		Body:  []byte("# Alpha node\n\nAlpha body links to [beta](../2)"),
		Tags:  []string{"alpha", "shared"},
	})
	require.NoError(t, err)
	_, err = k.Create(f.Context(), &kegpkg.CreateOptions{
		Title: "Beta node",
		Body:  []byte("# Beta node\n\nBeta body mentions gamma rays"),
		Tags:  []string{"beta", "shared"},
	})
	require.NoError(t, err)
	return f, k
}

func TestReadNodeAssemblesFullState(t *testing.T) {
	t.Parallel()
	f, k := newLiftedKeg(t)

	view, err := k.ReadNode(f.Context(), kegpkg.NodeId{ID: 1})
	require.NoError(t, err)
	require.Equal(t, kegpkg.NodeId{ID: 1}, view.ID)
	require.Contains(t, string(view.Content), "Alpha body")
	require.NotNil(t, view.Stats)
	// MemoryRepo supports files/images, so the lists must be non-nil.
	require.NotNil(t, view.Files)
	require.NotNil(t, view.Images)
}

func TestNodeExistsContentAware(t *testing.T) {
	t.Parallel()
	f, k := newLiftedKeg(t)

	exists, err := k.NodeExists(f.Context(), kegpkg.NodeId{ID: 1})
	require.NoError(t, err)
	require.True(t, exists)

	exists, err = k.NodeExists(f.Context(), kegpkg.NodeId{ID: 4242})
	require.NoError(t, err)
	require.False(t, exists)
}

func TestQueryByTagAndStatsField(t *testing.T) {
	t.Parallel()
	f, k := newLiftedKeg(t)

	entries, err := k.Query(f.Context(), kegpkg.QueryOptions{Expr: "alpha"})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "Alpha node", entries[0].Title)

	entries, err = k.Query(f.Context(), kegpkg.QueryOptions{Expr: "shared"})
	require.NoError(t, err)
	require.Len(t, entries, 2)

	entries, err = k.Query(f.Context(), kegpkg.QueryOptions{Expr: "shared and not beta"})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "Alpha node", entries[0].Title)

	_, err = k.Query(f.Context(), kegpkg.QueryOptions{Expr: "shared and ("})
	require.Error(t, err)
}

func TestGrepMatchesContentLines(t *testing.T) {
	t.Parallel()
	f, k := newLiftedKeg(t)

	matches, err := k.Grep(f.Context(), kegpkg.GrepOptions{Pattern: "GAMMA", IgnoreCase: true})
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, "Beta node", matches[0].Entry.Title)
	require.NotEmpty(t, matches[0].Lines)

	matches, err = k.Grep(f.Context(), kegpkg.GrepOptions{Pattern: "no-such-text"})
	require.NoError(t, err)
	require.Empty(t, matches)
}

func TestSummaryCountsNodesAndAssets(t *testing.T) {
	t.Parallel()
	f, k := newLiftedKeg(t)

	require.NoError(t, k.WriteFile(f.Context(), kegpkg.NodeId{ID: 1}, "doc.txt", []byte("hi")))

	sum, err := k.Summary(f.Context())
	require.NoError(t, err)
	require.Equal(t, 3, sum.NodeCount) // zero node + 2 created
	require.True(t, sum.Files.Supported)
	require.Equal(t, 1, sum.Files.NodesWithAssets)
	require.Equal(t, 1, sum.Files.TotalAssets)
}

func TestExportImportRoundTrip(t *testing.T) {
	t.Parallel()
	f, k := newLiftedKeg(t)
	require.NoError(t, k.WriteFile(f.Context(), kegpkg.NodeId{ID: 1}, "doc.txt", []byte("payload")))

	rc, err := k.ExportNodes(f.Context(), kegpkg.ExportNodesOptions{WithAssets: true})
	require.NoError(t, err)
	archive, err := io.ReadAll(rc)
	require.NoError(t, err)
	require.NoError(t, rc.Close())
	require.NotEmpty(t, archive)

	// Import into a fresh keg.
	repo2 := kegpkg.NewMemoryRepo(f.Runtime())
	k2 := kegpkg.NewLocalKeg(repo2, f.Runtime())
	require.NoError(t, k2.Init(f.Context()))

	imported, err := k2.ImportNodes(f.Context(), bytes.NewReader(archive), kegpkg.ImportNodesOptions{})
	require.NoError(t, err)
	require.Len(t, imported, 3)

	view, err := k2.ReadNode(f.Context(), kegpkg.NodeId{ID: 1})
	require.NoError(t, err)
	require.Contains(t, string(view.Content), "Alpha body")
	require.Contains(t, view.Files, "doc.txt")

	// The dex must have been rebuilt to include imported nodes.
	dex, err := k2.Dex(f.Context())
	require.NoError(t, err)
	require.Len(t, dex.Nodes(f.Context()), 3)
}

func TestLockUnlockRoundTrip(t *testing.T) {
	t.Parallel()
	f, k := newLiftedKeg(t)
	id := kegpkg.NodeId{ID: 1}

	token, err := k.Lock(f.Context(), id)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	info, err := k.LockStatus(f.Context(), id)
	require.NoError(t, err)
	require.Equal(t, token, info.Token)

	require.NoError(t, k.Unlock(f.Context(), id, token))

	info, err = k.LockStatus(f.Context(), id)
	require.NoError(t, err)
	require.Empty(t, info.Token)
}

func TestMoveReturnsRewrittenNodes(t *testing.T) {
	t.Parallel()
	f, k := newLiftedKeg(t)

	// Node 1 links to ../2; moving 2 -> 5 must rewrite node 1.
	rewritten, err := k.Move(f.Context(), kegpkg.NodeId{ID: 2}, kegpkg.NodeId{ID: 5})
	require.NoError(t, err)
	require.Contains(t, rewritten, kegpkg.NodeId{ID: 1})

	content, err := k.GetContent(f.Context(), kegpkg.NodeId{ID: 1})
	require.NoError(t, err)
	require.Contains(t, string(content), "../5")
}
