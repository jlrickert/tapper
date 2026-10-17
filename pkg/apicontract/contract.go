// Package apicontract defines the release-independent Tapper REST contract.
package apicontract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Revision is the contract this build speaks: the revision a client sends
// on every REST request and the newest one a Hub serves.
const Revision = "2026-10-17"

// previousRevisions are older revisions a Hub still accepts, so released
// clients keep working through an upgrade. 2026-10-12 differs from
// 2026-10-17 only in POST /api/v1/@{ns}/kegs/{alias}/archive/restore, which
// 2026-10-17 answers with 202 and a job instead of 200 {imported,removed}.
// A handler whose behavior changed between revisions reads the negotiated
// one with RequestRevision and must not serve the new behavior to a request
// made under an older revision.
var previousRevisions = []string{"2026-10-12"}

// SupportedRevisions lists every revision a Hub accepts, newest first. The
// slice is a fresh copy on each call.
func SupportedRevisions() []string {
	return append([]string{Revision}, previousRevisions...)
}

func supported(rev string) bool {
	if rev == Revision {
		return true
	}
	for _, old := range previousRevisions {
		if rev == old {
			return true
		}
	}
	return false
}

type revisionKey struct{}

// RequestRevision reports the contract revision Middleware accepted for the
// request carrying ctx, or "" when the request did not pass the REST gate.
func RequestRevision(ctx context.Context) string {
	rev, _ := ctx.Value(revisionKey{}).(string)
	return rev
}

// WithRequestRevision returns ctx recording rev as the negotiated revision,
// as Middleware does for every accepted REST request. Tests of handlers that
// answer per revision use it to stand in for the gate.
func WithRequestRevision(ctx context.Context, rev string) context.Context {
	return context.WithValue(ctx, revisionKey{}, rev)
}
const VersionHeader = "Tapper-API-Version"
const ClientHeader = "Tapper-Client-Version"
const Required = "API_VERSION_REQUIRED"
const Unsupported = "API_VERSION_UNSUPPORTED"
const DiscoveryInvalid = "API_DISCOVERY_INVALID"
const DiscoveryUnavailable = "API_DISCOVERY_UNAVAILABLE"

type Discovery struct {
	ServerVersion string   `json:"server_version"`
	APIVersions   []string `json:"api_versions"`
}

// CompatibilityError guarantees the blocked application operation was not sent
// or was rejected by the server before authentication and dispatch.
type CompatibilityError struct {
	Message            string   `json:"error"`
	Code               string   `json:"code"`
	Requested          []string `json:"requested_api_versions"`
	Supported          []string `json:"supported_api_versions"`
	ServerVersion      string   `json:"server_version,omitempty"`
	OperationPerformed bool     `json:"operationPerformed"`
}

func (e *CompatibilityError) Error() string { return e.Code + ": " + e.Message }
func IsCompatibility(err error) bool        { var e *CompatibilityError; return errors.As(err, &e) }

func failure(code, message, server string, requested, supported []string) *CompatibilityError {
	if requested == nil {
		requested = []string{}
	}
	if supported == nil {
		supported = []string{}
	}
	return &CompatibilityError{Message: message, Code: code, Requested: requested, Supported: supported, ServerVersion: server}
}

// Validate accepts exactly one unmodified contract revision header value
// naming one of SupportedRevisions.
func Validate(h http.Header, server string) *CompatibilityError {
	values := h.Values(VersionHeader)
	if len(values) == 0 {
		return failure(Required, "send "+VersionHeader+": "+Revision, server, values, SupportedRevisions())
	}
	if len(values) != 1 || !supported(values[0]) {
		return failure(Unsupported, "unsupported REST contract; upgrade Tapper and Hub together", server, values, SupportedRevisions())
	}
	return nil
}

