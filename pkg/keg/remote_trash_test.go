package keg_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// A remote keg answers TrashInfo from the hub's trash listing: the entry for
// a trashed id, ErrNotExist for any other.
func TestRemoteKegTrashInfo(t *testing.T) {
	t.Parallel()
	srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/trash" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"trash":[{"id":7,"title":"Gone","reason":"merge","merged_into":3,"trashed_at":"2026-01-02T03:04:05Z","size":12}]}`))
	}))
	t.Cleanup(srv.Close)
	rk := keg.NewRemoteKeg(srv.URL, "", nil)
	ctx := context.Background()

	info, err := rk.TrashInfo(ctx, keg.NodeId{ID: 7})
	require.NoError(t, err)
	require.Equal(t, "Gone", info.Title)
	require.Equal(t, keg.TrashReasonMerge, info.Reason)
	require.Equal(t, &keg.NodeId{ID: 3}, info.MergedInto)

	_, err = rk.TrashInfo(ctx, keg.NodeId{ID: 8})
	require.ErrorIs(t, err, keg.ErrNotExist)
}
