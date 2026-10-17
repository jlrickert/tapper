package tapper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	// invocationTelemetrySpoolDir holds one spool directory per destination
	// (hub and account), so events are only ever resent where they belong.
	invocationTelemetrySpoolDir = "telemetry-spool"
	// invocationTelemetryLegacySpoolFile is the single shared spool file of
	// earlier releases. Its events carry no destination, so it is deleted
	// unsent.
	invocationTelemetryLegacySpoolFile = "telemetry-spool.json"
	// invocationTelemetryClaimMarker separates a spool file's name from the
	// claim suffix a reporter adds when it takes the file.
	invocationTelemetryClaimMarker = ".claimed-"
	// invocationTelemetryStaleClaim is how long a claimed file may linger
	// before it is treated as left behind by a crashed process.
	invocationTelemetryStaleClaim = 24 * time.Hour
	// invocationTelemetryRejectionMarker, in a destination's spool directory,
	// records that the hub refused this client's events. It has no .json
	// suffix, so it is never taken for a spool file.
	invocationTelemetryRejectionMarker = "rejected"
	// invocationTelemetryRejectionTTL is how long a rejection keeps processes
	// of the same client version from sending.
	invocationTelemetryRejectionTTL = 24 * time.Hour
	// invocationTelemetrySpoolDirMaxAge is how long another login's spool
	// directory is kept after its last write, in case that login returns.
	invocationTelemetrySpoolDirMaxAge = 7 * 24 * time.Hour
	// invocationTelemetrySpoolDirLimit bounds how many spool directories,
	// the current login's included, are kept at once.
	invocationTelemetrySpoolDirLimit = 8
)

// invocationSpoolSeq keeps spool file names unique within one process.
var invocationSpoolSeq atomic.Uint64

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

	// rt and spoolDir locate this destination's spool. An empty spoolDir
	// disables it: unsent events are then dropped at Close.
	rt       *toolkit.Runtime
	spoolDir string
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
	spoolDir       string
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
	endpoint, token, key, ok := resolveInvocationTelemetryTarget(rt, configService)
	if !ok {
		return nil
	}
	if legacy := invocationTelemetryLegacySpoolPath(configService); legacy != "" {
		_ = rt.Remove(legacy, false)
	}
	session := apicontract.NewSession(version, rt.Logger())
	if len(sessions) > 0 && sessions[0] != nil {
		session = sessions[0]
	}
	spoolDir := invocationTelemetrySpoolDirFor(configService, key)
	if invocationTelemetryRejected(rt, spoolDir, version) {
		return nil
	}
	if key != "" {
		pruneInvocationSpoolDirs(rt, filepath.Join(configService.PathService.StateRoot, invocationTelemetrySpoolDir), key)
	}
	return newHTTPInvocationReporter(endpoint, token, version, invocationReporterOptions{
		rt:       rt,
		spoolDir: spoolDir,
		contract: session,
		agent:    resolveTelemetryAgent(configService),
	})
}

// invocationTelemetryRejection is the rejection marker's content.
type invocationTelemetryRejection struct {
	ClientVersion string    `json:"client_version"`
	Status        int       `json:"status"`
	At            time.Time `json:"at"`
}

// invocationTelemetryRejected reports whether this destination refused
// events from this client version within invocationTelemetryRejectionTTL. A
// rejection says nothing about another version, so an upgrade tries again.
func invocationTelemetryRejected(rt *toolkit.Runtime, spoolDir, version string) bool {
	if rt == nil || spoolDir == "" {
		return false
	}
	raw, err := rt.ReadFile(filepath.Join(spoolDir, invocationTelemetryRejectionMarker))
	if err != nil {
		return false
	}
	var marker invocationTelemetryRejection
	if json.Unmarshal(raw, &marker) != nil || marker.ClientVersion != version {
		return false
	}
	age := rt.Clock().Now().Sub(marker.At)
	return age >= 0 && age < invocationTelemetryRejectionTTL
}

// writeRejection records that the hub refused this client's events, so the
// processes that follow do not each send a request bound to be refused.
func (r *httpInvocationReporter) writeRejection(status int) {
	if r.rt == nil || r.spoolDir == "" {
		return
	}
	data, err := json.Marshal(invocationTelemetryRejection{ClientVersion: r.version, Status: status, At: r.rt.Clock().Now().UTC()})
	if err != nil {
		return
	}
	if _, err := r.rt.Stat(r.spoolDir, false); errors.Is(err, os.ErrNotExist) {
		if err := r.rt.Mkdir(r.spoolDir, 0o700, true); err != nil {
			return
		}
	}
	_ = r.rt.AtomicWriteFile(filepath.Join(r.spoolDir, invocationTelemetryRejectionMarker), data, 0o600)
}