// LogFailure emits only compatibility metadata, never request contents or credentials.
func LogFailure(ctx context.Context, logger *slog.Logger, level slog.Level, client string, err error, requestID string) {
	var e *CompatibilityError
	if !errors.As(err, &e) {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Log(ctx, level, "REST API compatibility failure", "event", "api.compatibility_failure", "code", e.Code, "client_version", client, "server_version", e.ServerVersion, "requested_api_versions", e.Requested, "supported_api_versions", e.Supported, "operationPerformed", false, "request_id", requestID)
}

// Middleware gates all /api/v1 paths, including unknown operations, before auth.
// Discovery is build-only and remains usable when database services are down.
// An accepted request carries its revision in the response header and in its
// context (RequestRevision).
func Middleware(server string, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/version" && r.Method == http.MethodGet {
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(Discovery{server, SupportedRevisions()})
				return
			}
			if r.URL.Path == "/api/v1" || strings.HasPrefix(r.URL.Path, "/api/v1/") {
				if err := Validate(r.Header, server); err != nil {
					LogFailure(r.Context(), logger, slog.LevelWarn, r.Header.Get(ClientHeader), err, r.Header.Get("X-Request-ID"))
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Cache-Control", "no-store")
					w.WriteHeader(http.StatusBadRequest)
					_ = json.NewEncoder(w).Encode(err)
					return
				}
				rev := r.Header.Get(VersionHeader)
				w.Header().Set(VersionHeader, rev)
				r = r.WithContext(WithRequestRevision(r.Context(), rev))
			}
			next.ServeHTTP(w, r)
		})
	}
}

type check struct {
	done chan struct{}
	err  error
}

// DefaultDiscoveryCacheTTL is how long a cached discovery success is trusted
// when Session.CacheTTL is unset.
const DefaultDiscoveryCacheTTL = time.Hour

// DiscoveryCache persists successful discovery beyond one Session, so a new
// invocation can skip GET /api/version. Keys name a Hub base URL and this
// package's Revision. Only successes are stored; failures stay with the
// Session that saw them. Implementations must be safe for concurrent use and
// treat every error as a miss.
type DiscoveryCache interface {
	// Get returns when key last passed discovery.
	Get(key string) (time.Time, bool)
	// Put records that key passed discovery at checked.
	Put(key string, checked time.Time)
	// Delete forgets key after the Hub rejected the cached contract.
	Delete(key string)
}

// Session caches discovery per Hub for one invocation or MCP connection.
//
// Cache, when set, extends a success past the Session for CacheTTL
// (DefaultDiscoveryCacheTTL when zero), measured with Now (time.Now when
// nil). A stale entry is safe: the Hub still validates every request, and a
// typed rejection evicts the entry.
type Session struct {
	mu            sync.Mutex
	checks        map[string]*check
	ClientVersion string
	Logger        *slog.Logger

	Cache    DiscoveryCache
	CacheTTL time.Duration
	Now      func() time.Time
}

// discoveryCacheKey names base under the current Revision, so a client built
// for another revision never trusts this one's result.
func discoveryCacheKey(base string) string { return Revision + " " + base }

func (s *Session) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// cachedSuccess reports whether the persistent cache vouches for base.
func (s *Session) cachedSuccess(base string) bool {
	if s.Cache == nil {
		return false
	}
	checked, ok := s.Cache.Get(discoveryCacheKey(base))
	if !ok {
		return false
	}
	ttl := s.CacheTTL
	if ttl <= 0 {
		ttl = DefaultDiscoveryCacheTTL
	}
	age := s.now().Sub(checked)
	return age >= 0 && age < ttl
}

// evict drops base from the persistent cache after the Hub rejected the
// contract the cache vouched for.
func (s *Session) evict(base string) {
	if s.Cache != nil {
		s.Cache.Delete(discoveryCacheKey(base))
	}
}

func NewSession(client string, logger *slog.Logger) *Session {
	return &Session{checks: map[string]*check{}, ClientVersion: client, Logger: logger}
}

type sessionKey struct{}

func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, s)
}
func FromContext(ctx context.Context) *Session { s, _ := ctx.Value(sessionKey{}).(*Session); return s }

