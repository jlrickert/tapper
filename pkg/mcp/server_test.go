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

// --- write tool tests ---

func TestMCP_Create(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: map[string]any{
			"title": "New Node",
			"lead":  "A node created via MCP.",
			"tags":  []string{"mcp-test"},
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "create returned error: %s", text)
	require.NotEmpty(t, text)

	// Read it back.
	readRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{text},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	readText := extractText(t, readRes)
	require.False(t, readRes.IsError, "cat returned error: %s", readText)
	require.Contains(t, readText, "# New Node")
	require.Contains(t, readText, "A node created via MCP.")
}

func TestMCP_CreateWithBody(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	body := "# Custom Title\n\nCustom body content.\n"
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: map[string]any{
			"body": body,
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "create returned error: %s", text)

	readRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{text},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	readText := extractText(t, readRes)
	require.Contains(t, readText, "# Custom Title")
	require.Contains(t, readText, "Custom body content.")
}

func TestMCP_Edit(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Create a node first.
	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: map[string]any{
			"title": "Before Edit",
		},
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)
	require.False(t, createRes.IsError)

	// Edit it.
	editRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "edit",
		Arguments: map[string]any{
			"node_id": nodeID,
			"content": "# After Edit\n\nEdited via MCP.\n",
		},
	})
	require.NoError(t, err)
	require.False(t, editRes.IsError, "edit returned error: %s", extractText(t, editRes))

	// Read back.
	readRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{nodeID},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	readText := extractText(t, readRes)
	require.Contains(t, readText, "# After Edit")
	require.Contains(t, readText, "Edited via MCP.")
}

func TestMCP_MetaRead(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Node 0 has tags: [overview]
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "meta",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "meta returned error: %s", text)
	require.Contains(t, text, "overview")
}

func TestMCP_MetaWrite(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Create a node.
	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: map[string]any{
			"title": "Meta Test",
		},
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)

	// Write new metadata.
	writeRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "meta",
		Arguments: map[string]any{
			"node_id": nodeID,
			"content": "tags:\n  - updated\n  - mcp\n",
		},
	})
	require.NoError(t, err)
	require.False(t, writeRes.IsError, "meta write returned error: %s", extractText(t, writeRes))

	// Read back.
	readRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "meta",
		Arguments: map[string]any{
			"node_id": nodeID,
		},
	})
	require.NoError(t, err)
	readText := extractText(t, readRes)
	require.Contains(t, readText, "updated")
	require.Contains(t, readText, "mcp")
}

func TestMCP_Remove(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Create a node.
	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: map[string]any{
			"title": "To Be Removed",
		},
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)

	// Remove it.
	removeRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "remove",
		Arguments: map[string]any{
			"node_ids": []string{nodeID},
		},
	})
	require.NoError(t, err)
	require.False(t, removeRes.IsError, "remove returned error: %s", extractText(t, removeRes))

	// Confirm it's gone.
	catRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids": []string{nodeID},
		},
	})
	require.NoError(t, err)
	require.True(t, catRes.IsError, "expected error reading removed node")
}

func TestMCP_Move(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Create a node.
	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: map[string]any{
			"title": "Movable Node",
		},
	})
	require.NoError(t, err)
	srcID := extractText(t, createRes)

	// Move it to ID 999.
	moveRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "move",
		Arguments: map[string]any{
			"source_id": srcID,
			"dest_id":   "999",
		},
	})
	require.NoError(t, err)
	require.False(t, moveRes.IsError, "move returned error: %s", extractText(t, moveRes))

	// Old ID is gone.
	oldRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids": []string{srcID},
		},
	})
	require.NoError(t, err)
	require.True(t, oldRes.IsError, "expected error reading old node ID")

	// New ID exists.
	newRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{"999"},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	newText := extractText(t, newRes)
	require.False(t, newRes.IsError, "cat returned error: %s", newText)
	require.Contains(t, newText, "Movable Node")
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
