package parity_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParity_IndexOperations verifies that index and diagnostic operations
// produce equivalent results across CLI and MCP surfaces.
func TestParity_IndexOperations(t *testing.T) {
	t.Parallel()

	cases := []ParityTestCase{
		// --- index rebuild ---
		{
			Name:     "index/rebuild_succeeds_on_both",
			CLIArgs:  []string{"index", "rebuild"},
			MCPTool:  "index",
			MCPInput: map[string]any{},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				// Both should indicate success. Messages differ but both should
				// contain "rebuilt" or similar.
				require.Contains(t, strings.ToLower(cliOut), "rebuilt",
					"CLI index rebuild should indicate success")
				require.Contains(t, strings.ToLower(mcpOut), "rebuilt",
					"MCP index rebuild should indicate success")
			},
		},

		// --- list_indexes ---
		{
			Name:     "list_indexes/both_show_index_files",
			CLIArgs:  []string{"index", "list"},
			MCPTool:  "list_indexes",
			MCPInput: map[string]any{},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				require.Contains(t, cliOut, "nodes.tsv", "CLI should list nodes.tsv")
				require.Contains(t, mcpOut, "nodes.tsv", "MCP should list nodes.tsv")
				require.Contains(t, cliOut, "tags", "CLI should list tags index")
				require.Contains(t, mcpOut, "tags", "MCP should list tags index")
			},
		},

		// --- index_cat ---
		// CLI uses `index get`, MCP uses `index_cat`.
		{
			Name:    "index_cat/nodes_tsv",
			CLIArgs: []string{"index", "get", "nodes.tsv"},
			MCPTool: "index_cat",
			MCPInput: map[string]any{
				"name": "nodes.tsv",
			},
		},

		// --- doctor ---
		{
			Name:     "doctor/both_succeed",
			CLIArgs:  []string{"doctor"},
			MCPTool:  "doctor",
			MCPInput: map[string]any{},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				// Both should produce output (diagnostics may vary in wording).
				require.NotEmpty(t, cliOut, "CLI doctor should produce output")
				require.NotEmpty(t, mcpOut, "MCP doctor should produce output")
			},
		},
	}

	runParityTests(t, cases)
}

// TestParity_SnapshotOperations verifies snapshot operations work across surfaces.
func TestParity_SnapshotOperations(t *testing.T) {
	t.Parallel()

	t.Run("snapshot/cli_snapshot_visible_in_mcp_history", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Snapshot node 0 via CLI.
		_, err := env.runCLI("snapshot", "create", "0", "--message", "cli snapshot")
		require.NoError(t, err, "CLI snapshot should succeed")

		// Check history via MCP.
		mcpHistory, err := env.runMCP("node_history", map[string]any{
			"node_id": "0",
		})
		require.NoError(t, err, "MCP node_history should succeed")
		require.Contains(t, mcpHistory, "cli snapshot",
			"MCP should see CLI-created snapshot in history")
	})

	t.Run("snapshot/mcp_snapshot_visible_in_cli_history", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Snapshot node 0 via MCP.
		_, err := env.runMCP("node_snapshot", map[string]any{
			"node_id": "0",
			"message": "mcp snapshot",
		})
		require.NoError(t, err, "MCP snapshot should succeed")

		// Check history via CLI.
		cliHistory, err := env.runCLI("snapshot", "history", "0")
		require.NoError(t, err, "CLI snapshot list should succeed")
		require.Contains(t, cliHistory, "mcp snapshot",
			"CLI should see MCP-created snapshot in history")
	})

	// Known divergence: CLI `snapshot history` prints a header row even when
	// empty; MCP `node_history` returns "no snapshots". Both correctly
	// represent the empty state.
	t.Run("snapshot/empty_history_no_revisions", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		cliHistory, err := env.runCLI("snapshot", "history", "0")
		require.NoError(t, err)

		mcpHistory, err := env.runMCP("node_history", map[string]any{
			"node_id": "0",
		})
		require.NoError(t, err)

		// CLI shows table header but no data rows; MCP says "no snapshots".
		// Verify neither contains a "rev 1" or "rev 2" etc.
		require.NotContains(t, cliHistory, "rev 1",
			"CLI should have no revision entries")
		require.Contains(t, strings.ToLower(mcpHistory), "no snapshot",
			"MCP should indicate no snapshots")
	})
}

