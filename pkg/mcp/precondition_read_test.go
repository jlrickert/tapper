package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func readNodeHash(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, nodeID string) string {
	t.Helper()
	read, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "node_read",
		Arguments: map[string]any{"node_ids": []string{nodeID}},
	})
	require.NoError(t, err)
	require.False(t, read.IsError, "cat failed: %s", extractText(t, read))
	rows := structuredNodeRows(t, read)
	require.Len(t, rows, 1)
	return rows[0].Hash
}

// readNodePartHashes returns the per-part tokens node_edit takes.
func readNodePartHashes(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, nodeID string) (contentHash, metaHash string) {
	t.Helper()
	rows := catRows(t, session, ctx, map[string]any{"node_ids": []string{nodeID}})
	require.Len(t, rows, 1)
	require.NotEmpty(t, rows[0].ContentHash, "node_read must return content_hash")
	require.NotEmpty(t, rows[0].MetaHash, "node_read must return meta_hash")
	return rows[0].ContentHash, rows[0].MetaHash
}

// nodeReadText joins the documents of a node_read result's structured rows.
// node_read's text message is only a summary, so tests that check what a read
// returned look here.
func nodeReadText(t *testing.T, res *sdkmcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, res.StructuredContent, "node_read returned no structured content")
	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var out struct {
		Nodes []catRow `json:"nodes"`
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	var parts []string
	for _, row := range out.Nodes {
		for _, part := range []string{row.Meta, row.Content, row.Stats} {
			if part != "" {
				parts = append(parts, part)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func readSettingsHash(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, kegRef string) string {
	t.Helper()
	args := map[string]any{"minimal": false}
	if kegRef != "" {
		args["keg"] = kegRef
	}
	read, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_settings_read", Arguments: args})
	require.NoError(t, err)
	require.False(t, read.IsError, "keg_settings failed: %s", extractText(t, read))
	return structuredHash(t, read)
}

// structuredNodeRows decodes the per-node rows a read tool returns alongside
// its rendered text.
func structuredNodeRows(t *testing.T, res *sdkmcp.CallToolResult) []catRow {
	t.Helper()
	require.NotNil(t, res.StructuredContent, "read tool returned no structured content")
	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var payload struct {
		Nodes []catRow `json:"nodes"`
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
		Name:      "node_create",
		Arguments: batchCreateArgs(map[string]any{"title": "Precondition Subject"}),
	})
	require.NoError(t, err)
	require.False(t, createRes.IsError, "create failed: %s", extractText(t, createRes))
	nodeID := extractText(t, createRes)

	read, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "node_read",
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
		Name: "node_edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id": nodeID,
			"content": "# Missing token must fail\n",
		}),
	})
	require.NoError(t, err)
	require.True(t, missing.IsError)
	require.Contains(t, extractText(t, missing), "expected_content_hash")
	require.Equal(t, "PRECONDITION_REQUIRED", structuredMap(t, missing)["code"])
	require.Equal(t, false, structuredMap(t, missing)["operationPerformed"], "a missing part token must be rejected before the mutation")

	// The combined hash is not a part token: node_edit does not accept it.
	legacy, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":       nodeID,
			"content":       "# Legacy token must fail\n",
			"expected_hash": original,
		}),
	})
	require.NoError(t, err)
	require.True(t, legacy.IsError, "node_edit must reject expected_hash")

	originalContentHash := rows[0].ContentHash
	require.NotEmpty(t, originalContentHash)

	// The token a read handed out is accepted by the matching write.
	editRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":               nodeID,
			"content":               "# Precondition Subject\n\nFirst writer wins.\n",
			"expected_content_hash": originalContentHash,
		}),
	})
	require.NoError(t, err)
	require.False(t, editRes.IsError, "edit with a fresh hash must succeed: %s", extractText(t, editRes))

	// The write moved the node, so the old token is now stale.
	reread, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "node_read",
		Arguments: map[string]any{"node_ids": []string{nodeID}},
	})
	require.NoError(t, err)
	updated := structuredNodeRows(t, reread)[0].Hash
	require.NotEmpty(t, updated)
	require.NotEqual(t, original, updated, "a write must change the node's token")
	updatedContentHash, updatedMetaHash := readNodePartHashes(t, session, ctx, nodeID)
	require.NotEqual(t, originalContentHash, updatedContentHash)

	// A second agent still holding the pre-write token is refused rather than
	// silently clobbering the first writer's change.
	stale, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":               nodeID,
			"content":               "# Precondition Subject\n\nSecond writer clobbers.\n",
			"expected_content_hash": originalContentHash,
		}),
	})
	require.NoError(t, err)
	require.True(t, stale.IsError, "a stale hash must be refused, not applied")
	staleStructured := structuredMap(t, stale)
	require.Equal(t, "CONFLICT", staleStructured["code"])
	require.Equal(t, false, staleStructured["operationPerformed"])
	require.Equal(t, updated, staleStructured["currentHash"])
	require.Equal(t, updatedContentHash, staleStructured["currentContentHash"])
	require.Equal(t, updatedMetaHash, staleStructured["currentMetaHash"])
	require.Contains(t, staleStructured["currentContent"], "First writer wins.")
	require.NotEmpty(t, staleStructured["action"])

	// And the refused write left the first writer's content intact.
	final, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "node_read",
		Arguments: map[string]any{"node_ids": []string{nodeID}, "content_only": true},
	})
	require.NoError(t, err)
	require.Contains(t, nodeReadText(t, final), "First writer wins.")
}

