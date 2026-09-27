package mcp_test

import (
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestMCPLocalTransfersStayInWorkspaceAndNeverOverwrite(t *testing.T) {
	session, rt, ctx := newLocalTestSessionWithRuntime(t)
	created, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "node_create", Arguments: batchCreateArgs(map[string]any{"title": "Transfer security"})})
	require.NoError(t, err)
	id := extractText(t, created)
	uploaded, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "file_upload", Arguments: map[string]any{"node_id": id, "filename": "safe.txt", "data_base64": "c2FmZQ=="}})
	require.NoError(t, err)
	require.False(t, uploaded.IsError)
	require.NoError(t, rt.Mkdir("/outside", 0700, true))
	require.NoError(t, rt.WriteFile("/outside/secret", []byte("secret"), 0600))
	require.NoError(t, rt.Symlink("/outside", "/home/testuser/link"))
	require.NoError(t, rt.WriteFile("/home/testuser/present", []byte("keep"), 0600))
	for _, path := range []string{"/outside/secret", "../../outside/secret", "link/secret"} {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "file_upload", Arguments: map[string]any{"node_id": id, "filename": "blocked.txt", "source_path": path}})
		require.NoError(t, err)
		require.True(t, result.IsError, path)
	}
	for _, path := range []string{"/outside/new", "../../outside/new", "link/new", "present"} {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "file_download", Arguments: map[string]any{"node_id": id, "filename": "safe.txt", "dest_path": path}})
		require.NoError(t, err)
		require.True(t, result.IsError, path)
	}
	original, err := rt.ReadFile("/home/testuser/present")
	require.NoError(t, err)
	require.Equal(t, "keep", string(original))
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "file_download", Arguments: map[string]any{"node_id": id, "filename": "safe.txt", "dest_path": "new"}})
	require.NoError(t, err)
	require.False(t, result.IsError)
	saved, err := rt.ReadFile("/home/testuser/new")
	require.NoError(t, err)
	require.Equal(t, "safe", string(saved))
	listed, err := session.ListTools(ctx, &sdkmcp.ListToolsParams{})
	require.NoError(t, err)
	for _, tool := range listed.Tools {
		if tool.Name == "image_download" {
			require.False(t, tool.Annotations.ReadOnlyHint)
		}
	}
}
