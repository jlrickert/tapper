package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/relaycontract"
)

// testMCPEnv switches the test binary into a stdio MCP server, so stdio
// tests run a real child process without anything installed.
const testMCPEnv = "TAP_RELAY_TEST_MCP"

func TestMain(m *testing.M) {
	if os.Getenv(testMCPEnv) != "" {
		runTestMCPServer()
		return
	}
	os.Exit(m.Run())
}

func runTestMCPServer() {
	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test", Version: "1"}, nil)
	addEchoTool(server)
	server.AddTool(&sdkmcp.Tool{Name: "env", InputSchema: objectSchema()}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return textResult(strings.Join(os.Environ(), "\n")), nil
	})
	server.AddTool(&sdkmcp.Tool{Name: "crash", InputSchema: objectSchema()}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		os.Exit(3)
		return nil, nil
	})
	_ = server.Run(context.Background(), &sdkmcp.StdioTransport{})
	os.Exit(0)
}

func objectSchema() map[string]any { return map[string]any{"type": "object"} }

func textResult(text string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: text}}}
}

func addEchoTool(server *sdkmcp.Server) {
	server.AddTool(&sdkmcp.Tool{
		Name:        "echo",
		Description: "Echoes its message.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string"}}},
		Annotations: &sdkmcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		var args struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(req.Params.Arguments, &args)
		return textResult(args.Message), nil
	})
}

// httpMCP is an in-process streamable HTTP MCP server.
type httpMCP struct {
	server  *sdkmcp.Server
	url     string
	headers chan http.Header
	started chan struct{}
	stopped chan struct{}
}

func newHTTPMCP(t *testing.T) *httpMCP {
	t.Helper()
	h := &httpMCP{
		server:  sdkmcp.NewServer(&sdkmcp.Implementation{Name: "http-test", Version: "1"}, nil),
		headers: make(chan http.Header, 64),
		started: make(chan struct{}, 4),
		stopped: make(chan struct{}, 4),
	}
	addEchoTool(h.server)
	destructive := true
	h.server.AddTool(&sdkmcp.Tool{Name: "sleep", InputSchema: objectSchema(), Annotations: &sdkmcp.ToolAnnotations{DestructiveHint: &destructive}},
		func(ctx context.Context, _ *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			h.started <- struct{}{}
			<-ctx.Done()
			h.stopped <- struct{}{}
			return nil, ctx.Err()
		})
	h.server.AddTool(&sdkmcp.Tool{Name: "hidden", InputSchema: objectSchema()}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return textResult("hidden"), nil
	})
	h.server.AddTool(&sdkmcp.Tool{Name: "bad name", InputSchema: objectSchema()}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return textResult("bad"), nil
	})
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return h.server }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case h.headers <- r.Header.Clone():
		default:
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	h.url = srv.URL
	return h
}

func newToolServer(t *testing.T, cfg ToolServerConfig) *ToolServer {
	t.Helper()
	ts, err := NewToolServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func stdioServer(t *testing.T, name string, pass ...string) *ToolServer {
	t.Helper()
	return newToolServer(t, ToolServerConfig{
		Name:    name,
		Command: os.Args[0],
		Args:    []string{"-test.run=^$"},
		Env:     ToolEnv(os.Environ(), false, pass, map[string]string{testMCPEnv: "1"}),
	})
}

func startToolRelay(t *testing.T, hub *fakeHub, servers []*ToolServer, mutate func(*Options)) chan error {
	t.Helper()
	_, done := startRelay(t, hub, nil, func(o *Options) {
		o.ToolServers = servers
		if mutate != nil {
			mutate(o)
		}
	})
	return done
}

// handshakeProtocol reads register and answers registered with protocol.
func handshakeProtocol(t *testing.T, ctx context.Context, c *websocket.Conn, protocol int) relaycontract.Register {
	t.Helper()
	env := readEnv(t, ctx, c)
	var reg relaycontract.Register
	if err := env.Decode(&reg); err != nil {
		t.Fatal(err)
	}
	writeEnv(t, ctx, c, relaycontract.TypeRegistered, "", relaycontract.Registered{Protocol: protocol, Models: []relaycontract.CatalogBinding{}})
	return reg
}

// waitTools reads tools frames until one satisfies ok.
func waitTools(t *testing.T, ctx context.Context, c *websocket.Conn, ok func(relaycontract.Tools) bool) relaycontract.Tools {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		env := readEnv(t, ctx, c)
		if env.Type != relaycontract.TypeTools {
			continue
		}
		var tools relaycontract.Tools
		if err := env.Decode(&tools); err != nil {
			t.Fatal(err)
		}
		if ok(tools) {
			return tools
		}
	}
	t.Fatal("no matching tools frame")
	return relaycontract.Tools{}
}