func TestPrecondition_RemoveBatchUsesDistinctTokensAtomically(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	create := func(title string) string {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
			Name:      "node_create",
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
		Name: "node_delete",
		Arguments: map[string]any{"nodes": []map[string]any{
			{"node_id": one, "expected_hash": oneHash},
			{"node_id": two},
		}},
	})
	require.NoError(t, err)
	require.True(t, missing.IsError)
	require.Contains(t, extractText(t, missing), "expected_hash")
	require.Equal(t, "INVALID_ARGUMENT", structuredMap(t, missing)["code"])
	require.NotEmpty(t, readNodeHash(t, session, ctx, one))
	require.NotEmpty(t, readNodeHash(t, session, ctx, two))

	twoContentHash, _ := readNodePartHashes(t, session, ctx, two)
	edit, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":               two,
			"content":               "# Remove batch two\n\nchanged after the removal read\n",
			"expected_content_hash": twoContentHash,
		}),
	})
	require.NoError(t, err)
	require.False(t, edit.IsError, extractText(t, edit))
	currentTwoHash := readNodeHash(t, session, ctx, two)
	require.NotEqual(t, twoHash, currentTwoHash)

	stale, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_delete",
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
		Name: "node_delete",
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
			Name:      "node_read",
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
		Name:      "keg_settings_read",
		Arguments: map[string]any{"minimal": false},
	})
	require.NoError(t, err)
	require.False(t, settings.IsError, "keg_settings failed: %s", extractText(t, settings))
	require.NotEmpty(t, structuredHash(t, settings), "the full settings read must carry a token")

	// The minimal render is a cross-keg summary, not an editable document, so
	// it deliberately hands back nothing to echo.
	minimal, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "keg_settings_read",
		Arguments: map[string]any{"minimal": true},
	})
	require.NoError(t, err)
	require.False(t, minimal.IsError)
	require.NotContains(t, structuredMap(t, minimal), "hash", "the minimal summary must not offer a write token")
}

// TestCat_StructuredRowsAreSelfContained pins tapper#93: one cat row must carry
// the node's document AND the hash a write echoes back. They used to live in
// separate response surfaces — hash in structuredContent, document only in the
// rendered text — so an agent doing read-modify-write had to parse output meant
// for humans, and a multi-node read had to correlate two lists by position.
//
// Content and meta are asserted separately because that is the shape `node_edit`
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
	require.NotEmpty(t, metaRows[0].MetaHash, "meta_only must return meta_hash")
	require.Empty(t, metaRows[0].ContentHash, "meta_only read no content, so it has no content_hash")

	// content_only: the inverse.
	contentRows := catRows(t, session, ctx, map[string]any{"node_ids": []string{first}, "content_only": true})
	require.Len(t, contentRows, 1)
	require.Contains(t, contentRows[0].Content, "# Alpha")
	require.Empty(t, contentRows[0].Meta, "content_only must not return meta")
	require.NotEmpty(t, contentRows[0].ContentHash, "content_only must return content_hash")
	require.Empty(t, contentRows[0].MetaHash, "content_only returned no meta, so it has no meta_hash")

	// stats_only is explicit too.
	statsRows := catRows(t, session, ctx, map[string]any{"node_ids": []string{first}, "stats_only": true})
	require.Len(t, statsRows, 1)
	require.NotEmpty(t, statsRows[0].Stats, "stats_only must return stats")

	// The whole point: a structured row round-trips into edit with no parsing.
	editRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":            metaRows[0].NodeID,
			"meta":               metaRows[0].Meta,
			"expected_meta_hash": metaRows[0].MetaHash,
		}),
	})
	require.NoError(t, err)
	require.False(t, editRes.IsError, "structured meta did not round-trip: %s", extractText(t, editRes))
}

