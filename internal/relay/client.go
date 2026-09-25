package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jlrickert/tapper/pkg/apicontract"
	"github.com/jlrickert/tapper/pkg/relaycontract"
)

// ErrUnauthorized means Hub rejected the relay's credential. Reconnecting
// cannot fix it; the user has to sign in again.
var ErrUnauthorized = errors.New("hub rejected the relay credential; run `tap auth login`")

// ErrIncompatible means Hub and this relay share no protocol version.
var ErrIncompatible = errors.New("hub does not support this relay's protocol version")

// ErrDisconnected means the relay's owner disconnected it from Hub.
// Reconnecting would undo that, so it is permanent.
var ErrDisconnected = errors.New("relay was disconnected from Hub by its owner; restart `tap relay` to reconnect")

const (
	defaultCatalogInterval = time.Minute
	defaultReadTimeout     = 90 * time.Second
	writeTimeout           = 10 * time.Second
	maxFrameBytes          = 16 << 20
	minBackoff             = time.Second
	maxBackoff             = 30 * time.Second
)

// Hub is one hub the relay serves.
type Hub struct {
	// URL is the hub root, e.g. https://atlas.foldwise.dev.
	URL string
	// Token returns the current bearer token. It is called on every dial so a
	// refreshed token is picked up after a reconnect.
	Token func(ctx context.Context) (string, error)
}

// Options configures a Client.
type Options struct {
	// Hubs are served at once, each over its own connection, from one shared
	// concurrency budget and one model catalog.
	Hubs []Hub
	// Name identifies the relay to Hub.
	Name string
	// Version is the tap version reported at registration.
	Version string
	Providers     []*Provider
	Logger        *slog.Logger
	HTTPClient    *http.Client
	// CatalogInterval is how often providers are re-listed. Zero means a minute.
	CatalogInterval time.Duration
	// ReadTimeout drops a connection that has been silent this long. Hub pings
	// well inside it. Zero means 90 seconds.
	ReadTimeout time.Duration
	// OnRegistered, when set, observes each successful registration.
	OnRegistered func(hubURL string, reg relaycontract.Registered)
}

// Client maintains the relay's connections to its hubs.
type Client struct {
	opts      Options
	providers map[string]*Provider
	logger    *slog.Logger

	mu sync.Mutex
	// models is the current catalog, listed once for all hubs; sessions are
	// the registered connections a catalog change is sent to.
	models   []relaycontract.Model
	sessions map[*session]struct{}
}

// NewClient validates opts and returns a Client.
func NewClient(opts Options) (*Client, error) {
	if len(opts.Hubs) == 0 {
		return nil, errors.New("relay: at least one hub is required")
	}
	seen := make(map[string]bool, len(opts.Hubs))
	for _, h := range opts.Hubs {
		if h.URL == "" {
			return nil, errors.New("relay: hub URL is required")
		}
		if h.Token == nil {
			return nil, fmt.Errorf("relay: hub %s has no token source", h.URL)
		}
		key := strings.TrimRight(h.URL, "/")
		if seen[key] {
			return nil, fmt.Errorf("relay: hub %s is listed twice", h.URL)
		}
		seen[key] = true
	}
	if !relaycontract.ValidName(opts.Name) {
		return nil, fmt.Errorf("relay: name %q may contain only letters, digits, '.', '_' and '-'", opts.Name)
	}
	if len(opts.Providers) == 0 {
		return nil, errors.New("relay: no providers configured")
	}
	if opts.CatalogInterval <= 0 {
		opts.CatalogInterval = defaultCatalogInterval
	}
	if opts.ReadTimeout <= 0 {
		opts.ReadTimeout = defaultReadTimeout
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	providers := make(map[string]*Provider, len(opts.Providers))
	for _, p := range opts.Providers {
		if _, dup := providers[p.Name()]; dup {
			return nil, fmt.Errorf("relay: duplicate provider %q", p.Name())
		}
		providers[p.Name()] = p
	}
	return &Client{
		opts:      opts,
		providers: providers,
		logger:    logger,
		sessions:  make(map[*session]struct{}),
	}, nil
}

// Run keeps the relay connected to every hub until ctx ends. A permanent
// error on one hub (unauthorized, incompatible, disconnected by its owner)
// stops that hub only; Run returns those errors once no hub is left.
func (c *Client) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	c.models = c.listModels(runCtx)
	c.mu.Unlock()
	go c.refreshCatalog(runCtx)

	errs := make([]error, len(c.opts.Hubs))
	var wg sync.WaitGroup
	for i, hub := range c.opts.Hubs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = c.runHub(runCtx, hub)
			if errs[i] != nil && len(c.opts.Hubs) > 1 {
				c.logger.Error("relay stopped serving hub", "hub", hub.URL, "error", errs[i])
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return nil
	}
	return errors.Join(errs...)
}

