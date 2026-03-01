package tapper

import (
	"context"
	"slices"
	"testing"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// makeQueryKeg creates an in-memory keg pre-populated with nodes for
// evalQueryExpr unit tests.
//
// Nodes:
//
//	0 - meta: {tags: [planned]}
//	1 - meta: {entity: trick, tags: [planned]}
//	2 - meta: {entity: concept}
//	3 - meta: {} (empty)
func makeQueryKeg(t *testing.T) (*keg.Keg, *keg.Dex) {
	t.Helper()
	ctx := context.Background()

	rt, err := toolkit.NewTestRuntime(t.TempDir(), "/home/testuser", "testuser")
	require.NoError(t, err)

	repo := keg.NewMemoryRepo(rt)

	nodes := []struct {
		id   int
		meta []byte
	}{
		{0, []byte("tags:\n  - planned\n")},
		{1, []byte("entity: trick\ntags:\n  - planned\n")},
		{2, []byte("entity: concept\n")},
		{3, nil},
	}

	for _, n := range nodes {
		id := keg.NodeId{ID: n.id}
		require.NoError(t, repo.WriteContent(ctx, id, []byte("# Node\n")))
		if len(n.meta) > 0 {
			require.NoError(t, repo.WriteMeta(ctx, id, n.meta))
		}
	}

	// Write a minimal tags index so the dex can resolve "planned".
	// Format: <tag>\t<space-separated node paths>
	tagsData := []byte("planned\t0 1\n")
	require.NoError(t, repo.WriteIndex(ctx, "tags", tagsData))

	// Write a minimal nodes.tsv so the dex knows about all nodes.
	nodesTSV := []byte("0\t2026-01-01T00:00:00Z\tNode 0\n1\t2026-01-01T00:00:00Z\tNode 1\n2\t2026-01-01T00:00:00Z\tNode 2\n3\t2026-01-01T00:00:00Z\tNode 3\n")
	require.NoError(t, repo.WriteIndex(ctx, "nodes.tsv", nodesTSV))

	k := keg.NewKeg(repo, rt)

	// Use NewDexFromRepo which does not require an initialized keg.
	d, err := keg.NewDexFromRepo(ctx, repo)
	require.NoError(t, err)

	return k, d
}

func TestEvalQueryExpr(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	k, d := makeQueryKeg(t)
	entries := d.Nodes(ctx)

	cases := []struct {
		name    string
		expr    string
		wantIDs []string
		wantErr bool
	}{
		{
			name:    "plain_tag",
			expr:    "planned",
			wantIDs: []string{"0", "1"},
		},
		{
			name:    "attr_predicate_entity_trick",
			expr:    "entity=trick",
			wantIDs: []string{"1"},
		},
		{
			name:    "attr_predicate_entity_concept",
			expr:    "entity=concept",
			wantIDs: []string{"2"},
		},
		{
			name:    "attr_and_tag",
			expr:    "entity=trick and planned",
			wantIDs: []string{"1"},
		},
		{
			name:    "attr_or_attr",
			expr:    "entity=trick or entity=concept",
			wantIDs: []string{"1", "2"},
		},
		{
			name:    "attr_not_matched",
			expr:    "entity=plan",
			wantIDs: []string{},
		},
		{
			name:    "not_attr",
			expr:    "not entity=trick",
			wantIDs: []string{"0", "2", "3"},
		},
		{
			name:    "attr_and_not_tag",
			expr:    "entity=concept and not planned",
			wantIDs: []string{"2"},
		},
		{
			name:    "missing_key",
			expr:    "nosuchkey=value",
			wantIDs: []string{},
		},
		{
			name:    "parse_error",
			expr:    "a and (b",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := evalQueryExpr(ctx, k, d, entries, tc.expr)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Deduplicate and collect numeric IDs.
			seen := make(map[string]struct{})
			ids := make([]string, 0, len(got))
			for p := range got {
				n, parseErr := keg.ParseNode(p)
				if parseErr != nil || n == nil {
					continue
				}
				numericID := n.Path()
				if _, ok := seen[numericID]; ok {
					continue
				}
				seen[numericID] = struct{}{}
				ids = append(ids, numericID)
			}
			slices.Sort(ids)

			want := append([]string{}, tc.wantIDs...)
			slices.Sort(want)
			require.Equal(t, want, ids)
		})
	}
}
