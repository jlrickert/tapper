package mcp_test

import (
	"context"
	"embed"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/mylog"
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

func newTestSessionWithOpts(t *testing.T, opts ...mcp.ServerOptions) (*sdkmcp.ClientSession, context.Context) {
	t.Helper()
	ctx := context.Background()

	sb := newTestSandbox(t)
	rt := sb.Runtime()

	tap, err := tapper.NewTap(tapper.TapOptions{
		Runtime: rt,
	})
	require.NoError(t, err)

	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{}, opts...)
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
	require.Contains(t, names, "create")
	require.Contains(t, names, "edit")
	require.Contains(t, names, "meta")
	require.Contains(t, names, "remove")
	require.Contains(t, names, "move")
	require.Contains(t, names, "index")
	require.Contains(t, names, "list_indexes")
	require.Contains(t, names, "index_cat")
	require.Contains(t, names, "doctor")
	require.Contains(t, names, "node_history")
	require.Contains(t, names, "node_snapshot")
	require.Contains(t, names, "node_restore")
	require.Contains(t, names, "list_files")
	require.Contains(t, names, "list_images")
	require.Contains(t, names, "delete_file")
	require.Contains(t, names, "delete_image")
	require.Contains(t, names, "lock_acquire")
	require.Contains(t, names, "lock_release")
	require.Contains(t, names, "lock_status")
	require.Contains(t, names, "lock_force_release")
	require.Contains(t, names, "license")

	// New tools added in plan 440.
	require.Contains(t, names, "repo_init")
	require.Contains(t, names, "repo_rm")
	require.Contains(t, names, "config")
	require.Contains(t, names, "config_template")
	require.Contains(t, names, "import_from_keg")
	require.Contains(t, names, "export")
	require.Contains(t, names, "import")
	require.Contains(t, names, "upload_file")
	require.Contains(t, names, "download_file")
	require.Contains(t, names, "upload_image")
	require.Contains(t, names, "download_image")
	require.Contains(t, names, "graph")
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

func TestMCP_ListDefaultLimit(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Omitting limit should apply the MCP default (50). The test fixture
	// has only 2 nodes, so all are returned — but the important thing is
	// that the call succeeds with the default applied.
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

func TestMCP_ListUnlimitedWithNegativeOne(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Passing limit=-1 should request unlimited results (no cap).
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list",
		Arguments: map[string]any{
			"id_only": true,
			"limit":   -1,
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	text := extractText(t, res)
	require.Contains(t, text, "0")
	require.Contains(t, text, "1")
}

func TestMCP_ListExplicitLimit(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Passing limit=1 should cap at 1 result.
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list",
		Arguments: map[string]any{
			"id_only": true,
			"limit":   1,
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	text := extractText(t, res)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	require.Len(t, lines, 1, "limit=1 should return exactly 1 result")
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

// --- snapshot and file tool tests ---

func TestMCP_NodeHistory_Empty(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_history",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "node_history returned error: %s", text)
	require.Contains(t, text, "no snapshots")
}

func TestMCP_NodeSnapshotAndHistory(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Snapshot node 0.
	snapRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_snapshot",
		Arguments: map[string]any{
			"node_id": "0",
			"message": "initial snapshot",
		},
	})
	require.NoError(t, err)
	snapText := extractText(t, snapRes)
	require.False(t, snapRes.IsError, "node_snapshot returned error: %s", snapText)
	require.Contains(t, snapText, "snapshot rev")

	// Check history.
	histRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_history",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	histText := extractText(t, histRes)
	require.False(t, histRes.IsError, "node_history returned error: %s", histText)
	require.Contains(t, histText, "rev 1")
	require.Contains(t, histText, "initial snapshot")
}

func TestMCP_ListFiles_Empty(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list_files",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "list_files returned error: %s", text)
	require.Contains(t, text, "no files")
}

func TestMCP_ListImages_Empty(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list_images",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "list_images returned error: %s", text)
	require.Contains(t, text, "no images")
}

// --- index and diagnostics tool tests ---

func TestMCP_ListIndexes(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "list_indexes",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "list_indexes returned error: %s", text)
	require.Contains(t, text, "nodes.tsv")
	require.Contains(t, text, "tags")
}

func TestMCP_IndexCat(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "index_cat",
		Arguments: map[string]any{
			"name": "nodes.tsv",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "index_cat returned error: %s", text)
	require.Contains(t, text, "Personal Overview")
	require.Contains(t, text, "Hello World")
}

func TestMCP_IndexRebuild(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "index",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "index returned error: %s", text)
	require.Contains(t, text, "Indices rebuilt")
}

func TestMCP_Doctor(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "doctor",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "doctor returned error: %s", text)
	// The fixture may or may not have issues; just verify it returns something.
	require.NotEmpty(t, text)
}

