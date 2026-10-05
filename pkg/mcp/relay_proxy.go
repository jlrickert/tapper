package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Relayed tools are MCP tools that a `tap relay` somewhere forwards to Hub
// from a server on its machine. Hub serves them on /mcp/relay; `tap mcp`
// proxies that endpoint so a harness such as Claude Code sees them beside the
// KEG tools. Hub decides which ones the caller may reach (their own relays,
// servers shared with them, and an agent's gate when one is named); this side
// only lists and forwards.

// IsRelayedToolName reports whether name has the shape Hub gives relayed
// tools: mcp__<owner>__<server>__<tool>. No hosted KEG tool has it.
func IsRelayedToolName(name string) bool {
	return strings.HasPrefix(name, "mcp__") && strings.Count(name, "__") >= 3
}

// DefaultRelayRefresh is how often the relayed tool list is re-read when Hub
// announces no change: relays connect and drop without telling this process.
const DefaultRelayRefresh = 60 * time.Second

// RelayProxyOptions configures StartRelayProxy.
type RelayProxyOptions struct {
	// Endpoint is Hub's /mcp/relay URL, with ?agent= when the session runs as
	// an agent.
	Endpoint string
	// Token returns the current Hub bearer token; it is read on every request
	// so a refreshed login is picked up.
	Token func() string
	// Refresh overrides DefaultRelayRefresh.
	Refresh time.Duration
	// HTTPClient overrides the client used to reach Hub.
	HTTPClient *http.Client
	Logger     *slog.Logger
	// Version is reported to Hub as the client version.
	Version string
}

type relayProxy struct {
	srv  *sdkmcp.Server
	opts RelayProxyOptions
	kick chan struct{}

	mu      sync.Mutex
	session *sdkmcp.ClientSession
	tools   map[string]string // name -> serialized tool, to spot changes
}

// StartRelayProxy keeps srv's relayed tools in step with Hub's /mcp/relay
// until ctx ends or the returned stop is called. It never blocks or fails the
// server: while Hub is unreachable the server simply has no relayed tools.
func StartRelayProxy(ctx context.Context, srv *sdkmcp.Server, opts RelayProxyOptions) (stop func()) {
	if opts.Refresh <= 0 {
		opts.Refresh = DefaultRelayRefresh
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	p := &relayProxy{srv: srv, opts: opts, kick: make(chan struct{}, 1), tools: map[string]string{}}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		p.loop(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

func (p *relayProxy) loop(ctx context.Context) {
	ticker := time.NewTicker(p.opts.Refresh)
	defer ticker.Stop()
	defer p.closeSession()
	for {
		p.refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-p.kick:
		}
	}
}

// refresh re-reads Hub's list and registers, replaces, or removes tools to
// match. Any failure drops the session and every relayed tool: a tool that
// cannot reach Hub cannot run.
func (p *relayProxy) refresh(ctx context.Context) {
	session, err := p.ensureSession(ctx)
	if err == nil {
		var listed []*sdkmcp.Tool
		listed, err = listAllTools(ctx, session)
		if err == nil {
			p.apply(listed)
			return
		}
	}
	if ctx.Err() == nil {
		p.opts.Logger.Debug("relayed tools unavailable", "endpoint", p.opts.Endpoint, "error", err)
	}
	p.closeSession()
	p.apply(nil)
}

func (p *relayProxy) ensureSession(ctx context.Context) (*sdkmcp.ClientSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != nil {
		return p.session, nil
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "tap-mcp", Version: p.opts.Version}, &sdkmcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *sdkmcp.ToolListChangedRequest) {
			select {
			case p.kick <- struct{}{}:
			default:
			}
		},
	})
	base := p.opts.HTTPClient
	if base == nil {
		base = &http.Client{}
	}
	httpClient := *base
	httpClient.Transport = bearerTransport{base: base.Transport, token: p.opts.Token}
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{Endpoint: p.opts.Endpoint, HTTPClient: &httpClient}, nil)
	if err != nil {
		return nil, err
	}
	p.session = session
	return session, nil
}

func (p *relayProxy) closeSession() {
	p.mu.Lock()
	session := p.session
	p.session = nil
	p.mu.Unlock()
	if session != nil {
		_ = session.Close()
	}
}

func (p *relayProxy) current() *sdkmcp.ClientSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session
}

func listAllTools(ctx context.Context, session *sdkmcp.ClientSession) ([]*sdkmcp.Tool, error) {
	var out []*sdkmcp.Tool
	params := &sdkmcp.ListToolsParams{}
	for {
		page, err := session.ListTools(ctx, params)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Tools...)
		if page.NextCursor == "" {
			return out, nil
		}
		params = &sdkmcp.ListToolsParams{Cursor: page.NextCursor}
	}
}

// apply makes the server's relayed tools exactly listed. Only names with the
// relayed shape are taken, so a misbehaving endpoint cannot shadow a KEG tool.
func (p *relayProxy) apply(listed []*sdkmcp.Tool) {
	want := map[string]*sdkmcp.Tool{}
	for _, tool := range listed {
		if tool != nil && IsRelayedToolName(tool.Name) {
			want[tool.Name] = tool
		}
	}
	var gone []string
	for name := range p.tools {
		if _, ok := want[name]; !ok {
			gone = append(gone, name)
			delete(p.tools, name)
		}
	}
	if len(gone) > 0 {
		p.srv.RemoveTools(gone...)
	}
	for name, tool := range want {
		local := localRelayTool(tool)
		key, _ := json.Marshal(local)
		if p.tools[name] == string(key) {
			continue
		}
		p.tools[name] = string(key)
		p.srv.AddTool(local, p.handler(name))
	}
}

// localRelayTool is the tool as this server offers it. The input schema must
// be a JSON object schema or the SDK refuses the tool, so anything else
// becomes the open object schema; Hub's relay validates the real arguments.
// The output schema is dropped: results pass through as the server sent them.
func localRelayTool(remote *sdkmcp.Tool) *sdkmcp.Tool {
	local := *remote
	local.OutputSchema = nil
	var schema map[string]any
	if raw, err := json.Marshal(remote.InputSchema); err != nil || json.Unmarshal(raw, &schema) != nil || schema["type"] != "object" {
		local.InputSchema = map[string]any{"type": "object"}
	} else {
		local.InputSchema = schema
	}
	return &local
}

func (p *relayProxy) handler(name string) sdkmcp.ToolHandler {
	return func(ctx context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		session := p.current()
		if session == nil {
			return relayToolError("Hub is unreachable, so relayed tools cannot run right now."), nil
		}
		var args any
		if req != nil && req.Params != nil && len(req.Params.Arguments) > 0 {
			args = req.Params.Arguments
		}
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			select {
			case p.kick <- struct{}{}:
			default:
			}
			return relayToolError("The relayed tool call failed: " + err.Error()), nil
		}
		return result, nil
	}
}

func relayToolError(text string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{IsError: true, Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: text}}}
}

// bearerTransport adds the current Hub token to every request.
type bearerTransport struct {
	base  http.RoundTripper
	token func() string
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	if t.token != nil {
		if token := strings.TrimSpace(t.token()); token != "" {
			req = req.Clone(req.Context())
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	return base.RoundTrip(req)
}
