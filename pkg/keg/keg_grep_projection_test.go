package keg_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func seedGrepKeg(t *testing.T, repo keg.Repository) (keg.Keg, context.Context) {
	t.Helper()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(repo, fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	for _, body := range []string{
		"# Alpha\n\nneedle one\n",
		"# Beta\n\nnothing here\n",
		"# Gamma\n\nNEEDLE two\n",
		"# Delta\n\nneedle three\n",
	} {
		_, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(body), Meta: []byte("kind: note\n")})
		require.NoError(t, err)
	}
	require.NoError(t, k.Index(ctx, keg.IndexOptions{}))
	return k, ctx
}

func grepIDs(matches []keg.GrepMatch) []string {
	ids := make([]string, len(matches))
	for i, m := range matches {
		ids[i] = m.Entry.ID
	}
	return ids
}

func TestGrepPagesAndProjectsFields(t *testing.T) {
	fx := NewSandbox(t)
	k, ctx := seedGrepKeg(t, newTestMemoryRepo(fx.Runtime()))

	all, err := k.Grep(ctx, keg.GrepOptions{Pattern: "needle", IgnoreCase: true})
	require.NoError(t, err)
	require.Equal(t, []string{"1", "3", "4"}, grepIDs(all))

	page, err := k.Grep(ctx, keg.GrepOptions{Pattern: "needle", IgnoreCase: true, Offset: 1, Limit: 1, Fields: []string{"kind"}})
	require.NoError(t, err)
	require.Equal(t, []string{"3"}, grepIDs(page))
	require.Equal(t, map[string]string{"kind": "note"}, page[0].Fields)

	_, err = k.Grep(ctx, keg.GrepOptions{Pattern: "needle", Offset: -1})
	require.ErrorIs(t, err, keg.ErrInvalid)
}

func TestListViewRestrictsToNodeIDs(t *testing.T) {
	fx := NewSandbox(t)
	k, ctx := seedGrepKeg(t, newTestMemoryRepo(fx.Runtime()))
	view, err := k.ListView(ctx, keg.ListViewOptions{
		NodeIDs: []keg.NodeId{{ID: 4}, {ID: 2}},
		Fields:  []string{"kind"},
	})
	require.NoError(t, err)
	require.Len(t, view.Rows, 2)
	require.Equal(t, "2", view.Rows[0].Entry.ID)
	require.Equal(t, "4", view.Rows[1].Entry.ID)
	require.Equal(t, "note", view.Rows[1].Fields["kind"])
	require.Equal(t, 2, view.TotalMatches)
}

// scanRepo implements the optional content scan the way an index-backed
// store would: a literal narrows the scan to nodes containing it.
type scanRepo struct {
	keg.Repository
	scans    int
	literals []string
}

func (r *scanRepo) ScanContent(ctx context.Context, literal string, ignoreCase bool) (map[string][]byte, error) {
	r.scans++
	r.literals = append(r.literals, literal)
	ids, err := r.ListNodes(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, id := range ids {
		raw, err := r.ReadContent(ctx, id)
		if err != nil {
			continue
		}
		haystack, needle := string(raw), literal
		if ignoreCase {
			haystack, needle = strings.ToLower(haystack), strings.ToLower(needle)
		}
		if strings.Contains(haystack, needle) {
			out[id.Path()] = raw
		}
	}
	return out, nil
}

func TestGrepWithContentScanMatchesPlainGrep(t *testing.T) {
	fx := NewSandbox(t)
	plain, ctx := seedGrepKeg(t, newTestMemoryRepo(fx.Runtime()))
	repo := &scanRepo{Repository: newTestMemoryRepo(fx.Runtime())}
	scanned, _ := seedGrepKeg(t, repo)

	for _, opts := range []keg.GrepOptions{
		{Pattern: "needle"},
		{Pattern: "needle", IgnoreCase: true},
		{Pattern: `\bneedle\s+t`},
		{Pattern: "(?i)needle"},
	} {
		want, err := plain.Grep(ctx, opts)
		require.NoError(t, err)
		got, err := scanned.Grep(ctx, opts)
		require.NoError(t, err)
		require.Equal(t, grepIDs(want), grepIDs(got), "pattern %q", opts.Pattern)
	}
	// Literal patterns narrow the scan (case folded under (?i)); anything
	// else scans every node.
	for i := range repo.literals {
		repo.literals[i] = strings.ToLower(repo.literals[i])
	}
	require.Equal(t, []string{"needle", "needle", "", "needle"}, repo.literals)
}