type catRow struct {
	NodeID      string `json:"node_id"`
	Hash        string `json:"hash"`
	ContentHash string `json:"content_hash"`
	MetaHash    string `json:"meta_hash"`
	Content     string `json:"content"`
	Meta        string `json:"meta"`
	Stats       string `json:"stats"`
}

func catRows(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, args map[string]any) []catRow {
	t.Helper()
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "node_read", Arguments: args})
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
		Name:      "node_create",
		Arguments: map[string]any{"nodes": []any{map[string]any{"key": "node", "content": content, "meta": meta}}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "create returned error: %s", extractText(t, res))
	return extractText(t, res)
}

// TestPrecondition_NodeEditPartHashesAreIndependent pins the split tokens: a
// metadata edit leaves content_hash alone, so a content edit holding the
// pre-meta-edit content_hash still applies, and vice versa.
func TestPrecondition_NodeEditPartHashesAreIndependent(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	nodeID := createNodeForTest(t, session, ctx, "# Split\n\nOriginal body.\n", "tags:\n  - original\n")
	before := catRows(t, session, ctx, map[string]any{"node_ids": []string{nodeID}})[0]
	require.NotEmpty(t, before.ContentHash)
	require.NotEmpty(t, before.MetaHash)
	require.NotEmpty(t, before.Hash)

	// A meta-only edit changes meta_hash and the combined hash, not content_hash.
	metaEdit, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":            nodeID,
			"meta":               "tags:\n  - concurrent\n",
			"expected_meta_hash": before.MetaHash,
		}),
	})
	require.NoError(t, err)
	require.False(t, metaEdit.IsError, "meta edit failed: %s", extractText(t, metaEdit))
	metaResult := editResultRow(t, metaEdit)
	require.Equal(t, before.ContentHash, metaResult["content_hash"], "a meta edit must not change content_hash")
	require.NotEqual(t, before.MetaHash, metaResult["meta_hash"])

	afterMeta := catRows(t, session, ctx, map[string]any{"node_ids": []string{nodeID}})[0]
	require.Equal(t, before.ContentHash, afterMeta.ContentHash, "a meta edit must not change content_hash")
	require.NotEqual(t, before.MetaHash, afterMeta.MetaHash)
	require.NotEqual(t, before.Hash, afterMeta.Hash, "the combined hash still covers both parts")
	require.Equal(t, afterMeta.MetaHash, metaResult["meta_hash"], "edit results return the same token a read would")

	// A content edit holding only the pre-meta-edit content_hash still applies.
	contentEdit, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":               nodeID,
			"content":               "# Split\n\nEdited body.\n",
			"expected_content_hash": before.ContentHash,
		}),
	})
	require.NoError(t, err)
	require.False(t, contentEdit.IsError, "content edit after a concurrent meta edit failed: %s", extractText(t, contentEdit))
	contentResult := editResultRow(t, contentEdit)
	require.Equal(t, afterMeta.MetaHash, contentResult["meta_hash"], "a content edit must not change meta_hash")

	// The returned content_hash chains into a follow-up edit with no re-read.
	followUp, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":               nodeID,
			"content":               "# Split\n\nSecond edit.\n",
			"expected_content_hash": contentResult["content_hash"],
		}),
	})
	require.NoError(t, err)
	require.False(t, followUp.IsError, "follow-up edit with returned content_hash failed: %s", extractText(t, followUp))

	final := catRows(t, session, ctx, map[string]any{"node_ids": []string{nodeID}})[0]
	require.Contains(t, final.Content, "Second edit.")
	require.Contains(t, final.Meta, "concurrent")
}

