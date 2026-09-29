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
// store would: the filter's substrings narrow the scan, compared with a
// lowercase fold like SQL ILIKE rather than Go's case folding.
type scanRepo struct {
	keg.Repository
	delivered int
}

func (r *scanRepo) ScanContent(ctx context.Context, filter keg.ContentFilter, fn func(keg.NodeId, []byte) error) error {
	ids, err := r.ListNodes(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		raw, err := r.ReadContent(ctx, id)
		if err != nil {
			continue
		}
		haystack := string(raw)
		if filter.IgnoreCase {
			haystack = strings.ToLower(haystack)
		}
		keep := true
		for _, sub := range filter.Substrings {
			if filter.IgnoreCase {
				sub = strings.ToLower(sub)
			}
			keep = keep && strings.Contains(haystack, sub)
		}
		if !keep {
			continue
		}
		r.delivered++
		if err := fn(id, raw); err != nil {
			return err
		}
	}
	return nil
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
		{Pattern: "(?i)NEEDLE two"},
		{Pattern: `needle\s+(one|two)`},
		{Pattern: "nothing|needle"},
		{Pattern: "ne+dle"},
		{Pattern: "need(le)?"},
		{Pattern: `(?:needle ){1,2}three`},
		{Pattern: "needle", MaxLines: 1},
	} {
		want, err := plain.Grep(ctx, opts)
		require.NoError(t, err)
		got, err := scanned.Grep(ctx, opts)
		require.NoError(t, err)
		require.Equal(t, want, got, "pattern %q", opts.Pattern)
	}
}

func TestGrepContentScanNarrowsToRequiredText(t *testing.T) {
	fx := NewSandbox(t)
	repo := &scanRepo{Repository: newTestMemoryRepo(fx.Runtime())}
	k, ctx := seedGrepKeg(t, repo)
	_, err := k.Grep(ctx, keg.GrepOptions{Pattern: `needle\s+t`})
	require.NoError(t, err)
	// Only the nodes containing "needle" cross the scan, not all four.
	require.Equal(t, 2, repo.delivered)
}

func TestGrepContentScanKeepsGoCaseFolding(t *testing.T) {
	// Go's (?i)s also matches U+017F (long s), which a lowercase comparison
	// does not fold; the scan must not drop that node.
	fx := NewSandbox(t)
	for _, repo := range []keg.Repository{newTestMemoryRepo(fx.Runtime()), &scanRepo{Repository: newTestMemoryRepo(fx.Runtime())}} {
		k := keg.NewLocalKeg(repo, fx.Runtime())
		ctx := fx.Context()
		initNonStrictTestKeg(t, k, ctx)
		_, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Note\n\nthe \u017ftar\n")})
		require.NoError(t, err)
		require.NoError(t, k.Index(ctx, keg.IndexOptions{}))
		got, err := k.Grep(ctx, keg.GrepOptions{Pattern: "star", IgnoreCase: true})
		require.NoError(t, err)
		require.Len(t, got, 1)
	}
}