// --- lock tool tests ---

func TestMCP_LockAcquireAndRelease(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Acquire lock on node 0.
	acquireRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_acquire",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	token := extractText(t, acquireRes)
	require.False(t, acquireRes.IsError, "lock_acquire returned error: %s", token)
	require.NotEmpty(t, token)

	// Status should show locked.
	statusRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_status",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	statusText := extractText(t, statusRes)
	require.False(t, statusRes.IsError)
	require.Contains(t, statusText, "locked")
	require.Contains(t, statusText, token)

	// Release with correct token.
	releaseRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_release",
		Arguments: map[string]any{
			"node_id": "0",
			"token":   token,
		},
	})
	require.NoError(t, err)
	require.False(t, releaseRes.IsError, "lock_release returned error: %s", extractText(t, releaseRes))

	// Status should show unlocked.
	statusRes2, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_status",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	require.Contains(t, extractText(t, statusRes2), "unlocked")
}

func TestMCP_LockForceRelease(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Acquire lock.
	acquireRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_acquire",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	require.False(t, acquireRes.IsError)

	// Force release without knowing the token.
	forceRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_force_release",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	require.False(t, forceRes.IsError, "lock_force_release returned error: %s", extractText(t, forceRes))

	// Status should show unlocked.
	statusRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_status",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	require.Contains(t, extractText(t, statusRes), "unlocked")
}

func TestMCP_LockReleaseTokenMismatch(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Acquire lock.
	acquireRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_acquire",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	require.False(t, acquireRes.IsError)

	// Release with wrong token should fail.
	releaseRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_release",
		Arguments: map[string]any{
			"node_id": "0",
			"token":   "wrong-token",
		},
	})
	require.NoError(t, err)
	require.True(t, releaseRes.IsError, "expected error for token mismatch")
	require.Contains(t, extractText(t, releaseRes), "mismatch")
}

// --- license tool tests ---

func TestMCP_License(t *testing.T) {
	t.Parallel()
	licenseText := "Apache License\nVersion 2.0, January 2004\nFull license content here."
	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{
		LicenseText: licenseText,
	})

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "license",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "license returned error: %s", text)
	require.Contains(t, text, "Apache License")
	require.Contains(t, text, "Version 2.0")
}

func TestMCP_License_Empty(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "license",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "license returned error: %s", text)
	require.Contains(t, text, "no license text available")
}

// --- repo and config tool tests ---

func TestMCP_ToolsList_IncludesNewTools(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	names := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		names[i] = tool.Name
	}

	require.Contains(t, names, "repo_init")
	require.Contains(t, names, "repo_rm")
	require.Contains(t, names, "config")
	require.Contains(t, names, "config_template")
}

func TestMCP_Config(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "config",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "config returned error: %s", text)
	require.Contains(t, text, "personal")
}

func TestMCP_ConfigUser(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "config",
		Arguments: map[string]any{
			"scope": "user",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "config user returned error: %s", text)
	require.Contains(t, text, "defaultKeg")
}

func TestMCP_ConfigInvalidScope(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "config",
		Arguments: map[string]any{
			"scope": "invalid",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error for invalid scope")
}

func TestMCP_ConfigTemplate(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "config_template",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "config_template returned error: %s", text)
	require.Contains(t, text, "fallbackKeg")
}

func TestMCP_ConfigTemplateProject(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "config_template",
		Arguments: map[string]any{
			"scope": "project",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "config_template project returned error: %s", text)
	require.Contains(t, text, "defaultKeg")
}

func TestMCP_RepoInit(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "repo_init",
		Arguments: map[string]any{
			"keg":   "newkeg",
			"user":  true,
			"title": "New Test KEG",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "repo_init returned error: %s", text)
	require.Contains(t, text, "initialized keg")
	require.Contains(t, text, "newkeg")
}

func TestMCP_RepoInitMissingAlias(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "repo_init",
		Arguments: map[string]any{
			"keg": "",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error for missing alias")
}

func TestMCP_RepoRm(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Init a new keg first so we can remove it.
	initRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "repo_init",
		Arguments: map[string]any{
			"keg":  "ephemeral",
			"user": true,
		},
	})
	require.NoError(t, err)
	require.False(t, initRes.IsError, "repo_init returned error: %s", extractText(t, initRes))

	// Remove it.
	rmRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "repo_rm",
		Arguments: map[string]any{
			"alias": "ephemeral",
		},
	})
	require.NoError(t, err)
	text := extractText(t, rmRes)
	require.False(t, rmRes.IsError, "repo_rm returned error: %s", text)
	require.Contains(t, text, "removed keg alias")
}

