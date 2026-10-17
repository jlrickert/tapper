package tapper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// rejectingTelemetryHub answers every telemetry post with status, the way a
// hub that will never accept this client's events does, and counts them.
func rejectingTelemetryHub(t *testing.T, status *atomic.Int32) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/version" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"server_version":"test","api_versions":["2026-10-12"]}`))
			return
		}
		posts.Add(1)
		w.WriteHeader(int(status.Load()))
	}))
	t.Cleanup(srv.Close)
	return srv, &posts
}

func runVersionedReporter(t *testing.T, r InvocationReporter, command string) {
	t.Helper()
	if r == nil {
		return
	}
	r.Report(InvocationEvent{Surface: "cli", Command: command})
	time.Sleep(1200 * time.Millisecond) // one flush interval
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r.Close(ctx)
}

// A hub that refused this client's events once is not asked again by every
// later process of the same version for a day.
func TestInvocationTelemetryRejectionIsRememberedPerVersion(t *testing.T) {
	fx, tap := newTelemetryAccountFixture(t)
	var status atomic.Int32
	status.Store(http.StatusNotFound)
	hub, posts := rejectingTelemetryHub(t, &status)
	signInForTelemetry(t, fx, tap, hub.URL, "refresh")

	runVersionedReporter(t, NewInvocationReporter(fx.Runtime(), tap.ConfigService, "v1.0.0"), "tap first")
	require.EqualValues(t, 1, posts.Load())

	again := NewInvocationReporter(fx.Runtime(), tap.ConfigService, "v1.0.0")
	runVersionedReporter(t, again, "tap second")
	require.Nil(t, again, "the same version skips telemetry after a rejection")
	require.EqualValues(t, 1, posts.Load(), "no request is sent to a hub that rejected this version")
}

// The rejection expires after a day, and never applies to another version:
// an upgraded client, or the hub after it upgrades, gets to try again.
func TestInvocationTelemetryRejectionExpiresAndIsVersionScoped(t *testing.T) {
	fx, tap := newTelemetryAccountFixture(t)
	var status atomic.Int32
	status.Store(http.StatusBadRequest)
	hub, posts := rejectingTelemetryHub(t, &status)
	signInForTelemetry(t, fx, tap, hub.URL, "refresh")

	runVersionedReporter(t, NewInvocationReporter(fx.Runtime(), tap.ConfigService, "v1.0.0"), "tap first")
	require.EqualValues(t, 1, posts.Load())

	status.Store(http.StatusNoContent)
	runVersionedReporter(t, NewInvocationReporter(fx.Runtime(), tap.ConfigService, "v1.1.0"), "tap upgraded")
	require.EqualValues(t, 2, posts.Load(), "another version is not covered by the rejection")

	fx.Advance(25 * time.Hour)
	runVersionedReporter(t, NewInvocationReporter(fx.Runtime(), tap.ConfigService, "v1.0.0"), "tap next-day")
	require.EqualValues(t, 3, posts.Load(), "the rejection lapses after a day")
}

// A retryable failure is not a rejection: the next process tries again.
func TestInvocationTelemetryTransientFailureIsNotRemembered(t *testing.T) {
	fx, tap := newTelemetryAccountFixture(t)
	var status atomic.Int32
	status.Store(http.StatusServiceUnavailable)
	hub, posts := rejectingTelemetryHub(t, &status)
	signInForTelemetry(t, fx, tap, hub.URL, "refresh")

	runVersionedReporter(t, NewInvocationReporter(fx.Runtime(), tap.ConfigService, "v1.0.0"), "tap first")
	first := posts.Load()
	require.Positive(t, first)
	r := NewInvocationReporter(fx.Runtime(), tap.ConfigService, "v1.0.0")
	require.NotNil(t, r)
	runVersionedReporter(t, r, "tap second")
	require.Greater(t, posts.Load(), first)
}

// A refused credential is not a refused client: an expired or revoked token
// stops this process's reporter, but the next process, holding a refreshed
// credential for the same login and version, reports again.
func TestInvocationTelemetryCredentialFailureIsNotRemembered(t *testing.T) {
	for _, code := range []int32{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(http.StatusText(int(code)), func(t *testing.T) {
			fx, tap := newTelemetryAccountFixture(t)
			var status atomic.Int32
			status.Store(code)
			hub, posts := rejectingTelemetryHub(t, &status)
			signInForTelemetry(t, fx, tap, hub.URL, "refresh")

			first := NewInvocationReporter(fx.Runtime(), tap.ConfigService, "v1.0.0")
			require.NotNil(t, first)
			first.Report(InvocationEvent{Surface: "cli", Command: "tap first"})
			runVersionedReporter(t, first, "tap first-again")
			require.EqualValues(t, 1, posts.Load(), "a refused credential is not retried within the process")

			status.Store(http.StatusNoContent)
			again := NewInvocationReporter(fx.Runtime(), tap.ConfigService, "v1.0.0")
			require.NotNil(t, again, "a credential failure must not suppress the next process")
			runVersionedReporter(t, again, "tap second")
			require.EqualValues(t, 2, posts.Load(), "the next process sends with its credential")
		})
	}
}

// A client-version refusal is still remembered for the same login and
// version, alongside the credential case above.
func TestInvocationTelemetryBadRequestIsStillRemembered(t *testing.T) {
	fx, tap := newTelemetryAccountFixture(t)
	var status atomic.Int32
	status.Store(http.StatusBadRequest)
	hub, posts := rejectingTelemetryHub(t, &status)
	signInForTelemetry(t, fx, tap, hub.URL, "refresh")

	runVersionedReporter(t, NewInvocationReporter(fx.Runtime(), tap.ConfigService, "v1.0.0"), "tap first")
	require.EqualValues(t, 1, posts.Load())

	status.Store(http.StatusNoContent)
	again := NewInvocationReporter(fx.Runtime(), tap.ConfigService, "v1.0.0")
	runVersionedReporter(t, again, "tap second")
	require.Nil(t, again, "a 400 still suppresses the same version")
	require.EqualValues(t, 1, posts.Load())
}
