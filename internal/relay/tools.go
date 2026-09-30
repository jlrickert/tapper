package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/relaycontract"
)

// The relay also forwards tool calls to MCP servers named in its owner's
// configuration. Hub can name only a server and a tool from the catalog the
// relay sent it, plus a JSON object of arguments; the command a server runs,
// the URL it listens on, its headers, and its environment all come from local
// configuration and never cross the wire.

// Defaults for a configured MCP server.
const (
	DefaultToolTimeout = 2 * time.Minute
	minToolBackoff     = time.Second
	maxToolBackoff     = time.Minute
	// toolStableAfter is how long a server must stay up before a crash no
	// longer counts against its backoff.
	toolStableAfter = time.Minute
)

// ErrToolServerDown means the MCP server is not connected right now.
var ErrToolServerDown = errors.New("mcp server is not running")

// ToolServerConfig describes one MCP server. Exactly one of Command or URL is
// set. Env is the child's whole environment (see ToolEnv); Headers are sent
// on every HTTP request, already resolved from the environment.
type ToolServerConfig struct {
	Name    string
	Title   string
	Command string
	Args    []string
	Dir     string
	Env     []string
	URL     string
	Headers map[string]string
	// Allow and Deny filter the server's tools by name (exact or glob). An
	// empty Allow offers every tool not denied.
	Allow []string
	Deny  []string
	// MaxConcurrent bounds this server's in-flight calls across every hub.
	// Zero means DefaultMaxConcurrent.
	MaxConcurrent int
	// Timeout bounds one call. Zero means DefaultToolTimeout.
	Timeout time.Duration
	// Shareable lets people the owner shares this server with call it.
	Shareable bool
}

// ToolServer supervises one MCP server: it connects (starting the command
// for a stdio server), lists the tools, reconnects after a crash, and runs
// calls. Its catalog is empty whenever it is down.
type ToolServer struct {
	cfg    ToolServerConfig
	sem    chan struct{}
	logger *slog.Logger
	http   *http.Client

	mu      sync.Mutex
	session *sdkmcp.ClientSession
	tools   []relaycontract.Tool
	// onChange is called, without mu held, after the catalog changes.
	onChange func()
}

// NewToolServer validates cfg and returns a server that is not yet running.
func NewToolServer(cfg ToolServerConfig) (*ToolServer, error) {
	if !relaycontract.ValidName(cfg.Name) {
		return nil, fmt.Errorf("relay: mcp server name %q must be 1-%d letters, digits, '.', '_' or '-'", cfg.Name, relaycontract.MaxNameLength)
	}
	switch {
	case cfg.Command == "" && cfg.URL == "":
		return nil, fmt.Errorf("relay: mcp server %s needs a command or a url", cfg.Name)
	case cfg.Command != "" && cfg.URL != "":
		return nil, fmt.Errorf("relay: mcp server %s sets both command and url; choose one", cfg.Name)
	case cfg.URL != "" && !strings.HasPrefix(cfg.URL, "http://") && !strings.HasPrefix(cfg.URL, "https://"):
		return nil, fmt.Errorf("relay: mcp server %s url must start with http:// or https://", cfg.Name)
	}
	if cfg.MaxConcurrent == 0 {
		cfg.MaxConcurrent = DefaultMaxConcurrent
	}
	if cfg.MaxConcurrent < 1 || cfg.MaxConcurrent > relaycontract.MaxConcurrentCap {
		return nil, fmt.Errorf("relay: mcp server %s maxConcurrent must be between 1 and %d", cfg.Name, relaycontract.MaxConcurrentCap)
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultToolTimeout
	}
	if cfg.Timeout < 0 {
		return nil, fmt.Errorf("relay: mcp server %s timeout must be positive", cfg.Name)
	}
	if len(cfg.Title) > relaycontract.MaxToolTitleLength {
		return nil, fmt.Errorf("relay: mcp server %s title is longer than %d bytes", cfg.Name, relaycontract.MaxToolTitleLength)
	}
	return &ToolServer{cfg: cfg, sem: make(chan struct{}, cfg.MaxConcurrent)}, nil
}

// Name returns the server's configured name.
func (s *ToolServer) Name() string { return s.cfg.Name }

// Shareable reports whether the owner lets people they share it with call it.
func (s *ToolServer) Shareable() bool { return s.cfg.Shareable }

