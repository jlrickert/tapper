package tapper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

type telemetryRoundTripFunc func(*http.Request) (*http.Response, error)

func (f telemetryRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == "/api/version" {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"server_version":"test","api_versions":["2026-10-12"]}`))}, nil
	}

	return f(req)
}

func telemetryTargetFixture(t *testing.T, userConfig string) (*sandbox.Sandbox, *Tap) {
	t.Helper()
	fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := NewTap(TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(userConfig), 0o644))
	store := &AuthStore{}
	store.Set("https://hub.example.com", AuthEntry{AccessToken: "stored-token"})
	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), tap.PathService.AuthStorePath()))
	return fx, tap
}

func TestResolveInvocationTelemetryTargetDefaultOnAndOptOut(t *testing.T) {
	base := "hub: primary\nhubs:\n  primary:\n    url: https://hub.example.com\n"

	t.Run("default on", func(t *testing.T) {
		fx, tap := telemetryTargetFixture(t, base)
		endpoint, token, _, ok := resolveInvocationTelemetryTarget(fx.Runtime(), tap.ConfigService)
		require.True(t, ok)
		require.Equal(t, "https://hub.example.com/api/v1/telemetry/invocations", endpoint)
		require.Equal(t, "stored-token", token)
	})

	t.Run("user config opt out", func(t *testing.T) {
		fx, tap := telemetryTargetFixture(t, "disableTelemetry: true\n"+base)
		_, _, _, ok := resolveInvocationTelemetryTarget(fx.Runtime(), tap.ConfigService)
		require.False(t, ok)
	})

	t.Run("environment opt out", func(t *testing.T) {
		fx, tap := telemetryTargetFixture(t, base)
		require.NoError(t, fx.Runtime().Env().Set("TAP_DISABLE_TELEMETRY", "1"))
		_, _, _, ok := resolveInvocationTelemetryTarget(fx.Runtime(), tap.ConfigService)
		require.False(t, ok)
	})

	t.Run("invalid selected project hub disables telemetry", func(t *testing.T) {
		fx, tap := telemetryTargetFixture(t, base)
		projectConfig := "/home/testuser/.tapper/config.yaml"
		require.NoError(t, fx.Runtime().Mkdir("/home/testuser/.tapper", 0o755, true))
		require.NoError(t, fx.Runtime().AtomicWriteFile(projectConfig, []byte("hub: attacker\n"), 0o644))
		endpoint, _, _, ok := resolveInvocationTelemetryTarget(fx.Runtime(), tap.ConfigService)
		require.False(t, ok)
		require.Empty(t, endpoint)
	})
}

func TestResolveInvocationTelemetryTargetSilentlySkipsUnavailableState(t *testing.T) {
	fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := NewTap(TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	_, _, _, ok := resolveInvocationTelemetryTarget(fx.Runtime(), tap.ConfigService)
	require.False(t, ok, "unbootstrapped client must skip")

	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte("hub: local\nhubs:\n  local: {}\n"), 0o644))
	tap.ConfigService.Reload()
	_, _, _, ok = resolveInvocationTelemetryTarget(fx.Runtime(), tap.ConfigService)
	require.False(t, ok, "client whose selected hub has no URL must skip")

	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte("hub: remote\nhubs:\n  remote:\n    url: https://hub.example.com\n"), 0o644))
	tap.ConfigService.Reload()
	_, _, _, ok = resolveInvocationTelemetryTarget(fx.Runtime(), tap.ConfigService)
	require.False(t, ok, "unauthenticated client must skip")
}

func TestHTTPInvocationReporterPayloadIsMinimalAndBatched(t *testing.T) {
	var (
		mu       sync.Mutex
		requests [][]map[string]any
	)
	client := &http.Client{Transport: telemetryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "Bearer stored-token", req.Header.Get("Authorization"))
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))
		var batch []map[string]any
		require.NoError(t, json.NewDecoder(req.Body).Decode(&batch))
		mu.Lock()
		requests = append(requests, batch)
		mu.Unlock()
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	r := newHTTPInvocationReporter("https://hub.example.com/api/v1/telemetry/invocations", "stored-token", "v0.test", invocationReporterOptions{
		client: client, batchSize: 2, flushInterval: time.Hour,
	})
	interactive := true
	r.Report(InvocationEvent{Surface: "cli", Command: "tap keg list", DurationMS: 10, Success: true, Interactive: &interactive})
	r.Report(InvocationEvent{Surface: "mcp", Tool: "keg_list", DurationMS: 20, Success: false})
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(requests) > 0
	}, time.Second, time.Millisecond, "a full batch is sent without waiting for Close")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r.Close(ctx)

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, requests, 1)
	require.Len(t, requests[0], 2)
	require.Equal(t, "v0.test", requests[0][0]["client_version"])
	require.Equal(t, "tap keg list", requests[0][0]["command"])
	require.Equal(t, "keg_list", requests[0][1]["tool"])
	for _, event := range requests[0] {
		for _, forbidden := range []string{"args", "error", "path", "keg", "node_id", "content", "credentials", "session_id"} {
			_, present := event[forbidden]
			require.Falsef(t, present, "payload contains forbidden field %q: %#v", forbidden, event)
		}
	}
}

func TestHTTPInvocationReporterQueuePressureNeverBlocks(t *testing.T) {
	r := &httpInvocationReporter{
		version: "test",
		queue:   make(chan InvocationEvent, 2),
	}
	for i := 0; i < 100; i++ {
		r.Report(InvocationEvent{Surface: "mcp", Tool: "list"})
	}
	require.Len(t, r.queue, 2)
	require.Equal(t, uint64(98), r.dropped.Load())
}

func TestHTTPInvocationReporterTimeoutAndOlderHubAreBestEffort(t *testing.T) {
	t.Run("timeout drops batch", func(t *testing.T) {
		client := &http.Client{Transport: telemetryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			<-req.Context().Done()
			return nil, req.Context().Err()
		})}
		r := newHTTPInvocationReporter("https://hub.example.com/api/v1/telemetry/invocations", "token", "test", invocationReporterOptions{
			client: client, batchSize: 1, requestTimeout: 10 * time.Millisecond, flushInterval: time.Hour,
		})
		r.Report(InvocationEvent{Surface: "mcp", Tool: "list"})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		r.Close(ctx)
		require.NoError(t, ctx.Err())
	})

	t.Run("unsupported endpoint disables process reporter", func(t *testing.T) {
		var calls atomic.Int64
		client := &http.Client{Transport: telemetryRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		})}
		r := newHTTPInvocationReporter("https://old.example.com/api/v1/telemetry/invocations", "token", "test", invocationReporterOptions{
			client: client, batchSize: 1, flushInterval: time.Hour,
		})
		r.Report(InvocationEvent{Surface: "mcp", Tool: "first"})
		require.Eventually(t, r.disabled.Load, time.Second, time.Millisecond)
		r.Report(InvocationEvent{Surface: "mcp", Tool: "second"})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		r.Close(ctx)
		require.Equal(t, int64(1), calls.Load())
	})

	// A hub older than this client rejects any field it does not know, and the
	// client cannot negotiate the payload down. Retrying a guaranteed-rejected
	// batch on every flush is pure waste, so 400 stops the process reporter the
	// same way an unsupported endpoint does.
	t.Run("rejected payload disables process reporter", func(t *testing.T) {
		var calls atomic.Int64
		client := &http.Client{Transport: telemetryRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		})}
		r := newHTTPInvocationReporter("https://old.example.com/api/v1/telemetry/invocations", "token", "test", invocationReporterOptions{
			client: client, batchSize: 1, flushInterval: time.Hour,
		})
		r.Report(InvocationEvent{Surface: "mcp", Tool: "first"})
		require.Eventually(t, r.disabled.Load, time.Second, time.Millisecond)
		r.Report(InvocationEvent{Surface: "mcp", Tool: "second"})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		r.Close(ctx)
		require.Equal(t, int64(1), calls.Load(), "the rejected batch must not be retried")
	})
}

// The agent is injected by the reporter, like the client version, so neither
// the CLI nor the MCP call site has to remember to set it.
func TestHTTPInvocationReporterInjectsAgent(t *testing.T) {
	bodies := make(chan []byte, 1)
	client := &http.Client{Transport: telemetryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		bodies <- body
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	r := newHTTPInvocationReporter("https://hub.example.com/api/v1/telemetry/invocations", "token", "test", invocationReporterOptions{
		client: client, batchSize: 1, flushInterval: time.Hour, agent: "qwen",
	})
	r.Report(InvocationEvent{Surface: "mcp", Tool: "list"})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		r.Close(ctx)
	}()

	select {
	case body := <-bodies:
		require.Contains(t, string(body), `"agent":"qwen"`)
	case <-time.After(time.Second):
		t.Fatal("no telemetry batch was sent")
	}
}

func TestHTTPInvocationReporterConcurrentReportAndClose(t *testing.T) {
	client := &http.Client{Transport: telemetryRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	r := newHTTPInvocationReporter("https://hub.example.com/api/v1/telemetry/invocations", "token", "test", invocationReporterOptions{
		client: client, batchSize: 50, flushInterval: time.Hour,
	})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				r.Report(InvocationEvent{Surface: "mcp", Tool: "list"})
			}
		}()
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		r.Close(ctx)
	}()
	wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r.Close(ctx)
}

func spoolFixture(t *testing.T) (*sandbox.Sandbox, string) {
	t.Helper()
	fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := NewTap(TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	return fx, invocationTelemetrySpoolDirFor(tap.ConfigService, "testkey")
}

// spoolFilesForTest lists the unclaimed spool files in dir, oldest first.
func spoolFilesForTest(t *testing.T, fx *sandbox.Sandbox, dir string) []string {
	t.Helper()
	entries, err := fx.Runtime().ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") && !strings.Contains(entry.Name(), invocationTelemetryClaimMarker) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

// readSpoolForTest returns every event spooled in dir, oldest first.
func readSpoolForTest(t *testing.T, fx *sandbox.Sandbox, dir string) []InvocationEvent {
	t.Helper()
	var events []InvocationEvent
	for _, name := range spoolFilesForTest(t, fx, dir) {
		raw, err := fx.Runtime().ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		var batch []InvocationEvent
		require.NoError(t, json.Unmarshal(raw, &batch))
		events = append(events, batch...)
	}
	return events
}

var spoolTestSeq atomic.Int64

// writeSpoolForTest adds one spool file holding events to dir.
func writeSpoolForTest(t *testing.T, fx *sandbox.Sandbox, dir string, events []InvocationEvent) {
	t.Helper()
	raw, err := json.Marshal(events)
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().Mkdir(dir, 0o700, true))
	name := fmt.Sprintf("%020d-test.json", spoolTestSeq.Add(1))
	require.NoError(t, fx.Runtime().AtomicWriteFile(filepath.Join(dir, name), raw, 0o600))
}

func noContent() (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
}

// A slow Hub must not hold up exit: Close waits only briefly for the send in
// flight, then spools it with everything still queued.
func TestHTTPInvocationReporterCloseSpoolsInsteadOfWaiting(t *testing.T) {
	fx, spool := spoolFixture(t)
	started := make(chan struct{}, 1)
	client := &http.Client{Transport: telemetryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-req.Context().Done()
		return nil, req.Context().Err()
	})}
	r := newHTTPInvocationReporter("https://hub.example.com/api/v1/telemetry/invocations", "token", "test", invocationReporterOptions{
		rt: fx.Runtime(), spoolDir: spool, client: client, batchSize: 1, flushInterval: time.Hour, requestTimeout: time.Minute,
	})
	r.Report(InvocationEvent{Surface: "cli", Command: "tap first"})
	<-started
	r.Report(InvocationEvent{Surface: "cli", Command: "tap info"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	begin := time.Now()
	r.Close(ctx)
	require.Less(t, time.Since(begin), time.Second, "Close must not wait out the request timeout")

	events := readSpoolForTest(t, fx, spool)
	require.Len(t, events, 2)
	require.Equal(t, "tap first", events[0].Command)
	require.Equal(t, "tap info", events[1].Command)
}

func TestHTTPInvocationReporterSendsAndClearsSpool(t *testing.T) {
	fx, spool := spoolFixture(t)
	writeSpoolForTest(t, fx, spool, []InvocationEvent{
		{Surface: "cli", Command: "tap one", ClientVersion: "v0.old"},
		{Surface: "cli", Command: "tap two", ClientVersion: "v0.old"},
	})
	bodies := make(chan []InvocationEvent, 4)
	client := &http.Client{Transport: telemetryRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		var batch []InvocationEvent
		_ = json.NewDecoder(req.Body).Decode(&batch)
		bodies <- batch
		return noContent()
	})}
	r := newHTTPInvocationReporter("https://hub.example.com/api/v1/telemetry/invocations", "token", "test", invocationReporterOptions{
		rt: fx.Runtime(), spoolDir: spool, client: client, flushInterval: time.Hour,
	})

	select {
	case batch := <-bodies:
		require.Len(t, batch, 2)
		require.Equal(t, "tap one", batch[0].Command)
		require.Equal(t, "v0.old", batch[0].ClientVersion, "spooled events keep the version that produced them")
	case <-time.After(time.Second):
		t.Fatal("the spool was not sent at startup")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r.Close(ctx)
	require.Empty(t, spoolFilesForTest(t, fx, spool), "a delivered spool is cleared")
}

func TestHTTPInvocationReporterSpoolIsBounded(t *testing.T) {
	fx, spool := spoolFixture(t)
	old := make([]InvocationEvent, invocationTelemetrySpoolLimit)
	for i := range old {
		old[i] = InvocationEvent{Surface: "cli", Command: fmt.Sprintf("old-%d", i)}
	}
	writeSpoolForTest(t, fx, spool, old)
	client := &http.Client{Transport: telemetryRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	r := newHTTPInvocationReporter("https://hub.example.com/api/v1/telemetry/invocations", "token", "test", invocationReporterOptions{
		rt: fx.Runtime(), spoolDir: spool, client: client, flushInterval: time.Hour,
	})
	for i := range 3 {
		r.Report(InvocationEvent{Surface: "cli", Command: fmt.Sprintf("new-%d", i)})
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r.Close(ctx)

	events := readSpoolForTest(t, fx, spool)
	require.Len(t, events, invocationTelemetrySpoolLimit)
	require.Equal(t, "old-3", events[0].Command, "the oldest events are dropped first")
	require.Equal(t, "new-2", events[len(events)-1].Command)
}

func TestInvocationTelemetryDisabledLeavesSpoolAlone(t *testing.T) {
	t.Run("opted out", func(t *testing.T) {
		fx, tap := telemetryTargetFixture(t, "disableTelemetry: true\nhub: primary\nhubs:\n  primary:\n    url: https://hub.example.com\n")
		spool := invocationTelemetrySpoolDirFor(tap.ConfigService, "testkey")
		writeSpoolForTest(t, fx, spool, []InvocationEvent{{Surface: "cli", Command: "tap kept"}})
		before := readSpoolForTest(t, fx, spool)

		require.Nil(t, NewInvocationReporter(fx.Runtime(), tap.ConfigService, "test"))
		require.Equal(t, before, readSpoolForTest(t, fx, spool), "disabled telemetry neither sends nor rewrites the spool")
	})

	t.Run("rejected by the Hub", func(t *testing.T) {
		fx, spool := spoolFixture(t)
		client := &http.Client{Transport: telemetryRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
		})}
		r := newHTTPInvocationReporter("https://old.example.com/api/v1/telemetry/invocations", "token", "test", invocationReporterOptions{
			rt: fx.Runtime(), spoolDir: spool, client: client, batchSize: 1, flushInterval: time.Hour,
		})
		r.Report(InvocationEvent{Surface: "cli", Command: "tap first"})
		require.Eventually(t, r.disabled.Load, time.Second, time.Millisecond)
		r.Report(InvocationEvent{Surface: "cli", Command: "tap second"})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		r.Close(ctx)
		require.Empty(t, spoolFilesForTest(t, fx, spool), "a Hub that rejects telemetry gets no spool")
	})
}
