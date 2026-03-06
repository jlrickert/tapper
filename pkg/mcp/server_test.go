package mcp_test

import (
	"context"
	"embed"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
)

//go:embed all:data/**
var testdata embed.FS

func newTestSandbox(t *testing.T) *sandbox.Sandbox {
	t.Helper()
	return sandbox.NewSandbox(t,
		&sandbox.Options{
			Data: testdata,
			Home: "/home/testuser",
			User: "testuser",
		},
		sandbox.WithFixture("testuser", "~"),
	)
}

func newTestSession(t *testing.T) (*sdkmcp.ClientSession, context.Context) {
	t.Helper()
	ctx := context.Background()

	sb := newTestSandbox(t)
	rt := sb.Runtime()

	tap, err := tapper.NewTap(tapper.TapOptions{
		Runtime: rt,
	})
	require.NoError(t, err)

	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{})
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()

	// Connect server in background.
	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx, serverTransport)
	}()
	t.Cleanup(func() {
		<-done
	})

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "test-client",
		Version: "0.1",
	}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		session.Close()
	})

	return session, ctx
}

func TestMCP_ToolsList(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Tools)

	names := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		names[i] = tool.Name
	}

	require.Contains(t, names, "cat")
	require.Contains(t, names, "list")
	require.Contains(t, names, "grep")
	require.Contains(t, names, "tags")
	require.Contains(t, names, "backlinks")
	require.Contains(t, names, "links")
	require.Contains(t, names, "list_kegs")
	require.Contains(t, names, "info")
	require.Contains(t, names, "keg_info")
	require.Contains(t, names, "stats")
	require.Contains(t, names, "dir")
}

func TestMCP_Cat(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids": []string{"0"},
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "cat returned error: %s", text)
	require.Contains(t, text, "Personal Overview")
}

func TestMCP_CatContentOnly(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{"0"},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	text := extractText(t, res)
	require.Contains(t, text, "# Personal Overview")
}

func TestMCP_List(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "list",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "list returned error: %v", res.Content)
	text := extractText(t, res)
	require.Contains(t, text, "Personal Overview")
	require.Contains(t, text, "Hello World")
}

func TestMCP_ListIdOnly(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list",
		Arguments: map[string]any{
			"id_only": true,
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	text := extractText(t, res)
	require.Contains(t, text, "0")
	require.Contains(t, text, "1")
}

func TestMCP_Grep(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "grep",
		Arguments: map[string]any{
			"query": "Hello",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "grep returned error: %v", res.Content)
	text := extractText(t, res)
	require.Contains(t, text, "Hello World")
}

func TestMCP_Tags(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "tags",
		Arguments: map[string]any{
			"tag": "test",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "tags returned error: %v", res.Content)
	text := extractText(t, res)
	require.Contains(t, text, "Hello World")
}

func TestMCP_Backlinks(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "backlinks",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "backlinks returned error: %v", res.Content)
	text := extractText(t, res)
	require.Contains(t, text, "Hello World")
}

func TestMCP_Links(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "links",
		Arguments: map[string]any{
			"node_id": "1",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "links returned error: %v", res.Content)
	text := extractText(t, res)
	require.Contains(t, text, "Personal Overview")
}

func TestMCP_ListKegs(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "list_kegs",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "list_kegs returned error: %v", res.Content)
	text := extractText(t, res)
	require.Contains(t, text, "personal")
}

func TestMCP_Info(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "info",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "info returned error: %v", res.Content)
	text := extractText(t, res)
	require.Contains(t, text, "Personal KEG")
}

func TestMCP_Stats(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "stats",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	// stats may be empty if no stats.json exists, but should not error
	require.False(t, res.IsError, "stats returned error: %v", res.Content)
}

func TestMCP_CatError(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids": []string{"999"},
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error for missing node")
}

func extractText(t *testing.T, res *sdkmcp.CallToolResult) string {
	t.Helper()
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
