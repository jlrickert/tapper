package mcp_test

import (
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// TestMCP_EditRefusesProseAsSchema pins that a sentence passed as schema is
// refused by input validation, before the handler runs, with the code an
// agent reads as "fix the arguments".
func TestMCP_EditRefusesProseAsSchema(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	nodeID := createNodeForTest(t, session, ctx, "# Typed\n", "")
	_, metaHash := readNodePartHashes(t, session, ctx, nodeID)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":            nodeID,
			"meta":               "tags:\n  - typed\n",
			"expected_meta_hash": metaHash,
			"schema":             "a note about the quarterly planning meeting",
		}),
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	structured := structuredMap(t, res)
	require.Equal(t, "INVALID_ARGUMENT", structured["code"])
	require.Equal(t, false, structured["operationPerformed"])
	require.Contains(t, extractText(t, res), "schema")

	_, unchanged := readNodePartHashes(t, session, ctx, nodeID)
	require.Equal(t, metaHash, unchanged, "a refused edit wrote metadata")
}

// TestMCP_UnknownSchemaListsKegTypes pins that a well-formed but undefined
// schema names the types the keg does define, so the retry needs no
// schema_list round trip.
func TestMCP_UnknownSchemaListsKegTypes(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	for _, name := range []string{"decision", "task"} {
		created, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
			Name:      "schema_create",
			Arguments: map[string]any{"data": "type: " + name + "\n"},
		})
		require.NoError(t, err)
		require.False(t, created.IsError, "schema create failed: %s", extractText(t, created))
	}

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_create",
		Arguments: map[string]any{"nodes": []any{map[string]any{
			"key": "n", "schema": "meeting", "content": "# Meeting\n",
		}}},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Equal(t, "SCHEMA_INVALID", structuredMap(t, res)["code"])
	text := extractText(t, res)
	require.Contains(t, text, `unknown schema "meeting"`)
	require.True(t, strings.Contains(text, "this keg defines: decision, task"), text)
}

// TestMCP_RepeatedIdenticalFailureSaysStop pins the loop breaker: the third
// identical failing call keeps its code and gains repeat_count and a stop
// action; changed arguments or a success start the count again.
func TestMCP_RepeatedIdenticalFailureSaysStop(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	nodeID := createNodeForTest(t, session, ctx, "# Looping\n\nBody.\n", "")
	bad := batchEditArgs(map[string]any{"node_id": nodeID, "content": "# Looping\n\nNew body.\n"})
	call := func(args map[string]any) map[string]any {
		t.Helper()
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "node_edit", Arguments: args})
		require.NoError(t, err)
		if !res.IsError {
			return nil
		}
		return structuredMap(t, res)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		got := call(bad)
		require.Equal(t, "PRECONDITION_REQUIRED", got["code"])
		require.NotContains(t, got, "repeat_count", "attempt %d is an ordinary retry", attempt)
	}
	third := call(bad)
	require.Equal(t, "PRECONDITION_REQUIRED", third["code"], "the original code is kept")
	require.EqualValues(t, 3, third["repeat_count"])
	action, _ := third["action"].(string)
	require.Contains(t, action, "failed 3 times; stop retrying it")
	require.Contains(t, action, "node_read(node_id=", "the original guidance is kept after the stop instruction")
	require.EqualValues(t, 4, call(bad)["repeat_count"])

	// Different arguments are a different call: the count starts over.
	changed := batchEditArgs(map[string]any{"node_id": nodeID, "content": "# Looping\n\nOther body.\n"})
	require.NotContains(t, call(changed), "repeat_count")

	// Following the advice works, and the success clears the tool's count.
	contentHash, _ := readNodePartHashes(t, session, ctx, nodeID)
	require.Nil(t, call(batchEditArgs(map[string]any{
		"node_id": nodeID, "content": "# Looping\n\nNew body.\n", "expected_content_hash": contentHash,
	})), "the corrected call failed")
	require.NotContains(t, call(bad), "repeat_count")
}

// TestMCP_RepeatedInvalidArgumentsSaysStop covers failures the SDK raises
// before any handler runs: they are counted too, and keep INVALID_ARGUMENT.
func TestMCP_RepeatedInvalidArgumentsSaysStop(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	args := batchEditArgs(map[string]any{"node_id": "1", "meta": "tags: []\n", "schema": "a note about planning"})
	var last map[string]any
	for range repeatThreshold {
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "node_edit", Arguments: args})
		require.NoError(t, err)
		require.True(t, res.IsError)
		last = structuredMap(t, res)
	}
	require.Equal(t, "INVALID_ARGUMENT", last["code"])
	require.EqualValues(t, repeatThreshold, last["repeat_count"])
}

const repeatThreshold = 3
