package mcp_test

import (
	"encoding/json"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// pageLines returns a listing call's structured lines.
func pageLines(t *testing.T, res *sdkmcp.CallToolResult) []string {
	t.Helper()
	require.Falsef(t, res.IsError, "call failed: %s", extractText(t, res))
	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var page struct {
		Lines []string `json:"lines"`
	}
	require.NoError(t, json.Unmarshal(raw, &page))
	return page.Lines
}

// id_only left the listing schemas because format "%i" prints the same
// lines. A client written against the old schema still sends it, and must get
// those lines rather than a validation failure.
func TestMCP_IDOnlyIsFormatForOldClients(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	listed, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	for _, tool := range listed.Tools {
		_, advertised := schemaProperties(t, tool.InputSchema)["id_only"]
		require.Falsef(t, advertised, "%s still advertises id_only", tool.Name)
	}

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"node_list", map[string]any{}},
		{"node_list", map[string]any{"reverse": true}},
		{"node_grep", map[string]any{"query": "Hel+o"}},
		{"tag_list", map[string]any{"query": "test"}},
		{"node_backlinks", map[string]any{"node_ids": []string{"0"}}},
		{"node_links", map[string]any{"node_ids": []string{"1"}}},
	}
	call := func(tool string, base map[string]any, extra map[string]any) []string {
		args := map[string]any{}
		for k, v := range base {
			args[k] = v
		}
		for k, v := range extra {
			args[k] = v
		}
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: tool, Arguments: args})
		require.NoError(t, err)
		return pageLines(t, res)
	}
	for _, tc := range cases {
		want := call(tc.tool, tc.args, map[string]any{"format": "%i"})
		require.NotEmptyf(t, want, "%s %v listed nothing; the case proves nothing", tc.tool, tc.args)
		require.Equalf(t, want, call(tc.tool, tc.args, map[string]any{"id_only": true}),
			"%s: id_only must print what format %%i prints", tc.tool)
		// As before, id_only wins over a format sent with it.
		require.Equalf(t, want, call(tc.tool, tc.args, map[string]any{"id_only": true, "format": "%t"}),
			"%s: id_only must override format", tc.tool)
		// id_only false asks for nothing, so the default rendering applies.
		require.Equalf(t, call(tc.tool, tc.args, nil), call(tc.tool, tc.args, map[string]any{"id_only": false}),
			"%s: id_only false must change nothing", tc.tool)
	}
}