func TestMCP_RepoRmDefaultRequiresForce(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Trying to remove the default keg without force should fail.
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "repo_rm",
		Arguments: map[string]any{
			"alias": "personal",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error removing default keg without force")
}

// --- import tool tests ---

func TestMCP_ToolsList_IncludesImportTool(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	names := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		names[i] = tool.Name
	}

	require.Contains(t, names, "import_from_keg")
}

func TestMCP_ImportFromKeg(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// First, init a second keg to import from.
	initRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "repo_init",
		Arguments: map[string]any{
			"keg":   "source",
			"user":  true,
			"title": "Source KEG",
		},
	})
	require.NoError(t, err)
	require.False(t, initRes.IsError, "repo_init returned error: %s", extractText(t, initRes))

	// Create a node in the source keg.
	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: map[string]any{
			"title": "Imported Node",
			"lead":  "This node will be imported.",
			"keg":   "source",
		},
	})
	require.NoError(t, err)
	require.False(t, createRes.IsError, "create returned error: %s", extractText(t, createRes))
	srcNodeID := extractText(t, createRes)

	// Import from source into personal (default).
	importRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "import_from_keg",
		Arguments: map[string]any{
			"source_keg":     "source",
			"node_ids":       []string{srcNodeID},
			"target_keg":     "personal",
			"skip_zero_node": true,
		},
	})
	require.NoError(t, err)
	text := extractText(t, importRes)
	require.False(t, importRes.IsError, "import_from_keg returned error: %s", text)
	require.Contains(t, text, "imported 1 node(s)")
}

func TestMCP_ImportFromKegSameKegError(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "import_from_keg",
		Arguments: map[string]any{
			"source_keg": "personal",
			"target_keg": "personal",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error importing from same keg")
}

// --- file transfer tool tests ---

func TestMCP_ToolsList_IncludesFileTransferTools(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	names := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		names[i] = tool.Name
	}

	require.Contains(t, names, "upload_file")
	require.Contains(t, names, "download_file")
	require.Contains(t, names, "upload_image")
	require.Contains(t, names, "download_image")
}

func TestMCP_UploadAndDownloadFile(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Create a node to attach files to.
	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: map[string]any{
			"title": "File Test Node",
		},
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)

	// Upload a file (base64 of "hello world").
	content := "aGVsbG8gd29ybGQ=" // base64("hello world")
	uploadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "upload_file",
		Arguments: map[string]any{
			"node_id":        nodeID,
			"filename":       "test.txt",
			"content_base64": content,
		},
	})
	require.NoError(t, err)
	uploadText := extractText(t, uploadRes)
	require.False(t, uploadRes.IsError, "upload_file returned error: %s", uploadText)
	require.Contains(t, uploadText, "uploaded file")

	// List files to verify.
	listRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list_files",
		Arguments: map[string]any{
			"node_id": nodeID,
		},
	})
	require.NoError(t, err)
	require.Contains(t, extractText(t, listRes), "test.txt")

	// Download the file.
	downloadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "download_file",
		Arguments: map[string]any{
			"node_id":  nodeID,
			"filename": "test.txt",
		},
	})
	require.NoError(t, err)
	downloadText := extractText(t, downloadRes)
	require.False(t, downloadRes.IsError, "download_file returned error: %s", downloadText)
	require.Equal(t, content, downloadText)
}

func TestMCP_UploadAndDownloadImage(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Create a node to attach images to.
	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: map[string]any{
			"title": "Image Test Node",
		},
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)

	// Upload an image (base64 of "fake png data").
	content := "ZmFrZSBwbmcgZGF0YQ==" // base64("fake png data")
	uploadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "upload_image",
		Arguments: map[string]any{
			"node_id":        nodeID,
			"filename":       "test.png",
			"content_base64": content,
		},
	})
	require.NoError(t, err)
	uploadText := extractText(t, uploadRes)
	require.False(t, uploadRes.IsError, "upload_image returned error: %s", uploadText)
	require.Contains(t, uploadText, "uploaded image")

	// List images to verify.
	listRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list_images",
		Arguments: map[string]any{
			"node_id": nodeID,
		},
	})
	require.NoError(t, err)
	require.Contains(t, extractText(t, listRes), "test.png")

	// Download the image.
	downloadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "download_image",
		Arguments: map[string]any{
			"node_id":  nodeID,
			"filename": "test.png",
		},
	})
	require.NoError(t, err)
	downloadText := extractText(t, downloadRes)
	require.False(t, downloadRes.IsError, "download_image returned error: %s", downloadText)
	require.Equal(t, content, downloadText)
}

