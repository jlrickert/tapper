package mcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/mcp"
)

const relayEcho = "mcp__alice__everything__echo"

// fakeRelayHub serves a /mcp/relay stand-in with one relayed tool, recording
// the bearer token of each request.
func fakeRelayHub(t *testing.T) (*httptest.Server, *sdkmcp.Server, func() []string) {
	t.Helper()
	remote := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "hub", Version: "test"}, nil)
	remote.AddTool(&sdkmcp.Tool{
		Name:        relayEcho,
		Description: "Runs on your relay laptop.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"message": map[string]any{"type": "string"}}},
	}, func(_ context.Context, req *sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
		return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "echo: " + string(req.Params.Arguments)}}}, nil
	})
	var (
		mu    sync.Mutex
		auths []string
	)
	handler := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return remote }, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	return ts, remote, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(auths)
	}
}

// relayLocalSession is tap mcp's server, held to an agent whose tool list
// does not name the relayed tool, proxying endpoint.
func relayLocalSession(t *testing.T, endpoint string) (*sdkmcp.ClientSession, context.Context) {
	t.Helper()
	ctx := context.Background()
	sb := newTestSandbox(t)
	tap := newMemoryTap(t, ctx, sb.Runtime())
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{})
	allow := &mcp.ToolAllowlist{Agent: "@alice/agent", Names: []string{"node_read"}}
	srv.AddReceivingMiddleware(allow.Middleware)
	stop := mcp.StartRelayProxy(ctx, srv, mcp.RelayProxyOptions{
		Endpoint: endpoint,
		Token:    func() string { return "hub-token" },
		Refresh:  50 * time.Millisecond,
	})
	t.Cleanup(stop)
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	_, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "claude", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session, ctx
}

func relayToolNames(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession) []string {
	t.Helper()
	list, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	var out []string
	for _, tool := range list.Tools {
		out = append(out, tool.Name)
	}
	return out
}

func TestRelayProxy_ListsCallsAndDropsRelayedTools(t *testing.T) {
	t.Parallel()
	hub, remote, auths := fakeRelayHub(t)
	session, ctx := relayLocalSession(t, hub.URL)

	require.Eventually(t, func() bool { return slices.Contains(relayToolNames(t, ctx, session), relayEcho) }, 5*time.Second, 20*time.Millisecond,
		"the relayed tool is listed beside the KEG tools, past the agent's allowlist")
	require.Contains(t, relayToolNames(t, ctx, session), "node_read")
	require.Contains(t, auths(), "Bearer hub-token")

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: relayEcho, Arguments: map[string]any{"message": "hi"}})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	require.Equal(t, `echo: {"message":"hi"}`, res.Content[0].(*sdkmcp.TextContent).Text,
		"the relayed result passes through unwrapped")

	remote.RemoveTools(relayEcho)
	require.Eventually(t, func() bool { return !slices.Contains(relayToolNames(t, ctx, session), relayEcho) }, 5*time.Second, 20*time.Millisecond,
		"a tool Hub stops listing drops out")
}

func TestRelayProxy_UnreachableHubLeavesKEGToolsWorking(t *testing.T) {
	t.Parallel()
	hub, _, _ := fakeRelayHub(t)
	endpoint := hub.URL
	hub.Close()
	session, ctx := relayLocalSession(t, endpoint)
	time.Sleep(150 * time.Millisecond)
	names := relayToolNames(t, ctx, session)
	require.Contains(t, names, "node_read")
	require.NotContains(t, names, relayEcho)
}

func TestIsRelayedToolName(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"mcp__alice__everything__echo":       true,
		"mcp__acme__claude__claude_code_run": true,
		"node_read":                          false,
		"mcp__alice__echo":                   false,
	} {
		require.Equal(t, want, mcp.IsRelayedToolName(name), name)
	}
}

func TestToolAllowlist_AdmitsRelayedToolsUnlessRefused(t *testing.T) {
	t.Parallel()
	allow := &mcp.ToolAllowlist{Agent: "@alice/agent", Names: []string{"node_read"}}
	require.True(t, allow.Allows(relayEcho))
	require.False(t, allow.Allows("node_create"))
	refused := &mcp.ToolAllowlist{Agent: "@alice/agent", Refused: "agent could not be loaded"}
	require.False(t, refused.Allows(relayEcho), "a failed agent load fails closed for relayed tools too")
}
