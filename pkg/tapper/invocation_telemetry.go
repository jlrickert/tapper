package tapper

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

const (
	invocationTelemetryQueueSize      = 256
	invocationTelemetryBatchSize      = 50
	invocationTelemetryFlushInterval  = time.Second
	invocationTelemetryRequestTimeout = 2 * time.Second
	invocationTelemetryShutdownFlush  = 500 * time.Millisecond
)

// InvocationEvent is the privacy-minimized shape accepted by the Tapper Hub
// invocation telemetry endpoint. Callers must set exactly one of Command or
// Tool according to Surface. ClientVersion and Agent are injected by the
// reporter.
//
// Agent is the configured `tap launch` agent alias driving the process, empty
// when a human is. It is a user-chosen label, not an identifier, and separates
// agent-driven usage from human usage in aggregate.
type InvocationEvent struct {
	Surface       string `json:"surface"`
	Command       string `json:"command,omitempty"`
	Tool          string `json:"tool,omitempty"`
	DurationMS    int64  `json:"duration_ms"`
	Success       bool   `json:"success"`
	Interactive   *bool  `json:"interactive,omitempty"`
	ClientVersion string `json:"client_version"`
	Agent         string `json:"agent,omitempty"`
}

// InvocationReporter accepts best-effort telemetry without blocking the
// caller. Close gives queued events a bounded final flush.
type InvocationReporter interface {
	// Report queues an event without blocking and may drop it under pressure.
	Report(InvocationEvent)
	// Close waits for a bounded final flush until ctx is done.
	Close(context.Context)
}

type httpInvocationReporter struct {
	endpoint string
	token    string
	version  string
	agent    string
	client   *http.Client

	queue chan InvocationEvent
	done  chan struct{}

	mu     sync.Mutex
	closed bool

	disabled atomic.Bool
	dropped  atomic.Uint64

	flushInterval  time.Duration
	requestTimeout time.Duration
}

type invocationReporterOptions struct {
	client         *http.Client
	queueSize      int
	batchSize      int
	flushInterval  time.Duration
	requestTimeout time.Duration
	agent          string
}

// NewInvocationReporter resolves the user-scope reporting destination and
// existing AuthStore token. It returns nil when telemetry is disabled or the
// client is not bootstrapped, authenticated, or configured with a remote hub.
func NewInvocationReporter(rt *toolkit.Runtime, configService *ConfigService, version string) InvocationReporter {
	endpoint, token, ok := resolveInvocationTelemetryTarget(rt, configService)
	if !ok {
		return nil
	}
	return newHTTPInvocationReporter(endpoint, token, version, invocationReporterOptions{
		agent: resolveTelemetryAgent(configService),
	})
}

// resolveTelemetryAgent reads the active agent from the merged config, which is
// where TAP_AGENT lands. It is resolved once at construction rather than per
// event: the agent driving a process cannot change while it runs.
func resolveTelemetryAgent(configService *ConfigService) string {
	if configService == nil {
		return ""
	}
	cfg, err := configService.Config()
	if err != nil || cfg == nil {
		return ""
	}
	return cfg.AgentName()
}

func resolveInvocationTelemetryTarget(rt *toolkit.Runtime, configService *ConfigService) (string, string, bool) {
	if rt == nil || configService == nil || !configService.UserConfigExists() {
		return "", "", false
	}
	if parseEnvBool(rt.Env().Get("TAP_DISABLE_TELEMETRY")) {
		return "", "", false
	}

	var (
		cfg *Config
		err error
	)
	if configService.ConfigPath != "" {
		cfg, err = configService.Config()
	} else {
		cfg, err = configService.UserConfig()
	}
	if err != nil || cfg == nil || cfg.DisableTelemetry() {
		return "", "", false
	}

	hubURL, err := ResolveLoginHubURL(cfg, "")
	if err != nil || strings.TrimSpace(hubURL) == "" {
		return "", "", false
	}
	store, err := LoadAuthStore(context.Background(), rt, configService.PathService.AuthStorePath())
	if err != nil {
		return "", "", false
	}
	entry, ok := store.Get(hubURL)
	if !ok || strings.TrimSpace(entry.AccessToken) == "" {
		return "", "", false
	}
	return strings.TrimRight(hubURL, "/") + "/api/v1/telemetry/invocations", entry.AccessToken, true
}