// invocationTelemetrySpoolDirFor places a destination's spool beside the auth
// store under StateRoot. It holds events only, never credentials; key names
// the destination without revealing it.
func invocationTelemetrySpoolDirFor(configService *ConfigService, key string) string {
	if configService == nil || configService.PathService == nil || key == "" {
		return ""
	}
	return filepath.Join(configService.PathService.StateRoot, invocationTelemetrySpoolDir, key)
}

func invocationTelemetryLegacySpoolPath(configService *ConfigService) string {
	if configService == nil || configService.PathService == nil {
		return ""
	}
	return filepath.Join(configService.PathService.StateRoot, invocationTelemetryLegacySpoolFile)
}

// invocationTelemetryKey names the spool directory for the hub and the login
// signed in to it. It is built from the entry's AccountKey, which survives
// token refreshes, never from a credential: a rotated refresh token must not
// strand what was spooled before it. A new login mints a new AccountKey, and
// another hub hashes differently, so events are never sent to a destination
// that did not produce them.
func invocationTelemetryKey(hubURL, accountKey string) string {
	sum := sha256.Sum256([]byte(CanonicalHubURL(hubURL) + "\naccount:" + accountKey))
	return hex.EncodeToString(sum[:])[:16]
}

// invocationTelemetryCredentialKey is the spool key earlier releases derived
// from the stored credential. The reporter claims that directory for the
// login while the credential is still the one that keyed it.
func invocationTelemetryCredentialKey(hubURL string, entry *AuthEntry) string {
	credential := entry.RefreshToken
	if strings.TrimSpace(credential) == "" {
		credential = entry.AccessToken
	}
	sum := sha256.Sum256([]byte(CanonicalHubURL(hubURL) + "\n" + credential))
	return hex.EncodeToString(sum[:])[:16]
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

// resolveInvocationTelemetryTarget returns the endpoint, the bearer token,
// and the spool identity key (see invocationTelemetryKey). It names a login
// from an earlier release and claims that login's credential-keyed spool. An
// empty key means the reporter keeps no spool.
func resolveInvocationTelemetryTarget(rt *toolkit.Runtime, configService *ConfigService) (endpoint, token, key string, ok bool) {
	if rt == nil || configService == nil || !configService.UserConfigExists() {
		return "", "", "", false
	}
	if parseEnvBool(rt.Env().Get("TAP_DISABLE_TELEMETRY")) {
		return "", "", "", false
	}

	cfg, err := configService.Config()
	if err != nil || cfg == nil || cfg.DisableTelemetry() {
		return "", "", "", false
	}

	hubURL, err := ResolveLoginHubURL(cfg, "")
	if err != nil || strings.TrimSpace(hubURL) == "" {
		return "", "", "", false
	}
	store, err := LoadAuthStore(context.Background(), rt, configService.PathService.AuthStorePath())
	if err != nil {
		return "", "", "", false
	}
	entry, found := store.Get(hubURL)
	if !found || strings.TrimSpace(entry.AccessToken) == "" {
		return "", "", "", false
	}
	accountKey := entry.AccountKey
	if accountKey == "" {
		// A login from an earlier release: name it now. When that fails the
		// reporter still sends, it just keeps no spool.
		accountKey, _ = ensureAuthAccountKey(context.Background(), rt, configService.PathService.AuthStorePath(), hubURL)
	}
	if accountKey != "" {
		key = invocationTelemetryKey(hubURL, accountKey)
		claimInvocationSpoolDir(rt, filepath.Join(configService.PathService.StateRoot, invocationTelemetrySpoolDir), invocationTelemetryCredentialKey(hubURL, entry), key)
	}
	return strings.TrimRight(hubURL, "/") + "/api/v1/telemetry/invocations", entry.AccessToken, key, true
}

// claimInvocationSpoolDir moves the spool files of directory from into
// directory to, then removes from. Earlier releases keyed the spool by the
// current credential; this hands those events to the login that produced
// them. A file another process claims first is simply skipped.
func claimInvocationSpoolDir(rt *toolkit.Runtime, root, from, to string) {
	if rt == nil || root == "" || from == "" || to == "" || from == to {
		return
	}
	src, dst := filepath.Join(root, from), filepath.Join(root, to)
	entries, err := rt.ReadDir(src)
	if err != nil {
		return
	}
	if _, err := rt.Stat(dst, false); errors.Is(err, os.ErrNotExist) {
		if err := rt.Mkdir(dst, 0o700, true); err != nil {
			return
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || strings.Contains(name, invocationTelemetryClaimMarker) {
			continue
		}
		_ = rt.Rename(filepath.Join(src, name), filepath.Join(dst, name))
	}
	_ = rt.Remove(src, true)
}

// keepSpoolAcrossRotation runs when a refresh is about to replace prev's
// credential with next's. A login from an earlier release has no AccountKey
// yet and its spool is keyed by the credential being spent, which nothing
// could name afterwards, so the login is named now (persisted, and carried
// onto next) and that spool is claimed for it. The spool lives beside the
// auth store. The caller holds the auth store lock.
func keepSpoolAcrossRotation(ctx context.Context, rt *toolkit.Runtime, storePath, storeKey, hubURL string, prev, next *AuthEntry) {
	if rt == nil || storePath == "" || prev == nil || next == nil || next.AccountKey != "" {
		return
	}
	// The refresh already holds the auth store lock.
	accountKey, err := ensureAuthAccountKeyLocked(ctx, rt, storePath, storeKey)
	if err != nil {
		return
	}
	next.AccountKey = accountKey
	root := filepath.Join(filepath.Dir(storePath), invocationTelemetrySpoolDir)
	claimInvocationSpoolDir(rt, root, invocationTelemetryCredentialKey(hubURL, prev), invocationTelemetryKey(hubURL, accountKey))
}

// pruneInvocationSpoolDirs removes the spool directories of other logins that
// have spooled nothing for invocationTelemetrySpoolDirMaxAge, then the oldest
// of the rest until at most invocationTelemetrySpoolDirLimit remain. A
// directory's age is that of its newest file name, which is stamped from the
// runtime clock. The current login's directory is never removed.
func pruneInvocationSpoolDirs(rt *toolkit.Runtime, root, keep string) {
	entries, err := rt.ReadDir(root)
	if err != nil {
		return
	}
	now := rt.Clock().Now()
	type spoolDir struct {
		name   string
		newest time.Time
	}
	var dirs []spoolDir
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == keep {
			continue
		}
		newest := invocationSpoolDirNewest(rt, filepath.Join(root, name))
		if now.Sub(newest) > invocationTelemetrySpoolDirMaxAge {
			_ = rt.Remove(filepath.Join(root, name), true)
			continue
		}
		dirs = append(dirs, spoolDir{name: name, newest: newest})
	}
	slices.SortFunc(dirs, func(a, b spoolDir) int { return a.newest.Compare(b.newest) })
	// One place is always held for the current login's directory, which
	// may not exist until this process spools.
	for len(dirs)+1 > invocationTelemetrySpoolDirLimit && len(dirs) > 0 {
		_ = rt.Remove(filepath.Join(root, dirs[0].name), true)
		dirs = dirs[1:]
	}
}

// invocationSpoolDirNewest reports when the newest file in a spool directory
// was written or claimed. A directory with no recognizable file reads as
// ancient.
func invocationSpoolDirNewest(rt *toolkit.Runtime, dir string) time.Time {
	entries, err := rt.ReadDir(dir)
	if err != nil {
		return time.Time{}
	}
	var newest time.Time
	for _, entry := range entries {
		at, ok := invocationSpoolFileTime(entry.Name())
		if ok && at.After(newest) {
			newest = at
		}
	}
	return newest
}

// invocationSpoolFileTime reads the time a spool file name carries: its claim
// time when claimed, otherwise the leading write timestamp.
func invocationSpoolFileTime(name string) (time.Time, bool) {
	if at, ok := invocationSpoolClaimTime(name); ok {
		return at, !at.IsZero()
	}
	stamp, _, _ := strings.Cut(name, "-")
	nanos, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, nanos), true
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
		spoolDir:       opts.spoolDir,
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

// takeSpool claims and removes every spooled file for this destination. A
// file is claimed by renaming it, so when several processes start at once
// each file goes to exactly one of them. Any problem reading the spool just
// means there is nothing to resend.
func (r *httpInvocationReporter) takeSpool() []InvocationEvent {
	if r.rt == nil || r.spoolDir == "" {
		return nil
	}
	entries, err := r.rt.ReadDir(r.spoolDir)
	if err != nil {
		return nil
	}
	now := r.rt.Clock().Now()
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if claimedAt, ok := invocationSpoolClaimTime(name); ok {
			if now.Sub(claimedAt) > invocationTelemetryStaleClaim {
				_ = r.rt.Remove(filepath.Join(r.spoolDir, name), false)
			}
			continue
		}
		if strings.HasSuffix(name, ".json") {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	var events []InvocationEvent
	for _, name := range names {
		path := filepath.Join(r.spoolDir, name)
		claimed := fmt.Sprintf("%s%s%d-%d", path, invocationTelemetryClaimMarker, now.UnixNano(), r.pid())
		if err := r.rt.Rename(path, claimed); err != nil {
			// Another process claimed it first.
			continue
		}
		events = append(events, readInvocationSpool(r.rt, claimed)...)
		_ = r.rt.Remove(claimed, false)
	}
	return events
}

// invocationSpoolClaimTime reports when a claimed spool file was claimed.
func invocationSpoolClaimTime(name string) (time.Time, bool) {
	i := strings.LastIndex(name, invocationTelemetryClaimMarker)
	if i < 0 {
		return time.Time{}, false
	}
	stamp, _, _ := strings.Cut(name[i+len(invocationTelemetryClaimMarker):], "-")
	nanos, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		// Unparseable: treat it as ancient so it is cleaned up.
		return time.Time{}, true
	}
	return time.Unix(0, nanos), true
}

func (r *httpInvocationReporter) pid() int {
	if p := r.rt.Process(); p != nil {
		return p.PID
	}
	return 0
}

// writeSpool writes the unsent events to a new file of their own, so
// concurrent processes never read-modify-write a shared file, then prunes
// the oldest files so the spool keeps at most invocationTelemetrySpoolLimit
// events. A disabled reporter writes nothing: the Hub will not accept those
// events from this client.
func (r *httpInvocationReporter) writeSpool() {
	if r.rt == nil || r.spoolDir == "" || len(r.unsent) == 0 || r.disabled.Load() {
		return
	}
	data, err := json.Marshal(r.unsent)
	if err != nil {
		return
	}
	if _, err := r.rt.Stat(r.spoolDir, false); errors.Is(err, os.ErrNotExist) {
		if err := r.rt.Mkdir(r.spoolDir, 0o700, true); err != nil {
			return
		}
	}
	name := fmt.Sprintf("%020d-%d-%d.json", r.rt.Clock().Now().UnixNano(), r.pid(), invocationSpoolSeq.Add(1))
	if err := r.rt.AtomicWriteFile(filepath.Join(r.spoolDir, name), data, 0o600); err != nil {
		return
	}
	r.pruneSpool()
}

// pruneSpool drops the oldest spool files, by name, until the rest hold at
// most invocationTelemetrySpoolLimit events. Whole files go, never part of
// one, so a file another process is claiming is never rewritten.
func (r *httpInvocationReporter) pruneSpool() {
	entries, err := r.rt.ReadDir(r.spoolDir)
	if err != nil {
		return
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, ".json") && !strings.Contains(name, invocationTelemetryClaimMarker) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	total := 0
	for i := len(names) - 1; i >= 0; i-- {
		path := filepath.Join(r.spoolDir, names[i])
		if total >= invocationTelemetrySpoolLimit {
			_ = r.rt.Remove(path, false)
			continue
		}
		n := len(readInvocationSpool(r.rt, path))
		if total+n > invocationTelemetrySpoolLimit {
			_ = r.rt.Remove(path, false)
			total = invocationTelemetrySpoolLimit
			continue
		}
		total += n
	}
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
			r.disable(0)
			return true
		}
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
	if disablesInvocationTelemetry(resp.StatusCode) {
		r.disable(resp.StatusCode)
		return true
	}
	return resp.StatusCode < 300 || resp.StatusCode == http.StatusRequestEntityTooLarge
}

// disable stops this reporter and, once per process, records the rejection
// for the processes that follow. status is 0 when the API contract, not a
// response status, ruled this client out. A credential failure stops only
// this process: it is not recorded, so a later process holding a refreshed
// or different credential sends again.
func (r *httpInvocationReporter) disable(status int) {
	if r.disabled.Swap(true) {
		return
	}
	if invocationTelemetryCredentialFailure(status) {
		return
	}
	r.writeRejection(status)
}

// invocationTelemetryCredentialFailure reports whether a status refuses the
// credential the request carried rather than this client. 401 is an expired
// or revoked access token, which tap's auth refresh replaces. 403 is the hub
// denying that credential — on the telemetry route, a token bound to an
// orientation the hub no longer honors — and another credential may be
// accepted. Neither says anything about this client version, so neither may
// leave a rejection marker that would silence the next process for a day.
func invocationTelemetryCredentialFailure(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

// disablesInvocationTelemetry reports whether a status means "stop trying" as
// opposed to "try again later". Retrying any of these within this process
// only wastes requests. Most say the hub will never accept this client's
// events and are remembered across processes; 401 and 403 instead refuse the
// credential this process holds, so they stop this process only (see
// invocationTelemetryCredentialFailure).
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
