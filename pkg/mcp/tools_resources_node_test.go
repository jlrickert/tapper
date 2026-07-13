package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
)

func TestMCP_NodeResource_ReadCurrentContent(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	readRes, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{
		URI: "tapper://node/1",
	})
	require.NoError(t, err)
	require.Len(t, readRes.Contents, 1)
	require.Equal(t, "text/markdown", readRes.Contents[0].MIMEType)
	require.Contains(t, readRes.Contents[0].Text, "# Hello World")
}

func TestMCP_NodeResource_SubscribeNotifiesOnChange(t *testing.T) {
	ctx := context.Background()
	sb := newTestSandbox(t)
	rt := sb.Runtime()

	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: rt})
	require.NoError(t, err)

	updates := make(chan string, 8)
	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "test-client",
		Version: "0.1",
	}, &sdkmcp.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, req *sdkmcp.ResourceUpdatedNotificationRequest) {
			select {
			case updates <- req.Params.URI:
			default:
			}
		},
	})
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{KegTargetOptions: tapper.KegTargetOptions{Flight: "@local/+test"}})
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx, serverTransport)
	}()
	t.Cleanup(func() {
		<-done
	})

	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		session.Close()
	})

	const uri = "tapper://node/1"
	require.NoError(t, session.Subscribe(ctx, &sdkmcp.SubscribeParams{URI: uri}))

	err = tap.Edit(ctx, tapper.EditOptions{
		NodeID: "1",
		Stream: &toolkit.Stream{
			In:      strings.NewReader("# Changed Through MCP Test\n"),
			IsPiped: true,
		},
	})
	require.NoError(t, err)

	select {
	case got := <-updates:
		require.Equal(t, uri, got)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for resource update notification")
	}
}