// TestParity_LockOperations verifies lock operations work across surfaces.
// Note: CLI uses --lock/--unlock flags on commands, while MCP has explicit
// lock_acquire/lock_release tools. This tests the MCP lock tools and verifies
// their state is observable.
func TestParity_LockOperations(t *testing.T) {
	t.Parallel()

	t.Run("lock/mcp_acquire_status_release", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Acquire via MCP.
		token, err := env.runMCP("lock_acquire", map[string]any{
			"node_id": "0",
		})
		require.NoError(t, err, "MCP lock_acquire should succeed")
		require.NotEmpty(t, token)

		// Status via MCP.
		status, err := env.runMCP("lock_status", map[string]any{
			"node_id": "0",
		})
		require.NoError(t, err, "MCP lock_status should succeed")
		require.Contains(t, status, "locked")
		require.Contains(t, status, token)

		// Release via MCP.
		_, err = env.runMCP("lock_release", map[string]any{
			"node_id": "0",
			"token":   token,
		})
		require.NoError(t, err, "MCP lock_release should succeed")

		// Status should show unlocked.
		status2, err := env.runMCP("lock_status", map[string]any{
			"node_id": "0",
		})
		require.NoError(t, err)
		require.Contains(t, status2, "unlocked")
	})

	// Known divergence: CLI `lock status` prints a table (Token, Holder,
	// Acquired, TTL) when locked, while MCP prefixes with "locked\n". Both
	// show the token. When unlocked, both print "unlocked".
	t.Run("lock/cli_lock_status_matches_mcp", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Acquire via MCP.
		token, err := env.runMCP("lock_acquire", map[string]any{
			"node_id": "0",
		})
		require.NoError(t, err)
		require.NotEmpty(t, token)

		// Check status via CLI -- shows Token: <token> (tabwriter).
		cliStatus, err := env.runCLI("lock", "status", "0")
		require.NoError(t, err, "CLI lock status should succeed")
		require.Contains(t, cliStatus, token,
			"CLI should show the token acquired by MCP")

		// Check status via MCP -- shows "locked" + token.
		mcpStatus, err := env.runMCP("lock_status", map[string]any{
			"node_id": "0",
		})
		require.NoError(t, err, "MCP lock_status should succeed")
		require.Contains(t, mcpStatus, token,
			"MCP should show the same token")

		// Release via MCP.
		_, err = env.runMCP("lock_release", map[string]any{
			"node_id": "0",
			"token":   token,
		})
		require.NoError(t, err)

		// Both should now show unlocked.
		cliStatus2, err := env.runCLI("lock", "status", "0")
		require.NoError(t, err)
		require.Contains(t, cliStatus2, "unlocked")

		mcpStatus2, err := env.runMCP("lock_status", map[string]any{
			"node_id": "0",
		})
		require.NoError(t, err)
		require.Contains(t, mcpStatus2, "unlocked")
	})

	t.Run("lock/cli_acquire_release_cycle", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Acquire via CLI.
		cliToken, err := env.runCLI("lock", "acquire", "0")
		require.NoError(t, err, "CLI lock acquire should succeed")
		require.NotEmpty(t, cliToken)

		// Verify locked via MCP.
		status, err := env.runMCP("lock_status", map[string]any{"node_id": "0"})
		require.NoError(t, err)
		require.Contains(t, status, "locked")

		// Release via CLI.
		_, err = env.runCLI("lock", "release", "0", "--token", strings.TrimSpace(cliToken))
		require.NoError(t, err, "CLI lock release should succeed")

		// Verify unlocked via MCP.
		status2, err := env.runMCP("lock_status", map[string]any{"node_id": "0"})
		require.NoError(t, err)
		require.Contains(t, status2, "unlocked")
	})

	t.Run("lock/force_release_works_on_both", func(t *testing.T) {
		t.Parallel()
		env := newParityEnv(t)

		// Acquire via MCP.
		_, err := env.runMCP("lock_acquire", map[string]any{
			"node_id": "0",
		})
		require.NoError(t, err)

		// Force release via MCP.
		_, err = env.runMCP("lock_force_release", map[string]any{
			"node_id": "0",
		})
		require.NoError(t, err, "MCP force_release should succeed")

		// Status should show unlocked.
		status, err := env.runMCP("lock_status", map[string]any{
			"node_id": "0",
		})
		require.NoError(t, err)
		require.Contains(t, status, "unlocked")
	})
}

// TestParity_FileOperations verifies file/image listing operations.
func TestParity_FileOperations(t *testing.T) {
	t.Parallel()

	cases := []ParityTestCase{
		// Known divergence: CLI prints nothing for empty file/image lists,
		// while MCP returns "no files" / "no images".
		{
			Name:    "list_files/empty_node",
			CLIArgs: []string{"file", "ls", "0"},
			MCPTool: "list_files",
			MCPInput: map[string]any{
				"node_id": "0",
			},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				// CLI prints nothing, MCP says "no files".
				require.Empty(t, strings.TrimSpace(cliOut),
					"CLI should print nothing for empty file list")
				require.Contains(t, strings.ToLower(mcpOut), "no file",
					"MCP should indicate no files")
			},
		},
		{
			Name:    "list_images/empty_node",
			CLIArgs: []string{"image", "ls", "0"},
			MCPTool: "list_images",
			MCPInput: map[string]any{
				"node_id": "0",
			},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				// CLI prints nothing, MCP says "no images".
				require.Empty(t, strings.TrimSpace(cliOut),
					"CLI should print nothing for empty image list")
				require.Contains(t, strings.ToLower(mcpOut), "no image",
					"MCP should indicate no images")
			},
		},
	}

	runParityTests(t, cases)
}