func TestPrecondition_NodeEditRequiresMatchingPartHashes(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	nodeID := createNodeForTest(t, session, ctx, "# Guarded\n\nBody.\n", "tags:\n  - guarded\n")
	contentHash, metaHash := readNodePartHashes(t, session, ctx, nodeID)

	// Meta supplied with only the content token: the meta part is unguarded.
	missingMeta, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":               nodeID,
			"content":               "# Guarded\n\nNew body.\n",
			"meta":                  "tags:\n  - changed\n",
			"expected_content_hash": contentHash,
		}),
	})
	require.NoError(t, err)
	require.True(t, missingMeta.IsError)
	require.Equal(t, "PRECONDITION_REQUIRED", structuredMap(t, missingMeta)["code"])
	require.Contains(t, extractText(t, missingMeta), "expected_meta_hash")

	// Meta supplied with no token at all.
	noToken, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "node_edit",
		Arguments: batchEditArgs(map[string]any{"node_id": nodeID, "meta": "tags:\n  - changed\n"}),
	})
	require.NoError(t, err)
	require.True(t, noToken.IsError)
	require.Equal(t, "PRECONDITION_REQUIRED", structuredMap(t, noToken)["code"])

	// Both refused writes left the node untouched.
	unchangedContent, unchangedMeta := readNodePartHashes(t, session, ctx, nodeID)
	require.Equal(t, contentHash, unchangedContent)
	require.Equal(t, metaHash, unchangedMeta)

	// Move the meta part on, then retry with the stale meta token.
	moved, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":            nodeID,
			"meta":               "tags:\n  - first\n",
			"expected_meta_hash": metaHash,
		}),
	})
	require.NoError(t, err)
	require.False(t, moved.IsError, extractText(t, moved))
	currentContent, currentMeta := readNodePartHashes(t, session, ctx, nodeID)

	stale, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":            nodeID,
			"meta":               "tags:\n  - second\n",
			"expected_meta_hash": metaHash,
		}),
	})
	require.NoError(t, err)
	require.True(t, stale.IsError)
	conflict := structuredMap(t, stale)
	require.Equal(t, "CONFLICT", conflict["code"])
	require.Equal(t, false, conflict["operationPerformed"])
	require.Equal(t, currentContent, conflict["currentContentHash"])
	require.Equal(t, currentMeta, conflict["currentMetaHash"])
	require.NotEmpty(t, conflict["currentHash"])
	require.Contains(t, conflict["currentContent"], "first")
}

// TestCat_TextIsSummaryWhenRowsCarryDocuments pins that node_read sends each
// document once: in nodes[], not again in the text message.
func TestCat_TextIsSummaryWhenRowsCarryDocuments(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	first := createNodeForTest(t, session, ctx, "# Once Alpha\n\nDistinctive alpha body.\n", "tags:\n  - onlyonce\n")
	second := createNodeForTest(t, session, ctx, "# Once Beta\n\nDistinctive beta body.\n", "")
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "node_read",
		Arguments: map[string]any{"node_ids": []string{first, second}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, extractText(t, res))

	message := extractText(t, res)
	require.Equal(t, "Read 2 nodes; each document is in nodes[]", message)
	var raw strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			raw.WriteString(tc.Text)
		}
	}
	require.Equal(t, 1, strings.Count(raw.String(), "Distinctive alpha body."), "the body must be sent exactly once")
	require.Equal(t, 1, strings.Count(raw.String(), "onlyonce"), "the meta must be sent exactly once")
	require.Contains(t, nodeReadText(t, res), "Distinctive beta body.")

	settings, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "keg_settings_read",
		Arguments: map[string]any{"minimal": false},
	})
	require.NoError(t, err)
	require.False(t, settings.IsError)
	require.Equal(t, "Settings are in data.", extractText(t, settings))
	require.Contains(t, structuredMap(t, settings)["data"], "kegv:")
}

func editResultRow(t *testing.T, res *sdkmcp.CallToolResult) map[string]any {
	t.Helper()
	results, ok := structuredMap(t, res)["results"].([]any)
	require.True(t, ok, "node_edit returned no results")
	require.Len(t, results, 1)
	row, ok := results[0].(map[string]any)
	require.True(t, ok)
	return row
}

func readContentHash(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, nodeID string) string {
	t.Helper()
	contentHash, _ := readNodePartHashes(t, session, ctx, nodeID)
	return contentHash
}

func readMetaHash(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, nodeID string) string {
	t.Helper()
	_, metaHash := readNodePartHashes(t, session, ctx, nodeID)
	return metaHash
}
