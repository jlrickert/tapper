package mcp_test

import (
	"context"
	"testing"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestMCP_DefaultFlightRestrictsKegs(t *testing.T) {
	t.Parallel()
	session, ctx, privateID := newFlightLockedSession(t)

	covered, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{"0"},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	coveredText := extractText(t, covered)
	require.False(t, covered.IsError, "covered cat returned error: %s", coveredText)
	require.Contains(t, coveredText, "# Personal Overview")

	blocked, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"keg":          "private",
			"node_ids":     []string{privateID},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	blockedText := extractText(t, blocked)
	require.True(t, blocked.IsError, "expected private keg to be blocked")
	require.Contains(t, blockedText, `keg "@local/private" is not available in flight`)
}

func TestMCP_InjectedToolFlightCannotOverrideSessionGate(t *testing.T) {
	t.Parallel()
	session, ctx, privateID := newFlightLockedSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"keg":          "private",
			"flight":       "+other",
			"node_ids":     []string{privateID},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, extractText(t, res), "unexpected additional properties")

	covered, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"keg":          "personal",
			"node_ids":     []string{"0"},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	coveredText := extractText(t, covered)
	require.False(t, covered.IsError, "session flight must remain active: %s", coveredText)
	require.Contains(t, coveredText, "# Personal Overview")
	_ = privateID
}

func TestMCP_NodeResourceUsesDefaultFlight(t *testing.T) {
	t.Parallel()
	session, ctx, privateID := newFlightLockedSession(t)

	_, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{
		URI: "tapper://node/" + privateID + "?keg=private",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `keg "@local/private" is not available in flight`)

	err = session.Subscribe(ctx, &sdkmcp.SubscribeParams{
		URI: "tapper://node/" + privateID + "?keg=private",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), `keg "@local/private" is not available in flight`)
}

func newFlightLockedSession(t *testing.T) (*sdkmcp.ClientSession, context.Context, string) {
	t.Helper()
	ctx := context.Background()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Setwd("/home/testuser"))
	rt := sb.Runtime()
	sb.MustWriteFile("~/.config/tapper/config.yaml", []byte(`defaultKeg: personal
fallbackNamespace: local
hubs:
  home:
    kind: local
    defaultNamespace: local
    basePath: ~/kegs
`), 0o644)

	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: rt})
	require.NoError(t, err)

	_, err = tap.InitKeg(ctx, tapper.InitOptions{Keg: "private", Namespace: "local"})
	require.NoError(t, err)
	privateID, err := tap.Create(ctx, tapper.CreateOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: "private"},
		Title:            "Private",
	})
	require.NoError(t, err)

	focused := `title: Focused
cover:
  - namespace: local
    keg: personal
    role: viewer
`
	other := `title: Other
cover:
  - namespace: local
    keg: private
    role: viewer
`
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/kegs/flights.d/focused.yaml", []byte(focused), 0o644))
	require.NoError(t, rt.AtomicWriteFile("/home/testuser/kegs/flights.d/other.yaml", []byte(other), 0o644))

	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{
		KegTargetOptions: tapper.KegTargetOptions{Flight: "+focused"},
	})
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx, serverTransport)
	}()
	t.Cleanup(func() {
		<-done
	})

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "test-client",
		Version: "0.1",
	}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		session.Close()
	})

	return session, ctx, privateID.PathNumeric()
}
