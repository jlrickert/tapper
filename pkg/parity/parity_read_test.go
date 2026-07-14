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
//	tap config    -> info        (both call Tap.Info; MCP uses Minimal=true by default)
//	tap info      -> keg_info    (both call Tap.KegInfo)
//	tap stats     -> stats       (both call Tap.Stats)
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
		// --- cat with a keg:<alias>/<id> ref (Tap.Cat via resolveNodeArg) ---
		//
		// The fixture registers the current keg under the "personal" alias, so
		// "keg:personal/0" resolves that alias through the tap-config kegs map
		// back to the same keg. Both surfaces must route the prefixed ref through
		// the shared Tap.resolveNodeArg choke point and read node 0's content,
		// identical to passing a bare "0".
		{
			Name:    "cat/alias_ref_resolves_same_keg",
			CLIArgs: []string{"cat", "keg:personal/0", "--content-only"},
			MCPTool: "cat",
			MCPInput: map[string]any{
				"node_ids":     []string{"keg:personal/0"},
				"content_only": true,
			},
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

		// --- list with sort (Tap.List) ---
		{
			Name:    "list/sort_updated",
			CLIArgs: []string{"list", "--id-only", "--sort", "updated"},
			MCPTool: "list",
			MCPInput: map[string]any{
				"id_only": true,
				"sort":    "updated",
				"limit":   -1,
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
				"node_ids": []string{"0"},
				"id_only":  true,
				"limit":    -1,
			},
		},

		// --- links (Tap.Links) ---
		{
			Name:    "links/node_1_id_only",
			CLIArgs: []string{"links", "1", "--id-only"},
			MCPTool: "links",
			MCPInput: map[string]any{
				"node_ids": []string{"1"},
				"id_only":  true,
				"limit":    -1,
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
				"node_ids": []string{"0"},
				"id_only":  true,
				"offset":   1,
				"limit":    -1,
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
				"node_ids": []string{"1"},
				"id_only":  true,
				"offset":   1,
				"limit":    -1,
			},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				// Fixture has 1 link from node 1 (to node 0). Offset 1
				// skips it, leaving empty output on both surfaces.
				require.Empty(t, strings.TrimSpace(cliOut), "CLI should return empty after offset")
				require.Empty(t, strings.TrimSpace(mcpOut), "MCP should return empty after offset")
			},
		},

		// --- settings/info (Tap.Info) ---
		//
		// CLI `tap keg settings` and MCP `info` both call Tap.Info. However, MCP
		// uses Minimal=true by default, returning only core fields. This test
		// verifies both contain the keg title.
		{
			Name:     "info/both_contain_keg_title",
			CLIArgs:  []string{"keg", "settings"},
			MCPTool:  "info",
			MCPInput: map[string]any{},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				require.Contains(t, cliOut, "Personal KEG", "CLI settings should show keg title")
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
				for _, want := range []string{"hub: home", "namespace: local", "keg: personal"} {
					require.Contains(t, cliOut, want, "CLI info should show resolved identity")
					require.Contains(t, mcpOut, want, "MCP keg_info should show resolved identity")
				}
				require.Contains(t, cliOut, "personal", "CLI info should show alias")
				require.Contains(t, mcpOut, "personal", "MCP keg_info should show alias")
				require.Contains(t, cliOut, "node_count", "CLI info should show node count")
				require.Contains(t, mcpOut, "node_count", "MCP keg_info should show node count")
				require.NotContains(t, mcpOut, "working_directory")
				require.NotContains(t, mcpOut, "keg_directory")
				require.NotContains(t, mcpOut, "resolution_source")
				require.NotContains(t, mcpOut, "scope")
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

		// --- multi-ID backlinks (Tap.Backlinks) ---
		//
		// The testuser fixture has node 1 linking to node 0. Querying
		// backlinks for both nodes at once should produce the same merged
		// result on both surfaces.
		{
			Name:    "backlinks/multi_id_merged",
			CLIArgs: []string{"backlinks", "0", "1", "--id-only"},
			MCPTool: "backlinks",
			MCPInput: map[string]any{
				"node_ids": []string{"0", "1"},
				"id_only":  true,
				"limit":    -1,
			},
		},

		// --- multi-ID links (Tap.Links) ---
		{
			Name:    "links/multi_id_merged",
			CLIArgs: []string{"links", "0", "1", "--id-only"},
			MCPTool: "links",
			MCPInput: map[string]any{
				"node_ids": []string{"0", "1"},
				"id_only":  true,
				"limit":    -1,
			},
		},

		// --- grep max_lines (Tap.Grep) ---
		//
		// CLI with --max-lines 3 and MCP with default max_lines (0, which
		// resolves to 3 for MCP) should produce the same output.
		{
			Name:    "grep/max_lines_default_parity",
			CLIArgs: []string{"grep", "node", "--max-lines", "3", "--ignore-case"},
			MCPTool: "grep",
			MCPInput: map[string]any{
				"query":       "node",
				"ignore_case": true,
				"limit":       -1,
			},
		},
	}

	runParityTests(t, cases)
}