// advertisedLimit is what the relay tells each hub it can take: the sum of
// its providers' limits, capped by the protocol. The real limits are per
// provider and shared by every hub, so a hub can still hear "overloaded"
// for a busy provider; this only keeps a hub from queueing more than the
// whole relay could ever run.
func (c *Client) advertisedLimit() int {
	total := 0
	for _, p := range c.opts.Providers {
		total += p.MaxConcurrent()
	}
	return min(total, relaycontract.MaxConcurrentCap)
}

// runHub keeps one hub connected until ctx ends or that hub rejects the
// relay for good.
func (c *Client) runHub(ctx context.Context, hub Hub) error {
	backoff := minBackoff
	for {
		registered, err := c.runOnce(ctx, hub)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrIncompatible) {
			return err
		}
		var hubErr *relaycontract.Error
		if errors.As(err, &hubErr) && hubErr.Code == relaycontract.CodeDisconnected {
			return ErrDisconnected
		}
		if registered {
			backoff = minBackoff
		}
		c.logger.Warn("relay disconnected; reconnecting", "hub", hub.URL, "error", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// ConnectURL converts a hub root into the relay WebSocket URL.
func ConnectURL(hubURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(hubURL, "/") + relaycontract.ConnectPath)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		return "", fmt.Errorf("relay: hub URL must be http or https, got %q", hubURL)
	}
	return u.String(), nil
}

func (c *Client) dial(ctx context.Context, hub Hub) (*websocket.Conn, error) {
	target, err := ConnectURL(hub.URL)
	if err != nil {
		return nil, err
	}
	token, err := hub.Token(ctx)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, ErrUnauthorized
	}
	h := make(http.Header)
	h.Set("Authorization", "Bearer "+token)
	h.Set(apicontract.VersionHeader, apicontract.Revision)
	if s := apicontract.FromContext(ctx); s != nil {
		h.Set(apicontract.ClientHeader, s.ClientVersion)
	}
	conn, resp, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: c.opts.HTTPClient, HTTPHeader: h})
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				return nil, ErrUnauthorized
			case http.StatusBadRequest:
				if resp.Header.Get(apicontract.VersionHeader) == "" {
					return nil, fmt.Errorf("%w: %v", ErrIncompatible, err)
				}
			}
		}
		return nil, err
	}
	conn.SetReadLimit(maxFrameBytes)
	return conn, nil
}

// listModels asks every provider for its models. A provider that cannot be
// reached contributes nothing rather than failing the relay.
func (c *Client) listModels(ctx context.Context) []relaycontract.Model {
	var out []relaycontract.Model
	for _, p := range c.opts.Providers {
		ids, err := p.ListModels(ctx)
		if err != nil {
			c.logger.Warn("relay provider unavailable", "provider", p.Name(), "error", err)
			ids = nil
		}
		// Configured transcription models are offered even when /models
		// fails or omits them: many speech servers list nothing.
		ids = p.WithTranscriptionModels(ids)
		for _, id := range ids {
			if len(out) == relaycontract.MaxModels {
				c.logger.Warn("relay model limit reached; remaining models not offered", "limit", relaycontract.MaxModels)
				return out
			}
			caps := []string{relaycontract.CapabilityChat, relaycontract.CapabilityStream}
			if p.Transcribes(id) {
				caps = []string{relaycontract.CapabilityTranscription}
			}
			out = append(out, relaycontract.Model{ID: id, Provider: p.Name(), Capabilities: caps, Priority: p.Priority()})
		}
	}
	return out
}

// session is one live connection.
type session struct {
	c    *Client
	conn *websocket.Conn

	writeMu sync.Mutex

	mu       sync.Mutex
	offered  map[string]struct{} // provider + "/" + model
	inflight map[string]context.CancelFunc

	wg sync.WaitGroup
}

