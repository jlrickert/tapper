package cli

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMcpServerOptionsSharesFilesystem pins the `tap mcp` half of the split
// attachment surface. Without it, upload_file and upload_image lose
// source_path, download_file is not registered at all, and download_image can
// no longer write to dest_path — a silent capability loss that the pkg/mcp
// tests would not catch, because they construct their own servers.
func TestMcpServerOptionsSharesFilesystem(t *testing.T) {
	t.Parallel()

	opts := mcpServerOptions(nil, nil)
	require.True(t, opts.SharedFilesystem,
		"tap mcp runs on the agent's own machine and must publish local-path attachment transfers")
}
