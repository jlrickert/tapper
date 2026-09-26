package mcp_test

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/mcp"
)

func allowlistSession(t *testing.T, allow *mcp.ToolAllowlist) *sdkmcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	for _, name := range []string{"orient", "guide", "node_read", "node_edit"} {
		sdkmcp.AddTool(srv, &sdkmcp.Tool{Name: name}, func(context.Context, *sdkmcp.CallToolRequest, struct{}) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "ran " + name}}}, nil, nil
		})
	}
	srv.AddReceivingMiddleware(allow.Middleware)
	serverT, clientT := sdkmcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, serverT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "client", Version: "0"}, nil).Connect(ctx, clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *sdkmcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// An agent's session lists and runs only its tools; orient and guide stay.
func TestToolAllowlist_AgentTools(t *testing.T) {
	cs := allowlistSession(t, &mcp.ToolAllowlist{Agent: "@me/reader", Names: []string{"node_read"}})
	require.Equal(t, []string{"guide", "node_read", "orient"}, toolNames(t, cs))

	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "node_edit", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content[0].(*sdkmcp.TextContent).Text, "not one of agent @me/reader's tools")

	res, err = cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "node_read", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.False(t, res.IsError)
}

// An empty allowlist is every tool; a refused one is none but orient/guide.
func TestToolAllowlist_EmptyAndRefused(t *testing.T) {
	require.Equal(t, []string{"guide", "node_edit", "node_read", "orient"}, toolNames(t, allowlistSession(t, &mcp.ToolAllowlist{Agent: "@me/admin"})))

	cs := allowlistSession(t, &mcp.ToolAllowlist{Agent: "@me/gone", Refused: "agent @me/gone could not be loaded"})
	require.Equal(t, []string{"guide", "orient"}, toolNames(t, cs))
	res, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "node_read", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, res.Content[0].(*sdkmcp.TextContent).Text, "could not be loaded")
}

func TestToolAllowlist_RefreshesAndFailsClosed(t *testing.T) {
	var deny atomic.Bool
	var unavailable atomic.Bool
	cs := allowlistSession(t, &mcp.ToolAllowlist{Agent: "@me/reader", Resolve: func(context.Context) (*mcp.ToolAllowlist, error) {
		if unavailable.Load() {
			return nil, errors.New("agent deleted")
		}
		names := []string{"node_read"}
		if deny.Load() {
			names = []string{"guide"}
		}
		return &mcp.ToolAllowlist{Agent: "@me/reader", Names: names}, nil
	}})
	require.Contains(t, toolNames(t, cs), "node_read")
	deny.Store(true)
	result, err := cs.CallTool(context.Background(), &sdkmcp.CallToolParams{Name: "node_read", Arguments: map[string]any{}})
	require.NoError(t, err)
	require.True(t, result.IsError, "revoked tools stop working on the same connection")
	require.NotContains(t, toolNames(t, cs), "node_read")
	unavailable.Store(true)
	require.Equal(t, []string{"guide", "orient"}, toolNames(t, cs))
}
