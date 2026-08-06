package tapper

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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
	base := "fallbackHub: primary\nhubs:\n  primary:\n    url: https://hub.example.com\n"

	t.Run("default on", func(t *testing.T) {
		fx, tap := telemetryTargetFixture(t, base)
		endpoint, token, ok := resolveInvocationTelemetryTarget(fx.Runtime(), tap.ConfigService)
		require.True(t, ok)
		require.Equal(t, "https://hub.example.com/api/v1/telemetry/invocations", endpoint)
		require.Equal(t, "stored-token", token)
	})

	t.Run("user config opt out", func(t *testing.T) {
		fx, tap := telemetryTargetFixture(t, "disableTelemetry: true\n"+base)
		_, _, ok := resolveInvocationTelemetryTarget(fx.Runtime(), tap.ConfigService)
		require.False(t, ok)
	})

	t.Run("environment opt out", func(t *testing.T) {
		fx, tap := telemetryTargetFixture(t, base)
		require.NoError(t, fx.Runtime().Env().Set("TAP_DISABLE_TELEMETRY", "1"))
		_, _, ok := resolveInvocationTelemetryTarget(fx.Runtime(), tap.ConfigService)
		require.False(t, ok)
	})

	t.Run("project hub does not redirect telemetry", func(t *testing.T) {
		fx, tap := telemetryTargetFixture(t, base)
		projectConfig := "/home/testuser/.tapper/config.yaml"
		require.NoError(t, fx.Runtime().Mkdir("/home/testuser/.tapper", 0o755, true))
		require.NoError(t, fx.Runtime().AtomicWriteFile(projectConfig, []byte("defaultHub: attacker\n"), 0o644))
		endpoint, _, ok := resolveInvocationTelemetryTarget(fx.Runtime(), tap.ConfigService)
		require.True(t, ok)
		require.Equal(t, "https://hub.example.com/api/v1/telemetry/invocations", endpoint)
	})
}

func TestResolveInvocationTelemetryTargetSilentlySkipsUnavailableState(t *testing.T) {
	fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := NewTap(TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	_, _, ok := resolveInvocationTelemetryTarget(fx.Runtime(), tap.ConfigService)
	require.False(t, ok, "unbootstrapped client must skip")

	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte("fallbackHub: local\nhubs:\n  local:\n    kind: local\n    basePath: /kegs\n"), 0o644))
	tap.ConfigService.Reload()
	_, _, ok = resolveInvocationTelemetryTarget(fx.Runtime(), tap.ConfigService)
	require.False(t, ok, "local-only client must skip")

	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte("fallbackHub: remote\nhubs:\n  remote:\n    url: https://hub.example.com\n"), 0o644))
	tap.ConfigService.Reload()
	_, _, ok = resolveInvocationTelemetryTarget(fx.Runtime(), tap.ConfigService)
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