// TestParity_FileUploadDownload verifies that CLI and MCP upload/download
// produce equivalent results using file-path handles.
func TestParity_FileUploadDownload(t *testing.T) {
	t.Parallel()
	env := newParityEnv(t)
	rt := env.sb.Runtime()

	// Write a source file in the sandbox. Name matches desired attachment name.
	srcPath := "/home/testuser/parity.txt"
	require.NoError(t, rt.WriteFile(srcPath, []byte("parity test data"), 0o644))

	// Upload via CLI: tap file upload NODE_ID LOCAL_PATH (name derived from path).
	cliUpload, err := env.runCLI("file", "upload", "0", srcPath)
	require.NoError(t, err)
	require.Contains(t, cliUpload, "parity.txt")

	// Download via MCP to verify CLI upload worked.
	mcpDestPath := "/home/testuser/parity-mcp-download.txt"
	mcpDownload, err := env.runMCP("download_file", map[string]any{
		"node_id":   "0",
		"filename":  "parity.txt",
		"dest_path": mcpDestPath,
	})
	require.NoError(t, err)
	require.Contains(t, mcpDownload, mcpDestPath)

	mcpGot, err := rt.ReadFile(mcpDestPath)
	require.NoError(t, err)
	require.Equal(t, "parity test data", string(mcpGot))

	// Clean up and re-upload via MCP.
	_, err = env.runCLI("file", "rm", "0", "parity.txt")
	require.NoError(t, err)

	_, err = env.runMCP("upload_file", map[string]any{
		"node_id":     "0",
		"filename":    "parity.txt",
		"source_path": srcPath,
	})
	require.NoError(t, err)

	// Download via CLI to verify MCP upload worked.
	cliDestPath := "/home/testuser/parity-cli-download.txt"
	cliDownload, err := env.runCLI("file", "download", "0", "parity.txt", "--dest", cliDestPath)
	require.NoError(t, err)
	require.Contains(t, cliDownload, cliDestPath)

	cliGot, err := rt.ReadFile(cliDestPath)
	require.NoError(t, err)
	require.Equal(t, "parity test data", string(cliGot))
}

// TestParity_ImageUploadDownload verifies that CLI and MCP image upload/download
// produce equivalent results using file-path handles.
func TestParity_ImageUploadDownload(t *testing.T) {
	t.Parallel()
	env := newParityEnv(t)
	rt := env.sb.Runtime()

	// Write a source image in the sandbox. Name matches desired attachment name.
	srcPath := "/home/testuser/parity.png"
	require.NoError(t, rt.WriteFile(srcPath, []byte("fake png parity"), 0o644))

	// Upload via CLI: tap image upload NODE_ID LOCAL_PATH (name derived from path).
	cliUpload, err := env.runCLI("image", "upload", "0", srcPath)
	require.NoError(t, err)
	require.Contains(t, cliUpload, "parity.png")

	// Download via MCP to verify CLI upload worked.
	mcpDestPath := "/home/testuser/parity-mcp-image.png"
	mcpDownload, err := env.runMCP("download_image", map[string]any{
		"node_id":   "0",
		"filename":  "parity.png",
		"dest_path": mcpDestPath,
	})
	require.NoError(t, err)
	require.Contains(t, mcpDownload, mcpDestPath)

	mcpGot, err := rt.ReadFile(mcpDestPath)
	require.NoError(t, err)
	require.Equal(t, "fake png parity", string(mcpGot))

	// Clean up and re-upload via MCP.
	_, err = env.runCLI("image", "rm", "0", "parity.png")
	require.NoError(t, err)

	_, err = env.runMCP("upload_image", map[string]any{
		"node_id":     "0",
		"filename":    "parity.png",
		"source_path": srcPath,
	})
	require.NoError(t, err)

	// Download via CLI to verify MCP upload worked.
	cliDestPath := "/home/testuser/parity-cli-image.png"
	cliDownload, err := env.runCLI("image", "download", "0", "parity.png", "--dest", cliDestPath)
	require.NoError(t, err)
	require.Contains(t, cliDownload, cliDestPath)

	cliGot, err := rt.ReadFile(cliDestPath)
	require.NoError(t, err)
	require.Equal(t, "fake png parity", string(cliGot))
}