func modelKey(provider, model string) string { return provider + "/" + model }

func (c *Client) runOnce(ctx context.Context, hub Hub) (registered bool, err error) {
	c.mu.Lock()
	models := c.models
	c.mu.Unlock()
	conn, err := c.dial(ctx, hub)
	if err != nil {
		return false, err
	}
	defer conn.CloseNow()

	s := &session{
		c:        c,
		conn:     conn,
		inflight: make(map[string]context.CancelFunc),
	}
	s.setOffered(models)

	if err := s.send(ctx, relaycontract.TypeRegister, "", relaycontract.Register{
		Relay:  relaycontract.RelayInfo{Name: c.opts.Name, Version: c.opts.Version, Protocols: []int{relaycontract.ProtocolVersion}},
		Limits: relaycontract.Limits{MaxConcurrent: c.advertisedLimit()},
		Models: nonNil(models),
	}); err != nil {
		return false, err
	}
	env, err := s.read(ctx)
	if err != nil {
		return false, err
	}
	switch env.Type {
	case relaycontract.TypeRegistered:
		var reg relaycontract.Registered
		if err := env.Decode(&reg); err != nil {
			return false, err
		}
		c.logger.Info("relay registered", "hub", hub.URL, "name", c.opts.Name, "models", len(reg.Models))
		if c.opts.OnRegistered != nil {
			c.opts.OnRegistered(hub.URL, reg)
		}
	case relaycontract.TypeError:
		var e relaycontract.Error
		if err := env.Decode(&e); err != nil {
			return false, err
		}
		if e.Code == relaycontract.CodeUnsupported {
			return false, fmt.Errorf("%w: %s", ErrIncompatible, e.Message)
		}
		return false, &e
	default:
		return false, fmt.Errorf("relay: expected registered, got %q", env.Type)
	}

	sessCtx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		s.wg.Wait()
	}()
	// Join the catalog broadcast only now that the hub has registered us: a
	// catalog frame before registered would be a protocol error. If the
	// catalog moved on while we were registering, catch this hub up.
	c.mu.Lock()
	c.sessions[s] = struct{}{}
	latest := c.models
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.sessions, s)
		c.mu.Unlock()
	}()
	if !slices.Equal(modelKeys(models), modelKeys(latest)) {
		s.sendCatalog(sessCtx, latest)
	}
	return true, s.readLoop(sessCtx)
}

func nonNil(models []relaycontract.Model) []relaycontract.Model {
	if models == nil {
		return []relaycontract.Model{}
	}
	return models
}

func (s *session) setOffered(models []relaycontract.Model) {
	offered := make(map[string]struct{}, len(models))
	for _, m := range models {
		offered[modelKey(m.Provider, m.ID)] = struct{}{}
	}
	s.mu.Lock()
	s.offered = offered
	s.mu.Unlock()
}

func (s *session) isOffered(provider, model string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.offered[modelKey(provider, model)]
	return ok
}

func (s *session) send(ctx context.Context, typ, id string, payload any) error {
	env, err := relaycontract.NewEnvelope(typ, id, payload)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(wctx, s.conn, env)
}

func (s *session) read(ctx context.Context) (relaycontract.Envelope, error) {
	rctx, cancel := context.WithTimeout(ctx, s.c.opts.ReadTimeout)
	defer cancel()
	var env relaycontract.Envelope
	err := wsjson.Read(rctx, s.conn, &env)
	return env, err
}

func (s *session) readLoop(ctx context.Context) error {
	for {
		env, err := s.read(ctx)
		if err != nil {
			return err
		}
		switch env.Type {
		case relaycontract.TypePing:
			if err := s.send(ctx, relaycontract.TypePong, env.ID, nil); err != nil {
				return err
			}
		case relaycontract.TypeInfer:
			s.startInfer(ctx, env)
		case relaycontract.TypeCancel:
			s.mu.Lock()
			cancel := s.inflight[env.ID]
			s.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		case relaycontract.TypeError:
			var e relaycontract.Error
			if env.Decode(&e) == nil && env.ID == "" {
				return &e
			}
		default:
			s.c.logger.Debug("relay ignoring frame", "type", env.Type)
		}
	}
}

