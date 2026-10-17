package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
)

func newURLSession(t *testing.T, webBaseURL string) (*sdkmcp.ClientSession, context.Context) {
	t.Helper()
	ctx := context.Background()
	rt := newTestSandbox(t).Runtime()
	tap := newMemoryTap(t, ctx, rt)
	tap.WebBaseURL = webBaseURL

	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{})
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx, serverTransport) }()
	t.Cleanup(func() { <-done })
	session, err := newTestClient(t, "test-client", nil).
		Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session, ctx
}

func callURL(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, args map[string]any) (*sdkmcp.CallToolResult, tapper.WebURL) {
	t.Helper()
	result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "url", Arguments: args})
	require.NoError(t, err)
	var link tapper.WebURL
	if result.StructuredContent != nil {
		raw, err := json.Marshal(result.StructuredContent)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &link))
	}
	return result, link
}

func TestURLTool_ReturnsTheNoteLink(t *testing.T) {
	t.Parallel()
	session, ctx := newURLSession(t, "https://hub.example")

	result, link := callURL(t, session, ctx, map[string]any{"ref": "1"})
	require.False(t, result.IsError, extractText(t, result))
	require.Equal(t, tapper.WebURL{URL: "https://hub.example/@local/personal/1", Kind: "node", Ref: "keg:@local/personal/1"}, link)
	require.Equal(t, "https://hub.example/@local/personal/1", extractText(t, result))

	_, link = callURL(t, session, ctx, map[string]any{"ref": "@team/+dev"})
	require.Equal(t, tapper.WebURL{URL: "https://hub.example/@team/+dev", Kind: "flight", Ref: "@team/+dev"}, link)

	_, link = callURL(t, session, ctx, map[string]any{})
	require.Equal(t, "keg", link.Kind)
	require.Equal(t, "https://hub.example/@local/personal", link.URL)
}

func TestURLTool_IsReadOnly(t *testing.T) {
	t.Parallel()
	session, ctx := newURLSession(t, "https://hub.example")
	listed, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	for _, tool := range listed.Tools {
		if tool.Name == "url" {
			require.True(t, tool.Annotations.ReadOnlyHint)
			return
		}
	}
	t.Fatal("url tool not registered")
}

func TestURLTool_ReportsAnUnrecognizedRef(t *testing.T) {
	t.Parallel()
	session, ctx := newURLSession(t, "https://hub.example")
	result, _ := callURL(t, session, ctx, map[string]any{"ref": "Not A Keg!"})
	require.True(t, result.IsError)
	require.Contains(t, extractText(t, result), "unrecognized reference")
}
