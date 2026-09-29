package keg_test

import (
	"context"
	"fmt"
	"regexp"
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

// grepRepo greps content itself, as a database backend would. Its matcher is
// Go's regexp, so results can be compared with the per-node path; patterns
// it cannot compile are reported as invalid, as the backend contract says.
type grepRepo struct {
	keg.Repository
	patterns []string
}

func (r *grepRepo) GrepContent(ctx context.Context, opts keg.GrepContentOptions, fn func(keg.NodeId, []string) error) error {
	r.patterns = append(r.patterns, opts.Pattern)
	pattern := opts.Pattern
	if opts.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fmt.Errorf("backend rejects %q: %w", opts.Pattern, keg.ErrInvalid)
	}
	ids, err := r.ListNodes(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		raw, err := r.ReadContent(ctx, id)
		if err != nil {
			continue
		}
		var lines []string
		for i, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
			line = strings.TrimRight(line, "\r")
			if re.MatchString(line) {
				lines = append(lines, fmt.Sprintf("%d:%s", i+1, line))
			}
		}
		if opts.MaxLines > 0 && len(lines) > opts.MaxLines {
			lines = lines[:opts.MaxLines]
		}
		if len(lines) > 0 {
			if err := fn(id, lines); err != nil {
				return err
			}
		}
	}
	return nil
}

func TestGrepThroughBackendMatchesPlainGrep(t *testing.T) {
	fx := NewSandbox(t)
	plain, ctx := seedGrepKeg(t, newTestMemoryRepo(fx.Runtime()))
	repo := &grepRepo{Repository: newTestMemoryRepo(fx.Runtime())}
	backed, _ := seedGrepKeg(t, repo)

	for _, opts := range []keg.GrepOptions{
		{Pattern: "needle"},
		{Pattern: "needle", IgnoreCase: true},
		{Pattern: `\bneedle\s+t`},
		{Pattern: "(?i)NEEDLE two"},
		{Pattern: "nothing|needle"},
		{Pattern: "needle", MaxLines: 1},
		{Pattern: "needle", IgnoreCase: true, Offset: 1, Limit: 1, Fields: []string{"kind"}},
	} {
		want, err := plain.Grep(ctx, opts)
		require.NoError(t, err)
		got, err := backed.Grep(ctx, opts)
		require.NoError(t, err)
		require.Equal(t, want, got, "pattern %q", opts.Pattern)
	}
}

// The backend owns its regex dialect: a pattern Go cannot compile still
// reaches it, and its refusal is reported as an invalid pattern.
func TestGrepLeavesPatternValidationToTheBackend(t *testing.T) {
	fx := NewSandbox(t)
	repo := &grepRepo{Repository: newTestMemoryRepo(fx.Runtime())}
	k, ctx := seedGrepKeg(t, repo)
	_, err := k.Grep(ctx, keg.GrepOptions{Pattern: "needle(?= one)"})
	require.ErrorIs(t, err, keg.ErrInvalid)
	require.Equal(t, []string{"needle(?= one)"}, repo.patterns)
}
