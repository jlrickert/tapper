package parity_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParity_ReadOperations runs table-driven parity tests for all read
// operations that produce output on both CLI and MCP surfaces.
//
// Command mapping (CLI -> MCP):
//
//	tap cat       -> cat         (both call Tap.Cat)
//	tap list      -> list        (both call Tap.List)
//	tap grep      -> grep        (both call Tap.Grep)
//	tap tags      -> tags        (both call Tap.Tags)
//	tap backlinks -> backlinks   (both call Tap.Backlinks)
//	tap links     -> links       (both call Tap.Links)
//	tap repo list -> list_kegs   (both call Tap.ListKegs; CLI joins with spaces, MCP with newlines)
//	tap config    -> info        (both call Tap.Info; MCP uses Minimal=true by default)
//	tap info      -> keg_info    (both call Tap.KegInfo)
//	tap stats     -> stats       (both call Tap.Stats)
//	tap dir       -> dir         (both call Tap.Dir)
func TestParity_ReadOperations(t *testing.T) {
	t.Parallel()

	cases := []ParityTestCase{
		// --- cat (Tap.Cat) ---
		{
			Name:    "cat/content_only",
			CLIArgs: []string{"cat", "0", "--content-only"},
			MCPTool: "cat",
			MCPInput: map[string]any{
				"node_ids":     []string{"0"},
				"content_only": true,
			},
		},
		{
			Name:    "cat/meta_only",
			CLIArgs: []string{"cat", "0", "--meta-only"},
			MCPTool: "cat",
			MCPInput: map[string]any{
				"node_ids":  []string{"0"},
				"meta_only": true,
			},
		},
		{
			Name:    "cat/default_with_frontmatter",
			CLIArgs: []string{"cat", "0"},
			MCPTool: "cat",
			MCPInput: map[string]any{
				"node_ids": []string{"0"},
			},
		},
		{
			Name:    "cat/multiple_nodes",
			CLIArgs: []string{"cat", "0", "1", "--content-only"},
			MCPTool: "cat",
			MCPInput: map[string]any{
				"node_ids":     []string{"0", "1"},
				"content_only": true,
			},
		},
		{
			Name:    "cat/stats_only",
			CLIArgs: []string{"cat", "0", "--stats-only"},
			MCPTool: "cat",
			MCPInput: map[string]any{
				"node_ids":   []string{"0"},
				"stats_only": true,
			},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				// Both should return stats fields. The access_count may differ
				// because CLI reads first (incrementing the count), then MCP
				// reads (seeing the incremented value). Verify structural parity
				// by checking that both contain the same core fields.
				for _, field := range []string{"title:", "hash:", "updated:", "created:"} {
					require.Contains(t, cliOut, field, "CLI stats should contain %s", field)
					require.Contains(t, mcpOut, field, "MCP stats should contain %s", field)
				}
			},
		},
		{
			Name:    "cat/nonexistent_node_errors",
			CLIArgs: []string{"cat", "999"},
			MCPTool: "cat",
			MCPInput: map[string]any{
				"node_ids": []string{"999"},
			},
			WantErr: true,
		},

		// --- list (Tap.List) ---
		// CLI defaults to --limit 0 (unlimited). MCP defaults to 50 when limit
		// is omitted (0); passing -1 requests unlimited. Both surfaces use
		// explicit unlimited here so output matches.
		{
			Name:    "list/id_only",
			CLIArgs: []string{"list", "--id-only"},
			MCPTool: "list",
			MCPInput: map[string]any{
				"id_only": true,
				"limit":   -1,
			},
		},
		{
			Name:    "list/id_only_reverse",
			CLIArgs: []string{"list", "--id-only", "--reverse"},
			MCPTool: "list",
			MCPInput: map[string]any{
				"id_only": true,
				"reverse": true,
				"limit":   -1,
			},
		},
		{
			Name:    "list/custom_format",
			CLIArgs: []string{"list", "-f", "%i %t"},
			MCPTool: "list",
			MCPInput: map[string]any{
				"format": "%i %t",
				"limit":  -1,
			},
		},

		// --- grep (Tap.Grep) ---
		{
			Name:    "grep/id_only",
			CLIArgs: []string{"grep", "Hello", "--id-only"},
			MCPTool: "grep",
			MCPInput: map[string]any{
				"query":   "Hello",
				"id_only": true,
				"limit":   -1,
			},
		},
		{
			Name:    "grep/default_format",
			CLIArgs: []string{"grep", "Hello"},
			MCPTool: "grep",
			MCPInput: map[string]any{
				"query": "Hello",
				"limit": -1,
			},
		},
		{
			Name:    "grep/no_match_returns_empty",
			CLIArgs: []string{"grep", "ZZZZNOTFOUND", "--id-only"},
			MCPTool: "grep",
			MCPInput: map[string]any{
				"query":   "ZZZZNOTFOUND",
				"id_only": true,
				"limit":   -1,
			},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				// Both should produce empty output for a non-matching pattern.
				require.Empty(t, strings.TrimSpace(cliOut), "CLI should return no results")
				require.Empty(t, strings.TrimSpace(mcpOut), "MCP should return no results")
			},
		},

		// --- tags (Tap.Tags) ---
		{
			Name:    "tags/list_all",
			CLIArgs: []string{"tags"},
			MCPTool: "tags",
			MCPInput: map[string]any{
				"limit": -1,
			},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				// Both should list the same tags; order may vary.
				require.ElementsMatch(t, normalizeLines(cliOut), normalizeLines(mcpOut),
					"tags should match.\nCLI:\n%s\n\nMCP:\n%s", cliOut, mcpOut)
			},
		},
		{
			Name:    "tags/filter_by_tag_id_only",
			CLIArgs: []string{"tags", "--query", "test", "--id-only"},
			MCPTool: "tags",
			MCPInput: map[string]any{
				"query":   "test",
				"id_only": true,
				"limit":   -1,
			},
		},

		// --- backlinks (Tap.Backlinks) ---
		{
			Name:    "backlinks/node_0_id_only",
			CLIArgs: []string{"backlinks", "0", "--id-only"},
			MCPTool: "backlinks",
			MCPInput: map[string]any{
				"node_id": "0",
				"id_only": true,
				"limit":   -1,
			},
		},

		// --- links (Tap.Links) ---
		{
			Name:    "links/node_1_id_only",
			CLIArgs: []string{"links", "1", "--id-only"},
			MCPTool: "links",
			MCPInput: map[string]any{
				"node_id": "1",
				"id_only": true,
				"limit":   -1,
			},
		},

		// --- list with offset (Tap.List) ---
		{
			Name:    "list/offset_skips_results",
			CLIArgs: []string{"list", "--id-only", "--offset", "1"},
			MCPTool: "list",
			MCPInput: map[string]any{
				"id_only": true,
				"offset":  1,
				"limit":   -1,
			},
		},

		// --- tags with offset (Tap.Tags) ---
		{
			Name:    "tags/offset_skips_tag_list",
			CLIArgs: []string{"tags", "--offset", "1"},
			MCPTool: "tags",
			MCPInput: map[string]any{
				"offset": 1,
				"limit":  -1,
			},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				// Fixture has 3 tags: hello, overview, test (sorted).
				// Offset 1 skips "hello", leaving "overview" and "test".
				require.ElementsMatch(t, normalizeLines(cliOut), normalizeLines(mcpOut),
					"tags with offset should match.\nCLI:\n%s\n\nMCP:\n%s", cliOut, mcpOut)
			},
		},
		{
			Name:    "tags/offset_with_filter",
			CLIArgs: []string{"tags", "--query", "test or overview", "--id-only", "--offset", "1"},
			MCPTool: "tags",
			MCPInput: map[string]any{
				"query":   "test or overview",
				"id_only": true,
				"offset":  1,
				"limit":   -1,
			},
		},

		// --- grep with offset (Tap.Grep) ---
		{
			Name:    "grep/offset_skips_results",
			CLIArgs: []string{"grep", "node", "--id-only", "--ignore-case", "--offset", "1"},
			MCPTool: "grep",
			MCPInput: map[string]any{
				"query":       "node",
				"id_only":     true,
				"ignore_case": true,
				"offset":      1,
				"limit":       -1,
			},
		},

		// --- backlinks with offset (Tap.Backlinks) ---
		{
			Name:    "backlinks/offset_skips_results",
			CLIArgs: []string{"backlinks", "0", "--id-only", "--offset", "1"},
			MCPTool: "backlinks",
			MCPInput: map[string]any{
				"node_id": "0",
				"id_only": true,
				"offset":  1,
				"limit":   -1,
			},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				// Fixture has 1 backlink to node 0 (from node 1). Offset 1
				// skips it, leaving empty output on both surfaces.
				require.Empty(t, strings.TrimSpace(cliOut), "CLI should return empty after offset")
				require.Empty(t, strings.TrimSpace(mcpOut), "MCP should return empty after offset")
			},
		},

		// --- links with offset (Tap.Links) ---
		{
			Name:    "links/offset_skips_results",
			CLIArgs: []string{"links", "1", "--id-only", "--offset", "1"},
			MCPTool: "links",
			MCPInput: map[string]any{
				"node_id": "1",
				"id_only": true,
				"offset":  1,
				"limit":   -1,
			},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				// Fixture has 1 link from node 1 (to node 0). Offset 1
				// skips it, leaving empty output on both surfaces.
				require.Empty(t, strings.TrimSpace(cliOut), "CLI should return empty after offset")
				require.Empty(t, strings.TrimSpace(mcpOut), "MCP should return empty after offset")
			},
		},

		// --- list_kegs (Tap.ListKegs) ---
		//
		// Known divergence: CLI joins aliases with spaces, MCP joins with
		// newlines. CLI passes cache=true, MCP passes cache=false. The
		// underlying aliases are the same.
		{
			Name:     "list_kegs/both_contain_personal",
			CLIArgs:  []string{"repo", "list"},
			MCPTool:  "list_kegs",
			MCPInput: map[string]any{},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				require.Contains(t, cliOut, "personal", "CLI should list personal keg")
				require.Contains(t, mcpOut, "personal", "MCP should list personal keg")
			},
		},

		// --- config/info (Tap.Info) ---
		//
		// CLI `tap config` and MCP `info` both call Tap.Info. However, MCP
		// uses Minimal=true by default, returning only core fields. This test
		// verifies both contain the keg title.
		{
			Name:     "info/both_contain_keg_title",
			CLIArgs:  []string{"config"},
			MCPTool:  "info",
			MCPInput: map[string]any{},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				require.Contains(t, cliOut, "Personal KEG", "CLI config should show keg title")
				require.Contains(t, mcpOut, "Personal KEG", "MCP info should show keg title")
			},
		},

		// --- keg_info (Tap.KegInfo) ---
		//
		// CLI `tap info` and MCP `keg_info` both call Tap.KegInfo.
		{
			Name:     "keg_info/both_show_diagnostics",
			CLIArgs:  []string{"info"},
			MCPTool:  "keg_info",
			MCPInput: map[string]any{},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				require.Contains(t, cliOut, "personal", "CLI info should show alias")
				require.Contains(t, mcpOut, "personal", "MCP keg_info should show alias")
				require.Contains(t, cliOut, "node_count", "CLI info should show node count")
				require.Contains(t, mcpOut, "node_count", "MCP keg_info should show node count")
			},
		},

		// --- stats (Tap.Stats) ---
		{
			Name:    "stats/node_0",
			CLIArgs: []string{"stats", "0"},
			MCPTool: "stats",
			MCPInput: map[string]any{
				"node_id": "0",
			},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				// Both call Tap.Stats which returns the same string.
				// Stats may or may not have content depending on fixture.
				// Just verify they produce the same result.
				require.Equal(t, strings.TrimSpace(cliOut), strings.TrimSpace(mcpOut),
					"stats output should match exactly")
			},
		},

		// --- dir (Tap.Dir) ---
		{
			Name:     "dir/keg_root",
			CLIArgs:  []string{"dir"},
			MCPTool:  "dir",
			MCPInput: map[string]any{},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				require.Contains(t, cliOut, "personal", "CLI dir should contain keg path")
				require.Contains(t, mcpOut, "personal", "MCP dir should contain keg path")
				require.Equal(t, strings.TrimSpace(cliOut), strings.TrimSpace(mcpOut),
					"dir paths should be identical")
			},
		},
		{
			Name:    "dir/node_path",
			CLIArgs: []string{"dir", "0"},
			MCPTool: "dir",
			MCPInput: map[string]any{
				"node_id": "0",
			},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				require.Equal(t, strings.TrimSpace(cliOut), strings.TrimSpace(mcpOut),
					"node dir paths should be identical")
				require.True(t, strings.HasSuffix(strings.TrimSpace(cliOut), "/0"),
					"path should end with /0")
			},
		},
	}

	runParityTests(t, cases)
}