func (s *session) startInfer(ctx context.Context, env relaycontract.Envelope) {
	var req relaycontract.Infer
	if err := env.Decode(&req); err != nil {
		s.fail(ctx, env.ID, relaycontract.CodeBadRequest, err.Error())
		return
	}
	if env.ID == "" {
		return
	}
	provider := s.c.providers[req.Provider]
	if provider == nil || !s.isOffered(req.Provider, req.Model) {
		s.fail(ctx, env.ID, relaycontract.CodeUnknownModel, "model is not offered by this relay")
		return
	}
	// A model does one job: transcription models only transcribe, and
	// nothing else is sent to /audio/transcriptions.
	if (req.API == relaycontract.APIOpenAIAudioTranscriptions) != provider.Transcribes(req.Model) {
		s.fail(ctx, env.ID, relaycontract.CodeUnsupported, "model does not support "+req.API)
		return
	}
	// The provider's limit, shared by every hub: a full Ollama turns this
	// request away without holding up another provider's.
	if !provider.tryAcquire() {
		s.fail(ctx, env.ID, relaycontract.CodeOverloaded, "provider "+provider.Name()+" is at its concurrency limit")
		return
	}
	reqCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.inflight[env.ID] = cancel
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer func() {
			cancel()
			s.mu.Lock()
			delete(s.inflight, env.ID)
			s.mu.Unlock()
			provider.release()
			s.wg.Done()
		}()
		s.infer(ctx, reqCtx, env.ID, provider, req)
	}()
}

func (s *session) infer(connCtx, reqCtx context.Context, id string, p *Provider, req relaycontract.Infer) {
	emit := func(obj json.RawMessage) error {
		return s.send(connCtx, relaycontract.TypeChunk, id, relaycontract.Chunk{Data: obj})
	}
	var usage *relaycontract.Usage
	var err error
	if req.API == relaycontract.APIOpenAIAudioTranscriptions {
		var result json.RawMessage
		if result, err = p.Transcribe(reqCtx, req.Body, req.Model); err == nil {
			err = emit(result)
		}
	} else {
		usage, err = p.ChatCompletions(reqCtx, req.Body, req.Model, req.Stream, emit)
	}
	if err != nil {
		code := relaycontract.CodeProviderError
		msg := err.Error()
		var perr *ProviderError
		switch {
		case reqCtx.Err() != nil:
			code, msg = relaycontract.CodeCancelled, "request cancelled"
		case errors.As(err, &perr):
			msg = perr.Message
		default:
			code = relaycontract.CodeProviderUnavailable
		}
		s.fail(connCtx, id, code, msg)
		return
	}
	if err := s.send(connCtx, relaycontract.TypeDone, id, relaycontract.Done{Usage: usage}); err != nil {
		s.c.logger.Debug("relay could not send done", "error", err)
	}
}

func (s *session) fail(ctx context.Context, id, code, msg string) {
	if err := s.send(ctx, relaycontract.TypeError, id, relaycontract.Error{Code: code, Message: msg}); err != nil {
		s.c.logger.Debug("relay could not send error", "error", err)
	}
}

// refreshCatalog re-lists the providers on an interval and sends a changed
// catalog to every registered hub.
func (c *Client) refreshCatalog(ctx context.Context) {
	ticker := time.NewTicker(c.opts.CatalogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		models := c.listModels(ctx)
		c.mu.Lock()
		if slices.Equal(modelKeys(c.models), modelKeys(models)) {
			c.mu.Unlock()
			continue
		}
		c.models = models
		sessions := make([]*session, 0, len(c.sessions))
		for s := range c.sessions {
			sessions = append(sessions, s)
		}
		c.mu.Unlock()
		for _, s := range sessions {
			s.sendCatalog(ctx, models)
		}
		c.logger.Info("relay catalog updated", "models", len(models), "hubs", len(sessions))
	}
}

// sendCatalog tells this session's hub the relay now offers models. A send
// that fails means the connection is going; its read loop ends it.
func (s *session) sendCatalog(ctx context.Context, models []relaycontract.Model) {
	s.setOffered(models)
	if err := s.send(ctx, relaycontract.TypeCatalog, "", relaycontract.Catalog{Models: nonNil(models)}); err != nil {
		s.c.logger.Debug("relay could not send catalog", "error", err)
	}
}

// modelKeys is a catalog's identity: its provider/model ids, sorted.
func modelKeys(models []relaycontract.Model) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, modelKey(m.Provider, m.ID))
	}
	slices.Sort(out)
	return out
}
