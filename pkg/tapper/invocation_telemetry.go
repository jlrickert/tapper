package tapper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/apicontract"
)

const (
	invocationTelemetryQueueSize      = 256
	invocationTelemetryBatchSize      = 50
	invocationTelemetryFlushInterval  = time.Second
	invocationTelemetryRequestTimeout = 2 * time.Second

	// InvocationTelemetryInFlightWait bounds how long Close waits for a send
	// that is already in flight. Close never starts a send of its own.
	InvocationTelemetryInFlightWait = 100 * time.Millisecond

	// invocationTelemetrySpoolLimit bounds the spool; the oldest events are
	// dropped first.
	invocationTelemetrySpoolLimit = 500
	invocationTelemetrySpoolFile  = "telemetry-spool.json"
)

// InvocationEvent is the privacy-minimized shape accepted by the Tapper Hub
// invocation telemetry endpoint. Callers must set exactly one of Command or
// Tool according to Surface. ClientVersion and Agent are injected by the
// reporter.
//
// Agent is the harness `tap launch` started the process under (TAP_HARNESS),
// empty when a human is. It separates agent-driven usage from human usage in
// aggregate.
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
// caller. Events still unsent at Close are spooled for the next process.
type InvocationReporter interface {
	// Report queues an event without blocking and may drop it under pressure.
	Report(InvocationEvent)
	// Close waits for a send already in flight, bounded by ctx and
	// InvocationTelemetryInFlightWait, then spools what is left unsent.
	Close(context.Context)
}

type httpInvocationReporter struct {
	endpoint string
	token    string
	version  string
	agent    string
	client   *http.Client
	contract *apicontract.Session

	queue chan InvocationEvent
	done  chan struct{}

	// sendCtx parents every send; Close cancels it once its wait is over so
	// an in-flight request stops and its batch is spooled instead.
	sendCtx    context.Context
	cancelSend context.CancelFunc

	// rt and spoolPath locate the spool. An empty spoolPath disables it:
	// unsent events are then dropped at Close.
	rt        *toolkit.Runtime
	spoolPath string
	// spooled holds the previous process's events, sent at startup.
	spooled []InvocationEvent

	mu     sync.Mutex
	closed bool
	// unsent collects events that were never delivered: failed sends and
	// whatever the queue held at Close. Written by run, read after done.
	unsent []InvocationEvent

	disabled atomic.Bool
	dropped  atomic.Uint64

	flushInterval  time.Duration
	requestTimeout time.Duration
}

