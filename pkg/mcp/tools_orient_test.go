package mcp_test

import (
	"context"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// orientCall is a thin helper that invokes the orient tool and returns
// the text payload. It fails the test on transport error or tool-side
// error so callers can treat the returned string as authoritative.
func orientCall(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, args map[string]any) string {
	t.Helper()
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "orient",
		Arguments: args,
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "orient returned error: %s", text)
	return text
}

func TestMCP_OrientTool_ReturnsSharedKegSystemPayload(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	text := orientCall(t, session, ctx, map[string]any{})

	require.True(t, strings.HasPrefix(text, "# KEG System\n\n"), text)
	require.Contains(t, text, "Tapper provides an MCP interface for KEG")
	require.NotContains(t, text, "CLI")
	require.NotContains(t, text, "`tap ")
	require.NotContains(t, text, "## Active KEG")
	require.Contains(t, text, "## Available KEGs")
	require.NotContains(t, text, "## KEG Instructions")
	require.Contains(t, text, "Call `keg_settings`")
	require.Contains(t, text, "## Guidance")
	require.Contains(t, text, "# Linking conventions")
	require.Contains(t, text, "# Snapshot policy")
	require.NotContains(t, text, "## Host:")
	require.NotContains(t, strings.ToLower(text), "tier 0")
	require.NotContains(t, strings.ToLower(text), "tier 1")
	require.NotContains(t, strings.ToLower(text), "tier 2")
}

func TestMCP_OrientToolRejectsKegTarget(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "orient",
		Arguments: map[string]any{"keg": "notes"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, extractText(t, res), "unexpected additional properties")
}

func TestMCP_OrientToolRejectsInjectedFlight(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "orient",
		Arguments: map[string]any{"flight": "f-demo"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, extractText(t, res), "unexpected additional properties")
}

func TestMCP_Resources_ListSingleOrientResource(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.ListResources(ctx, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Resources, "expected at least one orient resource")

	uris := make(map[string]struct{}, len(res.Resources))
	for _, r := range res.Resources {
		uris[r.URI] = struct{}{}
		require.NotContains(t, r.URI, "/tier-")
		require.False(t, strings.HasPrefix(r.URI, "tapper://orient/"), r.URI)
	}

	require.Contains(t, uris, "tapper://orient")
}

func TestMCP_Resources_ReadMatchesToolBytes(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	toolText := orientCall(t, session, ctx, map[string]any{})

	readRes, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{
		URI: "tapper://orient",
	})
	require.NoError(t, err)
	require.Len(t, readRes.Contents, 1)
	require.Equal(t, "tapper://orient", readRes.Contents[0].URI)
	require.Equal(t, "text/markdown", readRes.Contents[0].MIMEType)
	require.Equal(t, toolText, readRes.Contents[0].Text)
	require.NotContains(t, readRes.Contents[0].Text, "## Host:")
}