func hasServer(name string) func(relaycontract.Tools) bool {
	return func(x relaycontract.Tools) bool {
		return slices.ContainsFunc(x.Servers, func(s relaycontract.ToolServer) bool { return s.Name == name })
	}
}

func lacksServer(name string) func(relaycontract.Tools) bool {
	has := hasServer(name)
	return func(x relaycontract.Tools) bool { return !has(x) }
}

func toolNames(s relaycontract.ToolServer) []string {
	out := make([]string, 0, len(s.Tools))
	for _, tool := range s.Tools {
		out = append(out, tool.Name)
	}
	slices.Sort(out)
	return out
}

// callTool sends a call and returns the result, or the error frame.
func callTool(t *testing.T, ctx context.Context, c *websocket.Conn, id string, call relaycontract.Call) (*sdkmcp.CallToolResult, *relaycontract.Error) {
	t.Helper()
	if call.Arguments == nil {
		call.Arguments = json.RawMessage(`{}`)
	}
	writeEnv(t, ctx, c, relaycontract.TypeCall, id, call)
	var result *sdkmcp.CallToolResult
	for {
		env := readEnv(t, ctx, c)
		if env.ID != id {
			continue
		}
		switch env.Type {
		case relaycontract.TypeChunk:
			var chunk relaycontract.Chunk
			if err := env.Decode(&chunk); err != nil {
				t.Fatal(err)
			}
			result = &sdkmcp.CallToolResult{}
			if err := json.Unmarshal(chunk.Data, result); err != nil {
				t.Fatal(err)
			}
		case relaycontract.TypeDone:
			if result == nil {
				t.Fatal("done without a result chunk")
			}
			return result, nil
		case relaycontract.TypeError:
			var e relaycontract.Error
			if err := env.Decode(&e); err != nil {
				t.Fatal(err)
			}
			return nil, &e
		}
	}
}

