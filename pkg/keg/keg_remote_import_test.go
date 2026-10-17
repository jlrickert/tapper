package keg_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jlrickert/tapper/internal/testapi"
	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// A Tapper Hub has no archive import route, so POST /archive answers 404 or
// 405. ImportNodes must report that as an unsupported operation that points
// at the hub's archive restore, not as a missing keg.
func TestRemoteImportNodesUnsupportedOnHub(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			f := NewSandbox(t)
			var posts atomic.Int64
			srv := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/archive" {
					posts.Add(1)
					_, _ = io.Copy(io.Discard, r.Body)
				}
				http.Error(w, "404 page not found", status)
			}))
			t.Cleanup(srv.Close)

			rk := kegpkg.NewRemoteKeg(srv.URL, mockHubToken, f.Runtime())
			imported, err := rk.ImportNodes(f.Context(), strings.NewReader("archive"), kegpkg.ImportNodesOptions{})
			require.Error(t, err)
			require.Nil(t, imported)
			require.Equal(t, int64(1), posts.Load())
			require.True(t, errors.Is(err, kegpkg.ErrNotSupported), "err: %v", err)
			require.False(t, errors.Is(err, kegpkg.ErrNotExist), "err: %v", err)
			require.Contains(t, err.Error(), srv.URL+"/archive/restore")
		})
	}
}