// Check performs credential-free discovery once, bounded by the caller and five seconds.
func (s *Session) Check(ctx context.Context, client *http.Client, base string) error {
	base = strings.TrimRight(base, "/")
	s.mu.Lock()
	if s.checks == nil {
		s.checks = map[string]*check{}
	}
	if old := s.checks[base]; old != nil {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-old.done:
			return old.err
		}
	}
	c := &check{done: make(chan struct{})}
	s.checks[base] = c
	s.mu.Unlock()
	if s.cachedSuccess(base) {
		close(c.done)
		return nil
	}
	c.err = discover(ctx, client, base)
	if c.err == nil && s.Cache != nil {
		s.Cache.Put(discoveryCacheKey(base), s.now())
	}
	LogFailure(ctx, s.Logger, slog.LevelError, s.ClientVersion, c.err, "")
	close(c.done)
	return c.err
}

func discover(ctx context.Context, client *http.Client, base string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/version", nil)
	if err != nil {
		return err
	}
	req.URL.User = nil
	req.Header.Set("Accept", "application/json")
	c := *client
	c.Jar = nil
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		return fmt.Errorf("Hub API discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return fmt.Errorf("Hub API discovery: authentication failed (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return failure(DiscoveryUnavailable, "Hub has no usable API discovery; upgrade Tapper and Hub together", "", []string{Revision}, nil)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil {
		return fmt.Errorf("Hub API discovery: %w", err)
	}
	var d Discovery
	if len(raw) > 65536 || json.Unmarshal(raw, &d) != nil || d.ServerVersion == "" || d.APIVersions == nil {
		return failure(DiscoveryInvalid, "malformed Hub API discovery", "", []string{Revision}, nil)
	}
	for _, rev := range d.APIVersions {
		if strings.TrimSpace(rev) == "" {
			return failure(DiscoveryInvalid, "malformed Hub API discovery", d.ServerVersion, []string{Revision}, d.APIVersions)
		}
	}
	for _, rev := range d.APIVersions {
		if rev == Revision {
			return nil
		}
	}
	return failure(Unsupported, "no common REST contract; upgrade Tapper and Hub together", d.ServerVersion, []string{Revision}, d.APIVersions)
}

// Do applies discovery, mandatory headers, redirect restrictions and typed errors
// to a REST request. Non-REST OAuth and archive requests pass through unchanged.
func Do(client *http.Client, req *http.Request) (*http.Response, error) {
	path := req.URL.Path
	pos := strings.Index(path, "/api/v1")
	if pos < 0 || (len(path) > pos+7 && path[pos+7] != '/') {
		return client.Do(req)
	}
	base := *req.URL
	base.Path = path[:pos]
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = ""
	base.User = nil
	s := FromContext(req.Context())
	if s == nil {
		s = NewSession("dev", nil)
	}
	if err := s.Check(req.Context(), client, base.String()); err != nil {
		return nil, err
	}
	cloned := req.Clone(req.Context())
	cloned.Header.Set(VersionHeader, Revision)
	cloned.Header.Set(ClientHeader, s.ClientVersion)
	c := *client
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(cloned)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusBadRequest {
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 65537))
		if readErr != nil {
			resp.Body.Close()
			return nil, readErr
		}
		// Preserve the original body for ordinary validation errors.
		resp.Body = &replayBody{Reader: io.MultiReader(bytes.NewReader(raw), resp.Body), closer: resp.Body}
		var e CompatibilityError
		if len(raw) <= 65536 && json.Unmarshal(raw, &e) == nil && (e.Code == Required || e.Code == Unsupported) && !e.OperationPerformed {
			resp.Body.Close()
			s.evict(strings.TrimRight(base.String(), "/"))
			LogFailure(req.Context(), s.Logger, slog.LevelError, s.ClientVersion, &e, resp.Header.Get("X-Request-ID"))
			return nil, &e
		}
	}
	return resp, nil
}

type replayBody struct {
	io.Reader
	closer io.Closer
}

func (b *replayBody) Close() error { return b.closer.Close() }