func resultText(r *sdkmcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestToolsOnlyRelayRegisters(t *testing.T) {
	mcp := newHTTPMCP(t)
	hub := newFakeHub(t)
	startToolRelay(t, hub, []*ToolServer{newToolServer(t, ToolServerConfig{Name: "web", URL: mcp.url})}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	reg := handshakeProtocol(t, ctx, conn, 2)
	if reg.Limits.MaxConcurrent != 1 || len(reg.Models) != 0 {
		t.Fatalf("register = %+v, want limit 1 and no models", reg)
	}
}

func TestProtocol1HubNeverReceivesTools(t *testing.T) {
	mcp := newHTTPMCP(t)
	_, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, func(o *Options) {
		o.ToolServers = []*ToolServer{newToolServer(t, ToolServerConfig{Name: "web", URL: mcp.url})}
	})
	ctx := context.Background()
	conn := hub.accept(t)
	handshakeProtocol(t, ctx, conn, 1)
	// Give the server time to connect and publish; nothing may reach a
	// protocol 1 hub but the answer to its ping.
	time.Sleep(300 * time.Millisecond)
	writeEnv(t, ctx, conn, relaycontract.TypePing, "", nil)
	if env := readEnv(t, ctx, conn); env.Type != relaycontract.TypePong {
		t.Fatalf("frame = %q, want pong", env.Type)
	}
	writeEnv(t, ctx, conn, relaycontract.TypeCall, "c1", relaycontract.Call{Server: "web", Tool: "echo", Arguments: json.RawMessage(`{}`)})
	env := readEnv(t, ctx, conn)
	var e relaycontract.Error
	if env.Type != relaycontract.TypeError || env.Decode(&e) != nil || e.Code != relaycontract.CodeUnsupported {
		t.Fatalf("call on protocol 1 = %s %+v, want unsupported", env.Type, e)
	}
}

func TestHTTPToolServerCatalogAndCall(t *testing.T) {
	mcp := newHTTPMCP(t)
	t.Setenv("TEST_MCP_TOKEN", "sekrit")
	hub := newFakeHub(t)
	startToolRelay(t, hub, []*ToolServer{newToolServer(t, ToolServerConfig{
		Name:          "web",
		Title:         "Web tools",
		URL:           mcp.url,
		Headers:       map[string]string{"Authorization": "Bearer " + os.Getenv("TEST_MCP_TOKEN")},
		Deny:          []string{"hid*"},
		MaxConcurrent: 3,
	})}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshakeProtocol(t, ctx, conn, 2)
	tools := waitTools(t, ctx, conn, hasServer("web"))
	s := tools.Servers[0]
	if s.Title != "Web tools" || s.MaxConcurrent != 3 {
		t.Fatalf("server = %+v", s)
	}
	// hidden is denied; "bad name" is not a valid tool name and is dropped.
	if got := toolNames(s); !slices.Equal(got, []string{"echo", "sleep"}) {
		t.Fatalf("tools = %v, want [echo sleep]", got)
	}
	for _, tool := range s.Tools {
		switch tool.Name {
		case "echo":
			if a := tool.Annotations; a == nil || a.ReadOnlyHint == nil || !*a.ReadOnlyHint {
				t.Fatalf("echo annotations = %+v, want readOnly", a)
			}
		case "sleep":
			if a := tool.Annotations; a == nil || a.DestructiveHint == nil || !*a.DestructiveHint {
				t.Fatalf("sleep annotations = %+v, want destructive", a)
			}
		}
	}

	res, e := callTool(t, ctx, conn, "c1", relaycontract.Call{Server: "web", Tool: "echo", Arguments: json.RawMessage(`{"message":"hello"}`)})
	if e != nil {
		t.Fatalf("call failed: %+v", e)
	}
	if res.IsError || resultText(res) != "hello" {
		t.Fatalf("result = %+v", res)
	}
	sawAuth := false
	for len(mcp.headers) > 0 {
		if (<-mcp.headers).Get("Authorization") == "Bearer sekrit" {
			sawAuth = true
		}
	}
	if !sawAuth {
		t.Fatal("configured header never reached the MCP server")
	}
}

func TestToolCallErrors(t *testing.T) {
	mcp := newHTTPMCP(t)
	hub := newFakeHub(t)
	startToolRelay(t, hub, []*ToolServer{newToolServer(t, ToolServerConfig{Name: "web", URL: mcp.url, Deny: []string{"hidden"}})}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshakeProtocol(t, ctx, conn, 2)
	waitTools(t, ctx, conn, hasServer("web"))

	cases := []struct {
		name string
		call relaycontract.Call
		code string
	}{
		{"unknown server", relaycontract.Call{Server: "nope", Tool: "echo"}, relaycontract.CodeUnknownTool},
		{"unknown tool", relaycontract.Call{Server: "web", Tool: "nope"}, relaycontract.CodeUnknownTool},
		{"denied tool", relaycontract.Call{Server: "web", Tool: "hidden"}, relaycontract.CodeUnknownTool},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, e := callTool(t, ctx, conn, "e"+string(rune('0'+i)), tc.call)
			if e == nil || e.Code != tc.code {
				t.Fatalf("error = %+v, want %s", e, tc.code)
			}
		})
	}
	// Arguments must be an object; the strict decode refuses anything else.
	writeEnv(t, ctx, conn, relaycontract.TypeCall, "bad", map[string]any{"server": "web", "tool": "echo", "arguments": []int{1}})
	env := readEnv(t, ctx, conn)
	var e relaycontract.Error
	if env.Type != relaycontract.TypeError || env.Decode(&e) != nil || e.Code != relaycontract.CodeBadRequest {
		t.Fatalf("bad call = %s %+v, want bad_request", env.Type, e)
	}
}

func TestToolCallCancelReachesServer(t *testing.T) {
	mcp := newHTTPMCP(t)
	hub := newFakeHub(t)
	startToolRelay(t, hub, []*ToolServer{newToolServer(t, ToolServerConfig{Name: "web", URL: mcp.url})}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshakeProtocol(t, ctx, conn, 2)
	waitTools(t, ctx, conn, hasServer("web"))

	writeEnv(t, ctx, conn, relaycontract.TypeCall, "long", relaycontract.Call{Server: "web", Tool: "sleep", Arguments: json.RawMessage(`{}`)})
	select {
	case <-mcp.started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool never started")
	}
	writeEnv(t, ctx, conn, relaycontract.TypeCancel, "long", nil)
	env := readEnv(t, ctx, conn)
	var e relaycontract.Error
	if env.Type != relaycontract.TypeError || env.ID != "long" || env.Decode(&e) != nil || e.Code != relaycontract.CodeCancelled {
		t.Fatalf("frame = %s %+v, want cancelled", env.Type, e)
	}
	select {
	case <-mcp.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel never reached the MCP server")
	}
}

func TestToolCallTimeoutAndOverload(t *testing.T) {
	mcp := newHTTPMCP(t)
	hub := newFakeHub(t)
	startToolRelay(t, hub, []*ToolServer{newToolServer(t, ToolServerConfig{Name: "web", URL: mcp.url, MaxConcurrent: 1, Timeout: 500 * time.Millisecond})}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshakeProtocol(t, ctx, conn, 2)
	waitTools(t, ctx, conn, hasServer("web"))

	writeEnv(t, ctx, conn, relaycontract.TypeCall, "slow", relaycontract.Call{Server: "web", Tool: "sleep", Arguments: json.RawMessage(`{}`)})
	<-mcp.started
	// The one slot is taken: a second call is turned away at once.
	_, e := callTool(t, ctx, conn, "busy", relaycontract.Call{Server: "web", Tool: "echo"})
	if e == nil || e.Code != relaycontract.CodeOverloaded {
		t.Fatalf("second call = %+v, want overloaded", e)
	}
	for {
		env := readEnv(t, ctx, conn)
		if env.ID != "slow" {
			continue
		}
		var e relaycontract.Error
		if env.Type != relaycontract.TypeError || env.Decode(&e) != nil || e.Code != relaycontract.CodeTimeout {
			t.Fatalf("slow call = %s %+v, want timeout", env.Type, e)
		}
		break
	}
}

func TestToolListChangeIsResent(t *testing.T) {
	mcp := newHTTPMCP(t)
	hub := newFakeHub(t)
	startToolRelay(t, hub, []*ToolServer{newToolServer(t, ToolServerConfig{Name: "web", URL: mcp.url})}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshakeProtocol(t, ctx, conn, 2)
	waitTools(t, ctx, conn, hasServer("web"))

	mcp.server.AddTool(&sdkmcp.Tool{Name: "added", InputSchema: objectSchema()}, func(context.Context, *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return textResult("added"), nil
	})
	waitTools(t, ctx, conn, func(x relaycontract.Tools) bool {
		return len(x.Servers) == 1 && slices.Contains(toolNames(x.Servers[0]), "added")
	})
	mcp.server.RemoveTools("added")
	waitTools(t, ctx, conn, func(x relaycontract.Tools) bool {
		return len(x.Servers) == 1 && !slices.Contains(toolNames(x.Servers[0]), "added")
	})
}

func TestStdioToolServerScrubsEnvironment(t *testing.T) {
	t.Setenv("TAP_TEST_SECRET", "leaked")
	t.Setenv("TAP_TEST_PASSED", "passed")
	hub := newFakeHub(t)
	startToolRelay(t, hub, []*ToolServer{stdioServer(t, "local", "TAP_TEST_PASSED")}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshakeProtocol(t, ctx, conn, 2)
	tools := waitTools(t, ctx, conn, hasServer("local"))
	if got := toolNames(tools.Servers[0]); !slices.Equal(got, []string{"crash", "echo", "env"}) {
		t.Fatalf("tools = %v", got)
	}
	res, e := callTool(t, ctx, conn, "env", relaycontract.Call{Server: "local", Tool: "env"})
	if e != nil {
		t.Fatalf("env call failed: %+v", e)
	}
	env := resultText(res)
	if strings.Contains(env, "TAP_TEST_SECRET") {
		t.Fatal("an unlisted variable reached the MCP server")
	}
	if !strings.Contains(env, "TAP_TEST_PASSED=passed") || !strings.Contains(env, "PATH=") {
		t.Fatalf("server environment is missing passed or base variables:\n%s", env)
	}
}

func TestStdioToolServerRestartsAfterCrash(t *testing.T) {
	hub := newFakeHub(t)
	startToolRelay(t, hub, []*ToolServer{stdioServer(t, "local")}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshakeProtocol(t, ctx, conn, 2)
	waitTools(t, ctx, conn, hasServer("local"))

	writeEnv(t, ctx, conn, relaycontract.TypeCall, "boom", relaycontract.Call{Server: "local", Tool: "crash", Arguments: json.RawMessage(`{}`)})
	waitTools(t, ctx, conn, lacksServer("local"))
	waitTools(t, ctx, conn, hasServer("local"))
	res, e := callTool(t, ctx, conn, "again", relaycontract.Call{Server: "local", Tool: "echo", Arguments: json.RawMessage(`{"message":"back"}`)})
	if e != nil || resultText(res) != "back" {
		t.Fatalf("call after restart = %+v, %+v", res, e)
	}
}

func TestNewToolServerValidates(t *testing.T) {
	cases := map[string]ToolServerConfig{
		"bad name":        {Name: "a b", Command: "x"},
		"no endpoint":     {Name: "s"},
		"both endpoints":  {Name: "s", Command: "x", URL: "http://localhost"},
		"bad url":         {Name: "s", URL: "ftp://host"},
		"too concurrent":  {Name: "s", Command: "x", MaxConcurrent: relaycontract.MaxConcurrentCap + 1},
		"negative period": {Name: "s", Command: "x", Timeout: -time.Second},
	}
	for name, cfg := range cases {
		if _, err := NewToolServer(cfg); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	ts, err := NewToolServer(ToolServerConfig{Name: "s", Command: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if ts.cfg.MaxConcurrent != DefaultMaxConcurrent || ts.cfg.Timeout != DefaultToolTimeout {
		t.Fatalf("defaults = %d, %s", ts.cfg.MaxConcurrent, ts.cfg.Timeout)
	}
}

func TestNewClientRejectsEmptyAndDuplicateServers(t *testing.T) {
	hub := Hub{URL: "http://hub", Token: func(context.Context) (string, error) { return "", nil }}
	if _, err := NewClient(Options{Hubs: []Hub{hub}, Name: "laptop"}); err == nil {
		t.Fatal("a relay with nothing to offer was accepted")
	}
	a := newToolServer(t, ToolServerConfig{Name: "dup", Command: "x"})
	b := newToolServer(t, ToolServerConfig{Name: "dup", Command: "y"})
	if _, err := NewClient(Options{Hubs: []Hub{hub}, Name: "laptop", ToolServers: []*ToolServer{a, b}}); err == nil {
		t.Fatal("duplicate mcp servers were accepted")
	}
}

func TestToolEnv(t *testing.T) {
	environ := []string{"PATH=/bin", "HOME=/h", "LC_ALL=C", "SECRET=x", "PASS=y", "OVERRIDE=old"}
	got := ToolEnv(environ, false, []string{"PASS"}, map[string]string{"OVERRIDE": "new", "EXTRA": "e"})
	want := []string{"PATH=/bin", "HOME=/h", "LC_ALL=C", "PASS=y", "EXTRA=e", "OVERRIDE=new"}
	if !slices.Equal(got, want) {
		t.Fatalf("ToolEnv = %v, want %v", got, want)
	}
	all := ToolEnv(environ, true, nil, nil)
	if !slices.Equal(all, environ) {
		t.Fatalf("inherit = %v, want %v", all, environ)
	}
}