func TestMCP_UploadFileInvalidBase64(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "upload_file",
		Arguments: map[string]any{
			"node_id":        "0",
			"filename":       "test.txt",
			"content_base64": "not-valid-base64!!!",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error for invalid base64")
}

func TestMCP_DownloadFileNotFound(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "download_file",
		Arguments: map[string]any{
			"node_id":  "0",
			"filename": "nonexistent.txt",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error for missing file")
}

// --- archive tool tests ---

func TestMCP_ToolsList_IncludesArchiveTools(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	names := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		names[i] = tool.Name
	}

	require.Contains(t, names, "export")
	require.Contains(t, names, "import")
}

func TestMCP_ExportAndImport(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Export the default keg.
	exportRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "export",
		Arguments: map[string]any{
			"output_path": "~/export-test.tar.gz",
		},
	})
	require.NoError(t, err)
	text := extractText(t, exportRes)
	require.False(t, exportRes.IsError, "export returned error: %s", text)
	require.Contains(t, text, "exported to")

	// Create a second keg to import into.
	initRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "repo_init",
		Arguments: map[string]any{
			"keg":   "importtarget",
			"user":  true,
			"title": "Import Target",
		},
	})
	require.NoError(t, err)
	require.False(t, initRes.IsError, "repo_init returned error: %s", extractText(t, initRes))

	// Import the archive into the second keg.
	importRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "import",
		Arguments: map[string]any{
			"keg":  "importtarget",
			"path": "~/export-test.tar.gz",
		},
	})
	require.NoError(t, err)
	importText := extractText(t, importRes)
	require.False(t, importRes.IsError, "import returned error: %s", importText)
	require.Contains(t, importText, "imported")
	require.Contains(t, importText, "node(s)")
}

func TestMCP_ExportMissingPath(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "export",
		Arguments: map[string]any{
			"output_path": "",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error for empty output path")
}

func TestMCP_ImportMissingFile(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "import",
		Arguments: map[string]any{
			"path": "~/nonexistent-archive.tar.gz",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error for missing archive file")
}

// --- graph tool tests ---

func TestMCP_ToolsList_IncludesGraphTool(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	names := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		names[i] = tool.Name
	}

	require.Contains(t, names, "graph")
}

func TestMCP_Graph(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "graph",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "graph returned error: %s", text)
	require.Contains(t, text, "<!DOCTYPE html>")
	require.Contains(t, text, "KEG Graph")
	require.Contains(t, text, "__KEG__")
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

func TestMCP_ToolAnnotations_AllPresent(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Tools)

	// Every tool must have non-nil Annotations.
	for _, tool := range res.Tools {
		require.NotNilf(t, tool.Annotations, "tool %q is missing Annotations", tool.Name)
	}

	// Build a name->tool map for spot checks.
	byName := make(map[string]*sdkmcp.Tool, len(res.Tools))
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}

	// --- read-only tools ---
	readOnlyTools := []string{
		"cat", "list", "grep", "tags", "backlinks", "links",
		"list_kegs", "info", "keg_info", "stats", "dir",
		"list_files", "list_images", "download_file", "download_image",
		"list_indexes", "index_cat",
		"doctor", "lock_status", "license", "node_history",
	}
	for _, name := range readOnlyTools {
		tool, ok := byName[name]
		require.Truef(t, ok, "read-only tool %q not found", name)
		require.Truef(t, tool.Annotations.ReadOnlyHint, "tool %q should have ReadOnlyHint=true", name)
		require.NotNilf(t, tool.Annotations.OpenWorldHint, "tool %q should have OpenWorldHint set", name)
		require.Falsef(t, *tool.Annotations.OpenWorldHint, "tool %q should have OpenWorldHint=false", name)
	}

	// --- destructive tools ---
	destructiveTools := []string{
		"remove", "move", "node_restore",
		"delete_file", "delete_image",
		"repo_rm", "lock_force_release",
	}
	for _, name := range destructiveTools {
		tool, ok := byName[name]
		require.Truef(t, ok, "destructive tool %q not found", name)
		require.NotNilf(t, tool.Annotations.DestructiveHint, "tool %q should have DestructiveHint set", name)
		require.Truef(t, *tool.Annotations.DestructiveHint, "tool %q should have DestructiveHint=true", name)
	}

	// --- write non-destructive tools ---
	writeTools := []string{
		"create", "edit", "meta",
		"node_snapshot",
		"upload_file", "upload_image",
		"lock_acquire", "lock_release",
		"repo_init", "config", "config_template",
		"export", "import", "import_from_keg",
		"site", "serve", "graph",
	}
	for _, name := range writeTools {
		tool, ok := byName[name]
		require.Truef(t, ok, "write tool %q not found", name)
		require.NotNilf(t, tool.Annotations.DestructiveHint, "tool %q should have DestructiveHint set", name)
		require.Falsef(t, *tool.Annotations.DestructiveHint, "tool %q should have DestructiveHint=false", name)
	}

	// --- idempotent tool ---
	indexTool, ok := byName["index"]
	require.True(t, ok, "index tool not found")
	require.NotNil(t, indexTool.Annotations.DestructiveHint, "index should have DestructiveHint set")
	require.False(t, *indexTool.Annotations.DestructiveHint, "index should have DestructiveHint=false")
	require.True(t, indexTool.Annotations.IdempotentHint, "index should have IdempotentHint=true")
	require.NotNil(t, indexTool.Annotations.OpenWorldHint, "index should have OpenWorldHint set")
	require.False(t, *indexTool.Annotations.OpenWorldHint, "index should have OpenWorldHint=false")
}

