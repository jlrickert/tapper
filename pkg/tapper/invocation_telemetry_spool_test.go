package tapper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/apicontract"
	"github.com/stretchr/testify/require"
)

// fakeTelemetryHub records the events a Hub received. While failing is set it
// answers 503, so a reporter keeps its events for the spool.
type fakeTelemetryHub struct {
	*httptest.Server
	failing atomic.Bool
	mu      sync.Mutex
	got     []string
}

func newFakeTelemetryHub(t *testing.T) *fakeTelemetryHub {
	t.Helper()
	h := &fakeTelemetryHub{}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/version" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"server_version":"test","api_versions":["`+apicontract.Revision+`"]}`)
			return
		}
		if h.failing.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		var batch []InvocationEvent
		_ = json.NewDecoder(req.Body).Decode(&batch)
		h.mu.Lock()
		for _, e := range batch {
			h.got = append(h.got, e.Command)
		}
		h.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(h.Close)
	return h
}

func (h *fakeTelemetryHub) commands() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.got...)
}

// signInForTelemetry points the user config at hubURL and stores a login
// whose refresh token is refresh. It writes the entry as an earlier release
// did, with no AccountKey, unless the same login is already stored.
func signInForTelemetry(t *testing.T, fx *sandbox.Sandbox, tap *Tap, hubURL, refresh string) {
	t.Helper()
	cfg := fmt.Sprintf("hub: primary\nhubs:\n  primary:\n    url: %s\n", hubURL)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(cfg), 0o644))
	store, err := LoadAuthStore(fx.Context(), fx.Runtime(), tap.PathService.AuthStorePath())
	require.NoError(t, err)
	entry := AuthEntry{AccessToken: "access-" + refresh, RefreshToken: refresh}
	if prev, ok := store.Get(hubURL); ok && prev.RefreshToken == refresh {
		// Signing in again with the same credential is the same login.
		entry.AccountKey = prev.AccountKey
	}
	store.Set(hubURL, entry)
	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), tap.PathService.AuthStorePath()))
	tap.ConfigService.Reload()
}

// runTelemetryReporter starts a reporter for the current login, reports
// commands, and closes it.
func runTelemetryReporter(t *testing.T, fx *sandbox.Sandbox, tap *Tap, commands ...string) {
	t.Helper()
	r := NewInvocationReporter(fx.Runtime(), tap.ConfigService, "test")
	require.NotNil(t, r)
	for _, c := range commands {
		r.Report(InvocationEvent{Surface: "cli", Command: c})
	}
	// Let a startup resend finish before Close, which never starts a send.
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	r.Close(ctx)
}

// Spooled events belong to the hub and account that produced them: another
// hub never receives them, and the original hub still does later.
func TestInvocationSpoolIsNeverSentToAnotherHub(t *testing.T) {
	fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := NewTap(TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	hubA, hubB := newFakeTelemetryHub(t), newFakeTelemetryHub(t)

	hubA.failing.Store(true)
	signInForTelemetry(t, fx, tap, hubA.URL, "refresh-a")
	runTelemetryReporter(t, fx, tap, "tap on-a")
	require.Empty(t, hubA.commands())

	signInForTelemetry(t, fx, tap, hubB.URL, "refresh-b")
	runTelemetryReporter(t, fx, tap)
	require.Empty(t, hubB.commands(), "hub B must not receive hub A's events")

	hubA.failing.Store(false)
	signInForTelemetry(t, fx, tap, hubA.URL, "refresh-a")
	r := NewInvocationReporter(fx.Runtime(), tap.ConfigService, "test")
	require.NotNil(t, r)
	require.Eventually(t, func() bool { return len(hubA.commands()) == 1 }, 2*time.Second, 5*time.Millisecond)
	r.Close(context.Background())
	require.Equal(t, []string{"tap on-a"}, hubA.commands())
	require.Empty(t, hubB.commands())
}

// A new login on the same hub is another identity: the previous login's
// spool is not sent with the new credential.
func TestInvocationSpoolIsNotSentUnderAnotherLogin(t *testing.T) {
	fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := NewTap(TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	hub := newFakeTelemetryHub(t)

	hub.failing.Store(true)
	signInForTelemetry(t, fx, tap, hub.URL, "refresh-one")
	runTelemetryReporter(t, fx, tap, "tap first-login")

	hub.failing.Store(false)
	signInForTelemetry(t, fx, tap, hub.URL, "refresh-two")
	runTelemetryReporter(t, fx, tap, "tap second-login")
	time.Sleep(100 * time.Millisecond)
	require.NotContains(t, hub.commands(), "tap first-login")
}

// The single shared spool file of earlier releases names no destination, so
// it is deleted without being sent.
func TestInvocationLegacySpoolIsDiscarded(t *testing.T) {
	fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := NewTap(TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	hub := newFakeTelemetryHub(t)
	signInForTelemetry(t, fx, tap, hub.URL, "refresh")
	legacy := invocationTelemetryLegacySpoolPath(tap.ConfigService)
	raw, err := json.Marshal([]InvocationEvent{{Surface: "cli", Command: "tap legacy"}})
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().AtomicWriteFile(legacy, raw, 0o600))

	runTelemetryReporter(t, fx, tap)
	time.Sleep(100 * time.Millisecond)
	require.Empty(t, hub.commands())
	_, err = fx.Runtime().Stat(legacy, false)
	require.Error(t, err, "the legacy spool is removed")
}

func recordingTelemetryClient(status int, mu *sync.Mutex, got *[]string) *http.Client {
	return &http.Client{Transport: telemetryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if status >= 300 {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		}
		var batch []InvocationEvent
		_ = json.NewDecoder(req.Body).Decode(&batch)
		mu.Lock()
		for _, e := range batch {
			*got = append(*got, e.Command)
		}
		mu.Unlock()
		return noContent()
	})}
}

