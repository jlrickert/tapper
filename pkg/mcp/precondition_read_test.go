package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func readNodeHash(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, nodeID string) string {
	t.Helper()
	read, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "cat",
		Arguments: map[string]any{"node_ids": []string{nodeID}},
	})
	require.NoError(t, err)
	require.False(t, read.IsError, "cat failed: %s", extractText(t, read))
	rows := structuredNodeRows(t, read)
	require.Len(t, rows, 1)
	return rows[0].Hash
}

func readSettingsHash(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, kegRef string) string {
	t.Helper()
	args := map[string]any{"minimal": false}
	if kegRef != "" {
		args["keg"] = kegRef
	}
	read, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_settings", Arguments: args})
	require.NoError(t, err)
	require.False(t, read.IsError, "keg_settings failed: %s", extractText(t, read))
	return structuredHash(t, read)
}

// structuredNodeRows decodes the per-node rows a read tool returns alongside
// its rendered text.
func structuredNodeRows(t *testing.T, res *sdkmcp.CallToolResult) []struct {
	NodeID  string `json:"node_id"`
	Hash    string `json:"hash"`
	Content string `json:"content"`
} {
	t.Helper()
	require.NotNil(t, res.StructuredContent, "read tool returned no structured content")
	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var payload struct {
		Nodes []struct {
			NodeID  string `json:"node_id"`
			Hash    string `json:"hash"`
			Content string `json:"content"`
		} `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal(raw, &payload))
	return payload.Nodes
}

func structuredHash(t *testing.T, res *sdkmcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, res.StructuredContent, "read tool returned no structured content")
	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var payload struct {
		Hash string `json:"hash"`
	}
	require.NoError(t, json.Unmarshal(raw, &payload))
	return payload.Hash
}

// TestPrecondition_CatHashRoundTripsThroughEdit is the contract Phase 1 exists
// to establish: the token a read hands out is exactly the token the matching
// write accepts, and a token from before someone else's write is refused.
// Without this an agent has no way to obtain the precondition a write demands.
func TestPrecondition_CatHashRoundTripsThroughEdit(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "create",
		Arguments: batchCreateArgs(map[string]any{"title": "Precondition Subject"}),
	})
	require.NoError(t, err)
	require.False(t, createRes.IsError, "create failed: %s", extractText(t, createRes))
	nodeID := extractText(t, createRes)

	read, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "cat",
		Arguments: map[string]any{"node_ids": []string{nodeID}},
	})
	require.NoError(t, err)
	require.False(t, read.IsError, "cat failed: %s", extractText(t, read))

	rows := structuredNodeRows(t, read)
	require.Len(t, rows, 1)
	require.Equal(t, nodeID, rows[0].NodeID)
	original := rows[0].Hash
	require.NotEmpty(t, original, "cat must return a usable precondition token")

	missing, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id": nodeID,
			"content": "# Missing token must fail\n",
		}),
	})
	require.NoError(t, err)
	require.True(t, missing.IsError)
	require.Contains(t, extractText(t, missing), "expected_hash")
	require.Nil(t, missing.StructuredContent, "schema rejection must happen before the mutation handler")

	// The token a read handed out is accepted by the matching write.
	editRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":       nodeID,
			"content":       "# Precondition Subject\n\nFirst writer wins.\n",
			"expected_hash": original,
		}),
	})
	require.NoError(t, err)
	require.False(t, editRes.IsError, "edit with a fresh hash must succeed: %s", extractText(t, editRes))

	// The write moved the node, so the old token is now stale.
	reread, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "cat",
		Arguments: map[string]any{"node_ids": []string{nodeID}},
	})
	require.NoError(t, err)
	updated := structuredNodeRows(t, reread)[0].Hash
	require.NotEmpty(t, updated)
	require.NotEqual(t, original, updated, "a write must change the node's token")

	// A second agent still holding the pre-write token is refused rather than
	// silently clobbering the first writer's change.
	stale, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":       nodeID,
			"content":       "# Precondition Subject\n\nSecond writer clobbers.\n",
			"expected_hash": original,
		}),
	})
	require.NoError(t, err)
	require.True(t, stale.IsError, "a stale hash must be refused, not applied")
	staleStructured := structuredMap(t, stale)
	require.Equal(t, "CONFLICT", staleStructured["code"])
	require.Equal(t, false, staleStructured["operationPerformed"])
	require.Equal(t, updated, staleStructured["currentHash"])
	require.Contains(t, staleStructured["currentContent"], "First writer wins.")
	require.NotEmpty(t, staleStructured["action"])

	// And the refused write left the first writer's content intact.
	final, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "cat",
		Arguments: map[string]any{"node_ids": []string{nodeID}, "content_only": true},
	})
	require.NoError(t, err)
	require.Contains(t, extractText(t, final), "First writer wins.")
}

func TestPrecondition_RemoveBatchUsesDistinctTokensAtomically(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	create := func(title string) string {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
			Name:      "create",
			Arguments: batchCreateArgs(map[string]any{"title": title}),
		})
		require.NoError(t, err)
		require.False(t, result.IsError, extractText(t, result))
		return extractText(t, result)
	}
	one := create("Remove batch one")
	two := create("Remove batch two")
	oneHash := readNodeHash(t, session, ctx, one)
	twoHash := readNodeHash(t, session, ctx, two)
	require.NotEqual(t, oneHash, twoHash)

	missing, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "remove",
		Arguments: map[string]any{"nodes": []map[string]any{
			{"node_id": one, "expected_hash": oneHash},
			{"node_id": two},
		}},
	})
	require.NoError(t, err)
	require.True(t, missing.IsError)
	require.Contains(t, extractText(t, missing), "expected_hash")
	require.Nil(t, missing.StructuredContent)
	require.NotEmpty(t, readNodeHash(t, session, ctx, one))
	require.NotEmpty(t, readNodeHash(t, session, ctx, two))

	edit, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":       two,
			"content":       "# Remove batch two\n\nchanged after the removal read\n",
			"expected_hash": twoHash,
		}),
	})
	require.NoError(t, err)
	require.False(t, edit.IsError, extractText(t, edit))
	currentTwoHash := readNodeHash(t, session, ctx, two)
	require.NotEqual(t, twoHash, currentTwoHash)

	stale, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "remove",
		Arguments: map[string]any{"nodes": []map[string]any{
			{"node_id": one, "expected_hash": oneHash},
			{"node_id": two, "expected_hash": twoHash},
		}},
	})
	require.NoError(t, err)
	require.True(t, stale.IsError)
	conflict := structuredMap(t, stale)
	require.Equal(t, "CONFLICT", conflict["code"])
	require.Equal(t, false, conflict["operationPerformed"])
	require.Equal(t, currentTwoHash, conflict["currentHash"])
	require.NotEmpty(t, readNodeHash(t, session, ctx, one), "preflight conflict must not remove the first node")
	require.Equal(t, currentTwoHash, readNodeHash(t, session, ctx, two))

	valid, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "remove",
		Arguments: map[string]any{"nodes": []map[string]any{
			{"node_id": one, "expected_hash": oneHash},
			{"node_id": two, "expected_hash": currentTwoHash},
		}},
	})
	require.NoError(t, err)
	require.False(t, valid.IsError, extractText(t, valid))
	require.Contains(t, extractText(t, valid), "removed 2 node(s)")

	for _, nodeID := range []string{one, two} {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
			Name:      "cat",
			Arguments: map[string]any{"node_ids": []string{nodeID}},
		})
		require.NoError(t, err)
		require.True(t, result.IsError, "node %s survived a valid removal", nodeID)
	}
}

func structuredMap(t *testing.T, result *sdkmcp.CallToolResult) map[string]any {
	t.Helper()
	require.NotNil(t, result.StructuredContent)
	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

// TestPrecondition_ReadsExposeDocumentTokens covers the whole-document
// resources: schema definitions and keg settings each hand back the token
// their edit tool will require.
func TestPrecondition_ReadsExposeDocumentTokens(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	settings, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "keg_settings",
		Arguments: map[string]any{"minimal": false},
	})
	require.NoError(t, err)
	require.False(t, settings.IsError, "keg_settings failed: %s", extractText(t, settings))
	require.NotEmpty(t, structuredHash(t, settings), "the full settings read must carry a token")

	// The minimal render is a cross-keg summary, not an editable document, so
	// it deliberately hands back nothing to echo.
	minimal, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "keg_settings",
		Arguments: map[string]any{"minimal": true},
	})
	require.NoError(t, err)
	require.False(t, minimal.IsError)
	require.Nil(t, minimal.StructuredContent, "the minimal summary must not offer a write token")
}

// TestCat_StructuredRowsAreSelfContained pins tapper#93: one cat row must carry
// the node's document AND the hash a write echoes back. They used to live in
// separate response surfaces — hash in structuredContent, document only in the
// rendered text — so an agent doing read-modify-write had to parse output meant
// for humans, and a multi-node read had to correlate two lists by position.
//
// Content and meta are asserted separately because that is the shape `edit`
// accepts; a composed ---meta---body blob could not be sent back, since edit
// rejects frontmatter inside content.
func TestCat_StructuredRowsAreSelfContained(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	first := createNodeForTest(t, session, ctx, "# Alpha\n\nAlpha body.\n", "tags:\n  - alpha\n")
	second := createNodeForTest(t, session, ctx, "# Beta\n\nBeta body.\n", "tags:\n  - beta\n")

	rows := catRows(t, session, ctx, map[string]any{"node_ids": []string{first, second}})
	require.Len(t, rows, 2)
	require.Equal(t, []string{first, second}, []string{rows[0].NodeID, rows[1].NodeID},
		"rows must stay in request order so no positional correlation is needed")
	for i, row := range rows {
		require.NotEmpty(t, row.Hash, "row %d has no hash", i)
		require.Contains(t, row.Content, "body.", "row %d has no content", i)
		require.Contains(t, row.Meta, "tags:", "row %d has no meta", i)
	}
	require.Contains(t, rows[0].Content, "# Alpha")
	require.Contains(t, rows[1].Meta, "beta")

	// meta_only: metadata only, and the hash still works for a write.
	metaRows := catRows(t, session, ctx, map[string]any{"node_ids": []string{first}, "meta_only": true})
	require.Len(t, metaRows, 1)
	require.Contains(t, metaRows[0].Meta, "alpha")
	require.Empty(t, metaRows[0].Content, "meta_only must not return content")

	// content_only: the inverse.
	contentRows := catRows(t, session, ctx, map[string]any{"node_ids": []string{first}, "content_only": true})
	require.Len(t, contentRows, 1)
	require.Contains(t, contentRows[0].Content, "# Alpha")
	require.Empty(t, contentRows[0].Meta, "content_only must not return meta")

	// stats_only is explicit too.
	statsRows := catRows(t, session, ctx, map[string]any{"node_ids": []string{first}, "stats_only": true})
	require.Len(t, statsRows, 1)
	require.NotEmpty(t, statsRows[0].Stats, "stats_only must return stats")

	// The whole point: a structured row round-trips into edit with no parsing.
	editRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":       metaRows[0].NodeID,
			"meta":          metaRows[0].Meta,
			"expected_hash": metaRows[0].Hash,
		}),
	})
	require.NoError(t, err)
	require.False(t, editRes.IsError, "structured meta did not round-trip: %s", extractText(t, editRes))
}

type catRow struct {
	NodeID  string `json:"node_id"`
	Hash    string `json:"hash"`
	Content string `json:"content"`
	Meta    string `json:"meta"`
	Stats   string `json:"stats"`
}

func catRows(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, args map[string]any) []catRow {
	t.Helper()
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "cat", Arguments: args})
	require.NoError(t, err)
	require.False(t, res.IsError, "cat returned error: %s", extractText(t, res))
	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var out struct {
		Nodes []catRow `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	return out.Nodes
}

func createNodeForTest(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, content, meta string) string {
	t.Helper()
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "create",
		Arguments: map[string]any{"nodes": []any{map[string]any{"key": "node", "content": content, "meta": meta}}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "create returned error: %s", extractText(t, res))
	return extractText(t, res)
}
