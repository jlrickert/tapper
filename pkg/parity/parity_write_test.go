package parity_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParity_WriteOperations verifies that CLI and MCP write operations
// produce equivalent keg state. For write operations, we cannot directly
// compare stdout (CLI prints node ID, MCP prints status messages), so instead
// we perform the write via one surface and then verify the result using the
// other surface's read operations.
func TestParity_WriteOperations(t *testing.T) {
	t.Parallel()

	t.Run("create/both_surfaces_create_readable_nodes", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Create via CLI.
		cliOut, err := env.runCLICreate("CLI Node", "tags:\n  - parity-test\n")
		require.NoError(t, err, "CLI create should succeed")
		cliNodeID := strings.TrimSpace(cliOut)
		require.NotEmpty(t, cliNodeID, "CLI should return a node ID")

		// Create via MCP.
		mcpOut, err := env.runMCP("create", map[string]any{
			"nodes": []any{map[string]any{
				"key":     "node",
				"content": "# MCP Node\n",
				"meta":    "tags:\n  - parity-test\n",
			}},
		})
		require.NoError(t, err, "MCP create should succeed")
		mcpNodeID := strings.TrimSpace(mcpOut)
		require.NotEmpty(t, mcpNodeID, "MCP should return a node ID")

		// Verify CLI-created node is readable from MCP.
		mcpReadCLI, err := env.runMCP("cat", map[string]any{
			"node_ids":     []string{cliNodeID},
			"content_only": true,
		})
		require.NoError(t, err, "MCP should read CLI-created node")
		require.Contains(t, mcpReadCLI, "CLI Node", "MCP should see CLI node title")

		// Verify MCP-created node is readable from CLI.
		cliReadMCP, err := env.runCLI("cat", mcpNodeID, "--content-only")
		require.NoError(t, err, "CLI should read MCP-created node")
		require.Contains(t, cliReadMCP, "MCP Node", "CLI should see MCP node title")

		// Verify tags are set on both.
		cliMeta, err := env.runCLI("cat", cliNodeID, "--meta-only")
		require.NoError(t, err)
		require.Contains(t, cliMeta, "parity-test", "CLI-created node should have tag")

		mcpMeta, err := env.runMCP("cat", map[string]any{
			"node_ids":  []string{mcpNodeID},
			"meta_only": true,
		})
		require.NoError(t, err)
		require.Contains(t, mcpMeta, "parity-test", "MCP-created node should have tag")

		// Verify dex: tags index shows both nodes for "parity-test" from both surfaces.
		cliTags, err := env.runCLI("tags", "--query", "parity-test", "--id-only")
		require.NoError(t, err, "CLI tags query should succeed")
		require.Contains(t, cliTags, cliNodeID, "CLI tags should list CLI-created node")
		require.Contains(t, cliTags, mcpNodeID, "CLI tags should list MCP-created node")

		mcpTags, err := env.runMCP("tags", map[string]any{
			"query":   "parity-test",
			"id_only": true,
		})
		require.NoError(t, err, "MCP tags query should succeed")
		require.Contains(t, mcpTags, cliNodeID, "MCP tags should list CLI-created node")
		require.Contains(t, mcpTags, mcpNodeID, "MCP tags should list MCP-created node")
	})

	t.Run("create/both_surfaces_appear_in_list", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Create one node per surface.
		cliOut, err := env.runCLICreate("Listed CLI", "")
		require.NoError(t, err)
		cliID := strings.TrimSpace(cliOut)

		mcpOut, err := env.runMCP("create", map[string]any{
			"nodes": []any{map[string]any{"key": "node", "content": "# Listed MCP\n"}},
		})
		require.NoError(t, err)
		mcpID := strings.TrimSpace(mcpOut)

		// Both should appear in CLI list.
		cliList, err := env.runCLI("list", "--id-only", "-n", "0")
		require.NoError(t, err)
		require.True(t, containsLine(cliList, cliID), "CLI list should contain CLI-created node")
		require.True(t, containsLine(cliList, mcpID), "CLI list should contain MCP-created node")

		// Both should appear in MCP list.
		mcpList, err := env.runMCP("list", map[string]any{
			"id_only": true,
		})
		require.NoError(t, err)
		require.True(t, containsLine(mcpList, cliID), "MCP list should contain CLI-created node")
		require.True(t, containsLine(mcpList, mcpID), "MCP list should contain MCP-created node")
	})

	t.Run("remove/cli_remove_reflected_in_mcp", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Create a node.
		out, err := env.runMCP("create", map[string]any{
			"nodes": []any{map[string]any{"key": "node", "content": "# To Remove\n"}},
		})
		require.NoError(t, err)
		nodeID := strings.TrimSpace(out)

		// Remove via CLI.
		_, err = env.runCLI("rm", nodeID)
		require.NoError(t, err, "CLI rm should succeed")

		// MCP should not find it.
		_, err = env.runMCP("cat", map[string]any{
			"node_ids": []string{nodeID},
		})
		require.Error(t, err, "MCP should not find removed node")

		// Verify dex: list should NOT contain the removed node.
		// CLI creates a fresh Tap per invocation, so it re-reads the dex from disk.
		cliList, err := env.runCLI("list", "--id-only", "-n", "0")
		require.NoError(t, err)
		require.False(t, containsLine(cliList, nodeID), "CLI list should not contain removed node")
	})

	t.Run("remove/mcp_remove_reflected_in_cli", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Create a node via CLI.
		out, err := env.runCLICreate("MCP Will Remove", "")
		require.NoError(t, err)
		nodeID := strings.TrimSpace(out)

		// Remove via MCP.
		_, err = env.runMCP("remove", map[string]any{
			"nodes": []map[string]any{{
				"node_id":       nodeID,
				"expected_hash": env.nodeHash(nodeID),
			}},
		})
		require.NoError(t, err, "MCP remove should succeed")

		// CLI should not find it.
		_, err = env.runCLI("cat", nodeID, "--content-only")
		require.Error(t, err, "CLI should not find removed node")

		// Verify dex: list should NOT contain the removed node from both surfaces.
		cliList, err := env.runCLI("list", "--id-only", "-n", "0")
		require.NoError(t, err)
		require.False(t, containsLine(cliList, nodeID), "CLI list should not contain removed node")

		mcpList, err := env.runMCP("list", map[string]any{
			"id_only": true,
		})
		require.NoError(t, err)
		require.False(t, containsLine(mcpList, nodeID), "MCP list should not contain removed node")
	})

	t.Run("move/cli_move_reflected_in_mcp", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Create a node.
		out, err := env.runMCP("create", map[string]any{
			"nodes": []any{map[string]any{"key": "node", "content": "# Movable CLI\n"}},
		})
		require.NoError(t, err)
		srcID := strings.TrimSpace(out)

		// Move via CLI.
		_, err = env.runCLI("mv", srcID, "888")
		require.NoError(t, err, "CLI mv should succeed")

		// MCP should find it at new ID.
		mcpRead, err := env.runMCP("cat", map[string]any{
			"node_ids":     []string{"888"},
			"content_only": true,
		})
		require.NoError(t, err, "MCP should find node at new ID")
		require.Contains(t, mcpRead, "Movable CLI")

		// Old ID should be gone from MCP.
		_, err = env.runMCP("cat", map[string]any{
			"node_ids": []string{srcID},
		})
		require.Error(t, err, "MCP should not find node at old ID")

		// Verify dex: list should show "888" but NOT srcID.
		// CLI creates a fresh Tap per invocation, so it re-reads the dex from disk.
		cliList, err := env.runCLI("list", "--id-only", "-n", "0")
		require.NoError(t, err)
		require.True(t, containsLine(cliList, "888"), "CLI list should contain new ID")
		require.False(t, containsLine(cliList, srcID), "CLI list should not contain old ID")
	})

	t.Run("move/mcp_move_reflected_in_cli", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Create a node via CLI.
		out, err := env.runCLICreate("Movable MCP", "")
		require.NoError(t, err)
		srcID := strings.TrimSpace(out)

		// Move via MCP.
		_, err = env.runMCP("move", map[string]any{
			"source_id":     srcID,
			"dest_id":       "777",
			"expected_hash": env.nodeHash(srcID),
		})
		require.NoError(t, err, "MCP move should succeed")

		// CLI should find it at new ID.
		cliRead, err := env.runCLI("cat", "777", "--content-only")
		require.NoError(t, err, "CLI should find node at new ID")
		require.Contains(t, cliRead, "Movable MCP")

		// Old ID should be gone from CLI.
		_, err = env.runCLI("cat", srcID, "--content-only")
		require.Error(t, err, "CLI should not find node at old ID")

		// Verify dex: list should show "777" but NOT srcID from both surfaces.
		// MCP performed the move, so its in-memory dex is updated. CLI re-reads
		// from disk on each invocation.
		cliList, err := env.runCLI("list", "--id-only", "-n", "0")
		require.NoError(t, err)
		require.True(t, containsLine(cliList, "777"), "CLI list should contain new ID")
		require.False(t, containsLine(cliList, srcID), "CLI list should not contain old ID")

		mcpList, err := env.runMCP("list", map[string]any{
			"id_only": true,
		})
		require.NoError(t, err)
		require.True(t, containsLine(mcpList, "777"), "MCP list should contain new ID")
		require.False(t, containsLine(mcpList, srcID), "MCP list should not contain old ID")
	})

	// Note: No "cli_edit_reflected_in_mcp" test because CLI edit launches
	// an interactive editor (TTY). MCP edit accepts content directly.
	t.Run("edit/mcp_edit_reflected_in_cli", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Create a node.
		out, err := env.runMCP("create", map[string]any{
			"nodes": []any{map[string]any{"key": "node", "content": "# Before Edit\n"}},
		})
		require.NoError(t, err)
		nodeID := strings.TrimSpace(out)

		// Edit via MCP.
		_, err = env.runMCP("edit", map[string]any{
			"nodes": []any{map[string]any{
				"node_id":       nodeID,
				"content":       "# After MCP Edit\n\nEdited content.\n",
				"expected_hash": env.nodeHash(nodeID),
			}},
		})
		require.NoError(t, err, "MCP edit should succeed")

		// CLI should see the updated content.
		cliRead, err := env.runCLI("cat", nodeID, "--content-only")
		require.NoError(t, err)
		require.Contains(t, cliRead, "After MCP Edit")
		require.Contains(t, cliRead, "Edited content.")

		// Verify dex: list should show the new title.
		cliList, err := env.runCLI("list", "-f", "%i %t", "-n", "0")
		require.NoError(t, err)
		require.Contains(t, cliList, "After MCP Edit", "CLI list should show updated title")

		mcpList, err := env.runMCP("list", map[string]any{
			"format": "%i %t",
		})
		require.NoError(t, err)
		require.Contains(t, mcpList, "After MCP Edit", "MCP list should show updated title")
	})

	t.Run("meta/mcp_edit_meta_reflected_in_cli", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Create a node.
		out, err := env.runMCP("create", map[string]any{
			"nodes": []any{map[string]any{"key": "node", "content": "# Meta Test\n"}},
		})
		require.NoError(t, err)
		nodeID := strings.TrimSpace(out)

		// Write metadata via MCP.
		_, err = env.runMCP("edit", map[string]any{
			"nodes": []any{map[string]any{
				"node_id":       nodeID,
				"meta":          "tags:\n  - updated-meta\n  - parity\n",
				"expected_hash": env.nodeHash(nodeID),
			}},
		})
		require.NoError(t, err, "MCP metadata write should succeed")

		// CLI should see the updated metadata.
		cliMeta, err := env.runCLI("cat", nodeID, "--meta-only")
		require.NoError(t, err)
		require.Contains(t, cliMeta, "updated-meta")
		require.Contains(t, cliMeta, "parity")

		// Verify dex: tags index should show the node for "updated-meta".
		cliTags, err := env.runCLI("tags", "--query", "updated-meta", "--id-only")
		require.NoError(t, err)
		require.Contains(t, cliTags, nodeID, "CLI tags should list node with updated-meta tag")

		mcpTags, err := env.runMCP("tags", map[string]any{
			"query":   "updated-meta",
			"id_only": true,
		})
		require.NoError(t, err)
		require.Contains(t, mcpTags, nodeID, "MCP tags should list node with updated-meta tag")
	})

	// Regression for https://github.com/jlrickert/tapper/issues/29: writing
	// metadata that contains a synthetic `id:` field (e.g. piped from a
	// multi-node `cat --meta-only` stream) must not persist the `id:` into
	// meta.yaml. Otherwise successive cat → edit round-trips accumulate
	// duplicate `id:` lines.
	t.Run("meta/id_field_not_persisted", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		out, err := env.runMCP("create", map[string]any{
			"nodes": []any{map[string]any{"key": "node", "content": "# Id Strip Test\n"}},
		})
		require.NoError(t, err)
		nodeID := strings.TrimSpace(out)

		_, err = env.runMCP("edit", map[string]any{
			"nodes": []any{map[string]any{
				"node_id":       nodeID,
				"meta":          "id: \"" + nodeID + "\"\ntags:\n  - round-trip\n",
				"expected_hash": env.nodeHash(nodeID),
			}},
		})
		require.NoError(t, err, "MCP metadata write should succeed")

		first, err := env.runCLI("cat", nodeID, "--meta-only")
		require.NoError(t, err)
		require.NotContains(t, first, "id:", "id field must not be persisted to meta.yaml")
		require.Contains(t, first, "round-trip")

		_, err = env.runMCP("edit", map[string]any{
			"nodes": []any{map[string]any{
				"node_id":       nodeID,
				"meta":          first,
				"expected_hash": env.nodeHash(nodeID),
			}},
		})
		require.NoError(t, err, "second MCP metadata write should succeed")

		second, err := env.runCLI("cat", nodeID, "--meta-only")
		require.NoError(t, err)
		require.Equal(t, first, second, "round-trip cat → meta write → cat must be byte-identical")
	})

	t.Run("meta/cli_meta_and_mcp_cat_read_same_metadata", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Read metadata for node 0 via both surfaces.
		cliMeta, err := env.runCLI("meta", "0")
		require.NoError(t, err)

		mcpMeta, err := env.runMCP("cat", map[string]any{
			"node_ids":  []string{"0"},
			"meta_only": true,
		})
		require.NoError(t, err)

		// Both should return the same metadata.
		require.Equal(t, strings.TrimSpace(cliMeta), strings.TrimSpace(mcpMeta),
			"metadata from CLI and MCP should match")
	})
}
