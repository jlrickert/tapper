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

// Options configures a Client.
type Options struct {
	// HubURL is the hub root, e.g. https://atlas.foldwise.dev.
	HubURL string
	// Token returns the current bearer token. It is called on every dial so a
	// refreshed token is picked up after a reconnect.
	Token func(ctx context.Context) (string, error)
	// Name identifies the relay to Hub.
	Name string
	// Version is the tap version reported at registration.
	Version       string
	MaxConcurrent int
	Providers     []*Provider
	Logger        *slog.Logger
	HTTPClient    *http.Client
	// CatalogInterval is how often providers are re-listed. Zero means a minute.
	CatalogInterval time.Duration
	// ReadTimeout drops a connection that has been silent this long. Hub pings
	// well inside it. Zero means 90 seconds.
	ReadTimeout time.Duration
	// OnRegistered, when set, observes each successful registration.
	OnRegistered func(relaycontract.Registered)
}

// Client maintains the relay's connection to Hub.
type Client struct {
	opts      Options
	providers map[string]*Provider
	logger    *slog.Logger
}

// NewClient validates opts and returns a Client.
func NewClient(opts Options) (*Client, error) {
	if opts.HubURL == "" {
		return nil, errors.New("relay: hub URL is required")
	}
	if opts.Token == nil {
		return nil, errors.New("relay: token source is required")
	}
	if !relaycontract.ValidName(opts.Name) {
		return nil, fmt.Errorf("relay: name %q may contain only letters, digits, '.', '_' and '-'", opts.Name)
	}
	if opts.MaxConcurrent < 1 || opts.MaxConcurrent > relaycontract.MaxConcurrentCap {
		return nil, fmt.Errorf("relay: max concurrent must be between 1 and %d", relaycontract.MaxConcurrentCap)
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
	return &Client{opts: opts, providers: providers, logger: logger}, nil
}

// Run keeps the relay connected until ctx ends or a permanent error occurs.
func (c *Client) Run(ctx context.Context) error {
	backoff := minBackoff
	for {
		registered, err := c.runOnce(ctx)
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
		c.logger.Warn("relay disconnected; reconnecting", "error", err, "backoff", backoff)
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

func (c *Client) dial(ctx context.Context) (*websocket.Conn, error) {
	target, err := ConnectURL(c.opts.HubURL)
	if err != nil {
		return nil, err
	}
	token, err := c.opts.Token(ctx)
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
			continue
		}
		for _, id := range ids {
			if len(out) == relaycontract.MaxModels {
				c.logger.Warn("relay model limit reached; remaining models not offered", "limit", relaycontract.MaxModels)
				return out
			}
			out = append(out, relaycontract.Model{ID: id, Provider: p.Name(), Capabilities: []string{"chat", "stream"}})
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

	sem chan struct{}
	wg  sync.WaitGroup
}

func modelKey(provider, model string) string { return provider + "/" + model }

func (c *Client) runOnce(ctx context.Context) (registered bool, err error) {
	models := c.listModels(ctx)
	conn, err := c.dial(ctx)
	if err != nil {
		return false, err
	}
	defer conn.CloseNow()

	s := &session{
		c:        c,
		conn:     conn,
		inflight: make(map[string]context.CancelFunc),
		sem:      make(chan struct{}, c.opts.MaxConcurrent),
	}
	s.setOffered(models)

	if err := s.send(ctx, relaycontract.TypeRegister, "", relaycontract.Register{
		Relay:  relaycontract.RelayInfo{Name: c.opts.Name, Version: c.opts.Version, Protocols: []int{relaycontract.ProtocolVersion}},
		Limits: relaycontract.Limits{MaxConcurrent: c.opts.MaxConcurrent},
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
		c.logger.Info("relay registered", "hub", c.opts.HubURL, "name", c.opts.Name, "models", len(reg.Models))
		if c.opts.OnRegistered != nil {
			c.opts.OnRegistered(reg)
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
	go s.refreshCatalog(sessCtx)
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
	select {
	case s.sem <- struct{}{}:
	default:
		s.fail(ctx, env.ID, relaycontract.CodeOverloaded, "relay is at its concurrency limit")
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
			<-s.sem
			s.wg.Done()
		}()
		s.infer(ctx, reqCtx, env.ID, provider, req)
	}()
}

func (s *session) infer(connCtx, reqCtx context.Context, id string, p *Provider, req relaycontract.Infer) {
	usage, err := p.ChatCompletions(reqCtx, req.Body, req.Model, req.Stream, func(obj json.RawMessage) error {
		return s.send(connCtx, relaycontract.TypeChunk, id, relaycontract.Chunk{Data: obj})
	})
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

func (s *session) refreshCatalog(ctx context.Context) {
	ticker := time.NewTicker(s.c.opts.CatalogInterval)
	defer ticker.Stop()
	s.mu.Lock()
	current := sortedKeys(s.offered)
	s.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		models := s.c.listModels(ctx)
		next := make([]string, 0, len(models))
		for _, m := range models {
			next = append(next, modelKey(m.Provider, m.ID))
		}
		slices.Sort(next)
		if slices.Equal(current, next) {
			continue
		}
		s.setOffered(models)
		if err := s.send(ctx, relaycontract.TypeCatalog, "", relaycontract.Catalog{Models: nonNil(models)}); err != nil {
			s.c.logger.Debug("relay could not send catalog", "error", err)
			return
		}
		current = next
		s.c.logger.Info("relay catalog updated", "models", len(models))
	}
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
