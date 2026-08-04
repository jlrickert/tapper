package tapper

import (
	"context"
	"errors"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestResolveImportSourceAlias_BareIDs(t *testing.T) {
	t.Parallel()
	alias, bareIDs, err := resolveImportSourceAlias([]string{"1", "2", "3"}, "mykeg")
	require.NoError(t, err)
	require.Equal(t, "mykeg", alias)
	require.Equal(t, []string{"1", "2", "3"}, bareIDs)
}

func TestImportFromKeg_LeaveStubsRequiresEditorOnSource(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		leaveStubs bool
		want       FlightRole
	}{
		{name: "copy only", want: FlightRoleViewer},
		{name: "leave stubs", leaveStubs: true, want: FlightRoleEditor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got FlightRole
			tap := &Tap{KegResolver: func(context.Context, KegTargetOptions, FlightRole) (keg.Keg, error) {
				return nil, errors.New("unexpected resolver path")
			}}
			tap.KegResolver = func(_ context.Context, _ KegTargetOptions, role FlightRole) (keg.Keg, error) {
				got = role
				return nil, errors.New("stop after role capture")
			}
			_, err := tap.ImportFromKeg(context.Background(), ImportFromKegOptions{
				Source: KegTargetOptions{Keg: "source"}, LeaveStubs: tc.leaveStubs,
			})
			require.Error(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResolveImportSourceAlias_KegRefArgs(t *testing.T) {
	t.Parallel()
	alias, bareIDs, err := resolveImportSourceAlias(
		[]string{"keg:pub/5", "keg:pub/7"}, "",
	)
	require.NoError(t, err)
	require.Equal(t, "pub", alias)
	require.Equal(t, []string{"5", "7"}, bareIDs)
}

func TestResolveImportSourceAlias_KegRefConflictsWithFrom(t *testing.T) {
	t.Parallel()
	_, _, err := resolveImportSourceAlias([]string{"keg:pub/1"}, "other")
	require.Error(t, err)
}

func TestResolveImportSourceAlias_ConflictingAliasesInArgs(t *testing.T) {
	t.Parallel()
	_, _, err := resolveImportSourceAlias(
		[]string{"keg:pub/1", "keg:priv/2"}, "",
	)
	require.Error(t, err)
}

func TestResolveImportSourceAlias_MixedBareAndKegRef(t *testing.T) {
	t.Parallel()
	// Mixing bare IDs with keg: refs is allowed; bare IDs are kept as-is.
	alias, bareIDs, err := resolveImportSourceAlias(
		[]string{"3", "keg:pub/5"}, "",
	)
	require.NoError(t, err)
	require.Equal(t, "pub", alias)
	require.Equal(t, []string{"3", "5"}, bareIDs)
}

func TestFilterZeroImportNode(t *testing.T) {
	t.Parallel()
	ids := []keg.NodeId{{ID: 0}, {ID: 1}, {ID: 2}, {ID: 0, Code: "draft"}}
	got := filterZeroImportNode(ids)
	// Node 0 without code is filtered; Node 0 with code is kept.
	require.Len(t, got, 3)
	require.Equal(t, 1, got[0].ID)
	require.Equal(t, 2, got[1].ID)
	require.Equal(t, 0, got[2].ID)
	require.Equal(t, "draft", got[2].Code)
}

func TestUnionImportNodeIDs_Deduplication(t *testing.T) {
	t.Parallel()
	a := []keg.NodeId{{ID: 1}, {ID: 2}}
	b := []keg.NodeId{{ID: 2}, {ID: 3}}
	got := unionImportNodeIDs(a, b)
	require.Len(t, got, 3)
}