func newHTTPInvocationReporter(endpoint, token, version string, opts invocationReporterOptions) *httpInvocationReporter {
	if opts.queueSize <= 0 {
		opts.queueSize = invocationTelemetryQueueSize
	}
	if opts.batchSize <= 0 || opts.batchSize > invocationTelemetryBatchSize {
		opts.batchSize = invocationTelemetryBatchSize
	}
	if opts.flushInterval <= 0 {
		opts.flushInterval = invocationTelemetryFlushInterval
	}
	if opts.requestTimeout <= 0 {
		opts.requestTimeout = invocationTelemetryRequestTimeout
	}
	if opts.client == nil {
		opts.client = &http.Client{}
	}
	r := &httpInvocationReporter{
		endpoint:       endpoint,
		token:          token,
		version:        version,
		agent:          opts.agent,
		client:         opts.client,
		queue:          make(chan InvocationEvent, opts.queueSize),
		done:           make(chan struct{}),
		flushInterval:  opts.flushInterval,
		requestTimeout: opts.requestTimeout,
	}
	go r.run(opts.batchSize)
	return r
}

func (r *httpInvocationReporter) Report(event InvocationEvent) {
	if r == nil || r.disabled.Load() {
		return
	}
	event.ClientVersion = r.version
	event.Agent = r.agent
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.disabled.Load() {
		return
	}
	select {
	case r.queue <- event:
	default:
		r.dropped.Add(1)
	}
}

func (r *httpInvocationReporter) Close(ctx context.Context) {
	if r == nil {
		return
	}
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		close(r.queue)
	}
	r.mu.Unlock()

	select {
	case <-r.done:
	case <-ctx.Done():
	}
}

func (r *httpInvocationReporter) run(batchSize int) {
	defer close(r.done)
	ticker := time.NewTicker(r.flushInterval)
	defer ticker.Stop()
	batch := make([]InvocationEvent, 0, batchSize)
	for {
		select {
		case event, ok := <-r.queue:
			if !ok {
				if len(batch) > 0 && !r.disabled.Load() {
					ctx, cancel := context.WithTimeout(context.Background(), invocationTelemetryShutdownFlush)
					r.send(ctx, batch)
					cancel()
				}
				return
			}
			if r.disabled.Load() {
				continue
			}
			batch = append(batch, event)
			if len(batch) >= batchSize {
				ctx, cancel := context.WithTimeout(context.Background(), r.requestTimeout)
				r.send(ctx, batch)
				cancel()
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) == 0 || r.disabled.Load() {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), r.requestTimeout)
			r.send(ctx, batch)
			cancel()
			batch = batch[:0]
		}
	}
}

func (r *httpInvocationReporter) send(ctx context.Context, batch []InvocationEvent) {
	payload, err := json.Marshal(batch)
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
	if disablesInvocationTelemetry(resp.StatusCode) {
		r.disabled.Store(true)
	}
}

// disablesInvocationTelemetry reports whether a status means "stop trying" as
// opposed to "try again later". Every code here says the hub will never accept
// this client's events, so retrying only wastes requests.
//
// 400 is in the list because a hub older than the client rejects any field it
// does not know — its decoder disallows unknown fields — and the client cannot
// negotiate the payload down. Without this, a tap carrying a newly added field
// would re-send a guaranteed-rejected batch on every flush for the life of the
// process. Degrading to no telemetry is the correct outcome, and it is what
// lets the client and the hub release in either order.
//
// 413 is deliberately absent: batch contents vary, so a too-large batch says
// nothing about the next one.
func disablesInvocationTelemetry(status int) bool {
	switch status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusGone,
		http.StatusNotImplemented:
		return true
	default:
		return false
	}
}