func (s *ToolServer) tryAcquire() bool {
	select {
	case s.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *ToolServer) release() { <-s.sem }

// catalog is the server as Hub sees it, and whether it is up. A server that
// is down is left out of the tools frame entirely.
func (s *ToolServer) catalog() (relaycontract.ToolServer, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return relaycontract.ToolServer{}, false
	}
	return relaycontract.ToolServer{
		Name:          s.cfg.Name,
		Title:         s.cfg.Title,
		MaxConcurrent: cap(s.sem),
		Shareable:     s.cfg.Shareable,
		Tools:         slices.Clone(s.tools),
	}, true
}

func (s *ToolServer) offers(tool string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == nil {
		return false
	}
	return slices.ContainsFunc(s.tools, func(t relaycontract.Tool) bool { return t.Name == tool })
}

// run keeps the server connected until ctx ends.
func (s *ToolServer) run(ctx context.Context) {
	backoff := minToolBackoff
	for ctx.Err() == nil {
		started := time.Now()
		err := s.serveOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > toolStableAfter {
			backoff = minToolBackoff
		}
		s.logger.Warn("relay mcp server stopped; restarting", "server", s.cfg.Name, "error", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxToolBackoff)
	}
}

// serveOnce connects, publishes the tools, and waits for the connection to
// end.
func (s *ToolServer) serveOnce(ctx context.Context) error {
	transport, cleanup := s.transport()
	defer cleanup()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "tap-relay", Version: "1"}, &sdkmcp.ClientOptions{
		ToolListChangedHandler: func(ctx context.Context, _ *sdkmcp.ToolListChangedRequest) {
			go s.refresh(context.WithoutCancel(ctx))
		},
	})
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	tools, err := s.listTools(ctx, cs)
	if err != nil {
		_ = cs.Close()
		return fmt.Errorf("list tools: %w", err)
	}
	s.mu.Lock()
	s.session = cs
	s.tools = tools
	s.mu.Unlock()
	s.logger.Info("relay mcp server connected", "server", s.cfg.Name, "tools", len(tools))
	s.changed()

	stop := context.AfterFunc(ctx, func() { _ = cs.Close() })
	defer stop()
	err = cs.Wait()

	s.mu.Lock()
	s.session = nil
	s.tools = nil
	s.mu.Unlock()
	s.changed()
	if err == nil {
		err = errors.New("connection closed")
	}
	return err
}

// refresh re-lists a connected server's tools, publishing any change.
func (s *ToolServer) refresh(ctx context.Context) {
	s.mu.Lock()
	cs := s.session
	s.mu.Unlock()
	if cs == nil {
		return
	}
	lctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	tools, err := s.listTools(lctx, cs)
	if err != nil {
		s.logger.Debug("relay could not re-list mcp tools", "server", s.cfg.Name, "error", err)
		return
	}
	s.mu.Lock()
	if s.session != cs || sameTools(s.tools, tools) {
		s.mu.Unlock()
		return
	}
	s.tools = tools
	s.mu.Unlock()
	s.logger.Info("relay mcp tools changed", "server", s.cfg.Name, "tools", len(tools))
	s.changed()
}

func (s *ToolServer) changed() {
	if s.onChange != nil {
		s.onChange()
	}
}