type invocationReporterOptions struct {
	rt             *toolkit.Runtime
	spoolPath      string
	contract       *apicontract.Session
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
func NewInvocationReporter(rt *toolkit.Runtime, configService *ConfigService, version string, sessions ...*apicontract.Session) InvocationReporter {
	endpoint, token, ok := resolveInvocationTelemetryTarget(rt, configService)
	if !ok {
		return nil
	}
	session := apicontract.NewSession(version, rt.Logger())
	if len(sessions) > 0 && sessions[0] != nil {
		session = sessions[0]
	}
	return newHTTPInvocationReporter(endpoint, token, version, invocationReporterOptions{
		rt:        rt,
		spoolPath: invocationTelemetrySpoolPath(configService),
		contract:  session,
		agent:     resolveTelemetryAgent(configService),
	})
}

// invocationTelemetrySpoolPath places the spool beside the auth store under
// StateRoot. It holds events only, never credentials.
func invocationTelemetrySpoolPath(configService *ConfigService) string {
	if configService == nil || configService.PathService == nil {
		return ""
	}
	return filepath.Join(configService.PathService.StateRoot, invocationTelemetrySpoolFile)
}

// resolveTelemetryAgent reads the launching harness from the merged config,
// which is where TAP_HARNESS lands. It is resolved once at construction rather
// than per event: the harness driving a process cannot change while it runs.
func resolveTelemetryAgent(configService *ConfigService) string {
	if configService == nil {
		return ""
	}
	cfg, err := configService.Config()
	if err != nil || cfg == nil {
		return ""
	}
	return cfg.LaunchHarness()
}

func resolveInvocationTelemetryTarget(rt *toolkit.Runtime, configService *ConfigService) (string, string, bool) {
	if rt == nil || configService == nil || !configService.UserConfigExists() {
		return "", "", false
	}
	if parseEnvBool(rt.Env().Get("TAP_DISABLE_TELEMETRY")) {
		return "", "", false
	}

	cfg, err := configService.Config()
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
	if opts.contract == nil {
		opts.contract = apicontract.NewSession(version, nil)
	}
	sendCtx, cancelSend := context.WithCancel(context.Background())
	r := &httpInvocationReporter{
		sendCtx:        sendCtx,
		cancelSend:     cancelSend,
		rt:             opts.rt,
		spoolPath:      opts.spoolPath,
		endpoint:       endpoint,
		token:          token,
		version:        version,
		agent:          opts.agent,
		client:         opts.client,
		contract:       opts.contract,
		queue:          make(chan InvocationEvent, opts.queueSize),
		done:           make(chan struct{}),
		flushInterval:  opts.flushInterval,
		requestTimeout: opts.requestTimeout,
	}
	r.spooled = r.takeSpool()
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

	// Give a send already in flight a short window, then cancel it so its
	// batch joins the spool rather than holding up exit.
	wait := time.NewTimer(InvocationTelemetryInFlightWait)
	defer wait.Stop()
	select {
	case <-r.done:
	case <-wait.C:
	case <-ctx.Done():
	}
	if r.cancelSend != nil {
		r.cancelSend()
	}
	select {
	case <-r.done:
	case <-ctx.Done():
		return
	}
	r.writeSpool()
}

func (r *httpInvocationReporter) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func (r *httpInvocationReporter) run(batchSize int) {
	defer close(r.done)
	ticker := time.NewTicker(r.flushInterval)
	defer ticker.Stop()

	// The previous process's events go first, overlapping the command's own
	// requests instead of delaying its exit.
	for len(r.spooled) > 0 && !r.disabled.Load() && !r.isClosed() {
		n := min(batchSize, len(r.spooled))
		r.flush(r.spooled[:n])
		r.spooled = r.spooled[n:]
	}
	r.keepUnsent(r.spooled)
	r.spooled = nil

	batch := make([]InvocationEvent, 0, batchSize)
	for {
		select {
		case event, ok := <-r.queue:
			if !ok {
				r.keepUnsent(batch)
				return
			}
			if r.disabled.Load() {
				continue
			}
			batch = append(batch, event)
			// Close never starts a send; the drained remainder is spooled.
			if len(batch) >= batchSize && !r.isClosed() {
				r.flush(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) == 0 || r.disabled.Load() || r.isClosed() {
				continue
			}
			r.flush(batch)
			batch = batch[:0]
		}
	}
}

// flush sends one batch and keeps it for the spool when delivery failed for a
// reason a later process might not hit.
func (r *httpInvocationReporter) flush(batch []InvocationEvent) {
	parent := r.sendCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, r.requestTimeout)
	defer cancel()
	if !r.send(ctx, batch) && !r.disabled.Load() {
		r.keepUnsent(batch)
	}
}

// keepUnsent holds events for the spool, bounded so a long-lived MCP server
// with an unreachable Hub does not grow without limit.
func (r *httpInvocationReporter) keepUnsent(events []InvocationEvent) {
	r.unsent = append(r.unsent, events...)
	if over := len(r.unsent) - invocationTelemetrySpoolLimit; over > 0 {
		r.unsent = append([]InvocationEvent(nil), r.unsent[over:]...)
	}
}

// takeSpool reads and removes the previous process's spool. Any problem
// reading it just means there is nothing to resend.
func (r *httpInvocationReporter) takeSpool() []InvocationEvent {
	if r.rt == nil || r.spoolPath == "" {
		return nil
	}
	events := readInvocationSpool(r.rt, r.spoolPath)
	if len(events) > 0 {
		_ = r.rt.Remove(r.spoolPath, false)
	}
	return events
}

// writeSpool appends the unsent events to the spool, keeping the newest
// invocationTelemetrySpoolLimit. A disabled reporter writes nothing: the Hub
// will not accept those events from this client.
func (r *httpInvocationReporter) writeSpool() {
	if r.rt == nil || r.spoolPath == "" || len(r.unsent) == 0 || r.disabled.Load() {
		return
	}
	events := append(readInvocationSpool(r.rt, r.spoolPath), r.unsent...)
	if len(events) > invocationTelemetrySpoolLimit {
		events = events[len(events)-invocationTelemetrySpoolLimit:]
	}
	data, err := json.Marshal(events)
	if err != nil {
		return
	}
	if parent := filepath.Dir(r.spoolPath); parent != "" {
		if _, err := r.rt.Stat(parent, false); errors.Is(err, os.ErrNotExist) {
			if err := r.rt.Mkdir(parent, 0o700, true); err != nil {
				return
			}
		}
	}
	_ = r.rt.AtomicWriteFile(r.spoolPath, data, 0o600)
}

func readInvocationSpool(rt *toolkit.Runtime, path string) []InvocationEvent {
	raw, err := rt.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return nil
	}
	var events []InvocationEvent
	if json.Unmarshal(raw, &events) != nil {
		return nil
	}
	return events
}

// send posts one batch. It reports false when the batch was not delivered;
// a response that disables the reporter counts as delivered, since resending
// it would only be rejected again.
func (r *httpInvocationReporter) send(ctx context.Context, batch []InvocationEvent) bool {
	payload, err := json.Marshal(batch)
	if err != nil {
		return true
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(payload))
	if err != nil {
		return true
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(apicontract.WithSession(req.Context(), r.contract))
	resp, err := apicontract.Do(r.client, req)
	if err != nil {
		if apicontract.IsCompatibility(err) {
			r.disabled.Store(true)
			return true
		}
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
	if disablesInvocationTelemetry(resp.StatusCode) {
		r.disabled.Store(true)
		return true
	}
	return resp.StatusCode < 300 || resp.StatusCode == http.StatusRequestEntityTooLarge
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