const testTelemetryEndpoint = "https://hub.example.com/api/v1/telemetry/invocations"

// Processes that close at the same time each spool their own events, and the
// next process sends every one of them exactly once.
func TestInvocationSpoolConcurrentClosesLoseNothing(t *testing.T) {
	fx, spool := spoolFixture(t)
	var want []string
	var wg sync.WaitGroup
	for p := range 4 {
		r := newHTTPInvocationReporter(testTelemetryEndpoint, "token", "test", invocationReporterOptions{
			rt: fx.Runtime(), spoolDir: spool, client: recordingTelemetryClient(http.StatusServiceUnavailable, nil, nil),
			flushInterval: time.Hour,
		})
		for i := range 5 {
			c := fmt.Sprintf("p%d-%d", p, i)
			want = append(want, c)
			r.Report(InvocationEvent{Surface: "cli", Command: c})
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			r.Close(ctx)
		}()
	}
	wg.Wait()

	var mu sync.Mutex
	var got []string
	r := newHTTPInvocationReporter(testTelemetryEndpoint, "token", "test", invocationReporterOptions{
		rt: fx.Runtime(), spoolDir: spool, client: recordingTelemetryClient(http.StatusNoContent, &mu, &got),
		flushInterval: time.Hour,
	})
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) >= len(want) }, 2*time.Second, 5*time.Millisecond)
	r.Close(context.Background())
	mu.Lock()
	defer mu.Unlock()
	sort.Strings(got)
	sort.Strings(want)
	require.Equal(t, want, got)
	require.Empty(t, spoolFilesForTest(t, fx, spool))
}

// Processes that start at the same time split the spool between them: each
// spooled event is sent exactly once.
func TestInvocationSpoolConcurrentStartsSendEachEventOnce(t *testing.T) {
	fx, spool := spoolFixture(t)
	var want []string
	for f := range 20 {
		var batch []InvocationEvent
		for i := range 3 {
			c := fmt.Sprintf("f%d-%d", f, i)
			want = append(want, c)
			batch = append(batch, InvocationEvent{Surface: "cli", Command: c})
		}
		writeSpoolForTest(t, fx, spool, batch)
	}

	var mu sync.Mutex
	var got []string
	reporters := make([]*httpInvocationReporter, 4)
	var wg sync.WaitGroup
	for i := range reporters {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reporters[i] = newHTTPInvocationReporter(testTelemetryEndpoint, "token", "test", invocationReporterOptions{
				rt: fx.Runtime(), spoolDir: spool, client: recordingTelemetryClient(http.StatusNoContent, &mu, &got),
				flushInterval: time.Hour,
			})
		}()
	}
	wg.Wait()
	require.Eventually(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(got) >= len(want) }, 2*time.Second, 5*time.Millisecond)
	for _, r := range reporters {
		r.Close(context.Background())
	}
	mu.Lock()
	defer mu.Unlock()
	sort.Strings(got)
	sort.Strings(want)
	require.Equal(t, want, got)
	require.Empty(t, spoolFilesForTest(t, fx, spool))
}