// listTools pages through the server's tools and keeps the ones the filter
// admits and Hub would accept. A tool Hub would refuse is dropped here with
// a warning, so one bad tool never costs the whole catalog.
func (s *ToolServer) listTools(ctx context.Context, cs *sdkmcp.ClientSession) ([]relaycontract.Tool, error) {
	var out []relaycontract.Tool
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if !s.admits(tool.Name) {
			continue
		}
		t, err := contractTool(tool)
		if err == nil {
			err = t.Validate()
		}
		if err != nil {
			s.logger.Warn("relay skipping mcp tool", "server", s.cfg.Name, "tool", tool.Name, "error", err)
			continue
		}
		if len(out) == relaycontract.MaxToolsPerServer {
			s.logger.Warn("relay mcp server offers too many tools; skipping the rest", "server", s.cfg.Name, "limit", relaycontract.MaxToolsPerServer)
			break
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *ToolServer) admits(name string) bool {
	for _, p := range s.cfg.Deny {
		if matchModel(p, name) {
			return false
		}
	}
	if len(s.cfg.Allow) == 0 {
		return true
	}
	for _, p := range s.cfg.Allow {
		if matchModel(p, name) {
			return true
		}
	}
	return false
}

func contractTool(tool *sdkmcp.Tool) (relaycontract.Tool, error) {
	schema, err := json.Marshal(tool.InputSchema)
	if err != nil {
		return relaycontract.Tool{}, fmt.Errorf("encode inputSchema: %w", err)
	}
	t := relaycontract.Tool{
		Name:        tool.Name,
		Title:       tool.Title,
		Description: tool.Description,
		InputSchema: schema,
	}
	if a := tool.Annotations; a != nil {
		if t.Title == "" {
			t.Title = a.Title
		}
		readOnly, idempotent := a.ReadOnlyHint, a.IdempotentHint
		t.Annotations = &relaycontract.ToolAnnotations{
			ReadOnlyHint:    &readOnly,
			DestructiveHint: a.DestructiveHint,
			IdempotentHint:  &idempotent,
			OpenWorldHint:   a.OpenWorldHint,
		}
	}
	return t, nil
}

func sameTools(a, b []relaycontract.Tool) bool {
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	return bytes.Equal(ra, rb)
}

// call runs one tool and returns its CallToolResult as JSON. A tool that ran
// and failed is a result with isError, not an error.
func (s *ToolServer) call(ctx context.Context, tool string, args json.RawMessage) (json.RawMessage, error) {
	s.mu.Lock()
	cs := s.session
	s.mu.Unlock()
	if cs == nil {
		return nil, ErrToolServerDown
	}
	res, err := cs.CallTool(ctx, &sdkmcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("encode result: %w", err)
	}
	if len(raw) > relaycontract.MaxToolResultBytes {
		return toolErrorResult(fmt.Sprintf("the tool's result is larger than %d bytes", relaycontract.MaxToolResultBytes)), nil
	}
	return raw, nil
}

func toolErrorResult(msg string) json.RawMessage {
	raw, _ := json.Marshal(&sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: msg}},
		IsError: true,
	})
	return raw
}

// transport builds this server's MCP transport and a cleanup that runs after
// the session ends.
func (s *ToolServer) transport() (sdkmcp.Transport, func()) {
	if s.cfg.URL != "" {
		client := s.http
		if client == nil {
			client = http.DefaultClient
		}
		if len(s.cfg.Headers) > 0 {
			c := *client
			c.Transport = headerTransport{base: client.Transport, headers: s.cfg.Headers}
			client = &c
		}
		return &sdkmcp.StreamableClientTransport{Endpoint: s.cfg.URL, HTTPClient: client}, func() {}
	}
	cmd := exec.Command(s.cfg.Command, s.cfg.Args...)
	cmd.Dir = s.cfg.Dir
	cmd.Env = s.cfg.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.Stderr = &stderrLog{logger: s.logger, server: s.cfg.Name}
	setProcessGroup(cmd)
	return &sdkmcp.CommandTransport{Command: cmd}, func() { killProcessGroup(cmd) }
}

type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// stderrLog writes a stdio server's stderr to the relay log, a line at a
// time, never to Hub.
type stderrLog struct {
	logger *slog.Logger
	server string
	mu     sync.Mutex
	buf    []byte
}

const maxStderrLine = 4 << 10

func (w *stderrLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.emit(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > maxStderrLine {
		w.emit(w.buf)
		w.buf = w.buf[:0]
	}
	return len(p), nil
}

func (w *stderrLog) emit(line []byte) {
	line = bytes.TrimRight(line, "\r")
	if len(line) > maxStderrLine {
		line = line[:maxStderrLine]
	}
	if len(line) > 0 {
		w.logger.Debug("relay mcp server stderr", "server", w.server, "line", string(line))
	}
}

// baseToolEnv are the variables every stdio MCP server gets from the relay's
// environment: what a program needs to find its tools, its home, and its
// locale. Everything else, hub tokens and provider keys included, is passed
// only when the server's configuration names it.
var baseToolEnv = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "TMP", "TEMP", "LANG", "TERM",
	// Windows needs these to start most programs.
	"SYSTEMROOT", "SystemRoot", "COMSPEC", "ComSpec", "PATHEXT", "WINDIR", "USERPROFILE", "APPDATA", "LOCALAPPDATA",
}

// ToolEnv builds a stdio MCP server's environment from the relay's own. With
// inherit it is the whole environment; otherwise the base variables, locale
// settings (LC_*), and the names in pass. Literal values in set are applied
// last and win.
func ToolEnv(environ []string, inherit bool, pass []string, set map[string]string) []string {
	var out []string
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		if inherit || slices.Contains(baseToolEnv, key) || strings.HasPrefix(key, "LC_") || slices.Contains(pass, key) {
			if _, override := set[key]; !override {
				out = append(out, kv)
			}
		}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		out = append(out, k+"="+set[k])
	}
	return out
}
