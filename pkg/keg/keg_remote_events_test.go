package keg_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
)

func TestRemoteKegWatchHandshakeUsesStructuredErrorCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		code      string
		wantRetry bool
	}{
		{name: "orientation stale stops", code: keg.RemoteCodeOrientationStale},
		{name: "ordinary conflict retries", code: keg.RemoteCodeConflict, wantRetry: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusConflict)
				_, _ = fmt.Fprintf(w, `{"error":"handshake refused","code":%q}`, tc.code)
			}))
			defer srv.Close()

			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			remote := keg.NewRemoteKeg(srv.URL+"/api/v1/@team/kegs/notes", "token", nil)
			events, err := remote.Watch(ctx, keg.NodeId{ID: 7})
			if err != nil {
				t.Fatalf("Watch: %v", err)
			}

			if tc.wantRetry {
				deadline := time.Now().Add(1500 * time.Millisecond)
				for requests.Load() < 2 && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if requests.Load() < 2 {
					t.Fatalf("requests = %d, want retry", requests.Load())
				}
				cancel()
			}

			select {
			case _, ok := <-events:
				if ok {
					t.Fatal("unexpected watch event")
				}
			case <-time.After(time.Second):
				t.Fatal("watch did not terminate")
			}
			if !tc.wantRetry && requests.Load() != 1 {
				t.Fatalf("requests = %d, want exactly one", requests.Load())
			}
		})
	}
}