func TestMCP_InvocationLogging(t *testing.T) {
	t.Parallel()

	_, th := mylog.NewTestLogger(t, slog.LevelDebug)
	lg := slog.New(th)

	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{
		Logger: lg,
	})

	// Call a known tool to trigger the middleware.
	_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list_kegs",
	})
	require.NoError(t, err)

	// The middleware logs asynchronously from the server goroutine, so
	// use RequireEntry with a short timeout.
	entry := mylog.RequireEntry(t, th, func(e mylog.LoggedEntry) bool {
		return e.Msg == "invocation"
	}, 2*time.Second)

	require.Equal(t, slog.LevelInfo, entry.Level)
	require.Equal(t, "mcp", entry.Attrs["surface"])
	require.Equal(t, "list_kegs", entry.Attrs["tool"])
	require.Equal(t, true, entry.Attrs["success"])

	// duration_ms should be present and non-negative. Sandbox tests use a
	// frozen clock, so the value may be 0.
	durationRaw, hasDuration := entry.Attrs["duration_ms"]
	require.True(t, hasDuration, "log entry should include duration_ms")
	durationMs, ok := durationRaw.(int64)
	require.True(t, ok, "duration_ms should be an int64")
	require.GreaterOrEqual(t, durationMs, int64(0), "duration_ms should be non-negative")

	// Client metadata from the test client.
	require.Equal(t, "test-client", entry.Attrs["client.name"])
	require.Equal(t, "0.1", entry.Attrs["client.version"])
}

func TestMCP_InvocationLogging_ToolError(t *testing.T) {
	t.Parallel()

	_, th := mylog.NewTestLogger(t, slog.LevelDebug)
	lg := slog.New(th)

	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{
		Logger: lg,
	})

	// Call a tool that will return an error result (nonexistent node).
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "cat",
		Arguments: map[string]any{"node_ids": []string{"99999"}},
	})
	require.NoError(t, err) // RPC itself succeeds; the tool returns IsError.
	require.True(t, res.IsError, "tool should return an error result")

	entry := mylog.RequireEntry(t, th, func(e mylog.LoggedEntry) bool {
		return e.Msg == "invocation" && e.Attrs["tool"] == "cat"
	}, 2*time.Second)

	require.Equal(t, false, entry.Attrs["success"],
		"invocation log should reflect tool-level failure")
	require.Equal(t, "cat", entry.Attrs["tool"])
}

func TestMCP_InvocationLogging_WithKegAlias(t *testing.T) {
	t.Parallel()

	_, th := mylog.NewTestLogger(t, slog.LevelDebug)
	lg := slog.New(th)

	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{
		Logger: lg,
	})

	// Call a tool with an explicit keg alias in arguments.
	_, _ = session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "list",
		Arguments: map[string]any{"keg": "personal"},
	})

	entry := mylog.RequireEntry(t, th, func(e mylog.LoggedEntry) bool {
		return e.Msg == "invocation" && e.Attrs["tool"] == "list"
	}, 2*time.Second)

	require.Equal(t, "personal", entry.Attrs["keg"],
		"invocation log should include keg alias from tool arguments")
}
