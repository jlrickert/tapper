package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/internal/testapi"
	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// newLockWaitHubSession serves an unpinned session over a Hub whose lock
// endpoint records the ?wait= each acquisition sends.
func newLockWaitHubSession(t *testing.T) (*sdkmcp.ClientSession, context.Context, func() []string) {
	t.Helper()
	ctx := context.Background()
	var (
		mu    sync.Mutex
		waits []string
	)
	hub := testapi.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/flights":
			_ = json.NewEncoder(w).Encode([]tapper.HubFlight{})
		case r.URL.Path == "/api/v1/kegs":
			_ = json.NewEncoder(w).Encode([]tapper.HubKeg{{Namespace: "root", Alias: "notes", Role: "admin", Visibility: "private"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/@root/kegs/notes/nodes/0/lock":
			mu.Lock()
			if r.URL.Query().Has("wait") {
				waits = append(waits, r.URL.Query().Get("wait"))
			} else {
				waits = append(waits, "<none>")
			}
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "hub-token", "ttl_seconds": 300})
		case strings.HasSuffix(r.URL.Path, "/settings"):
			_, _ = fmt.Fprint(w, "kegv: \"2025-07\"\ntitle: Root notes\n")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hub.Close)
	rt := newTestSandbox(t).Runtime()
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: rt})
	require.NoError(t, err)
	config := fmt.Sprintf("disableAtlasHub: true\nhub: home\nhubs:\n  home: {url: '%s'}\n", hub.URL)
	require.NoError(t, rt.AtomicWriteFile(tap.PathService.UserConfig(), []byte(config), 0o644))
	tap.ConfigService.Reload()
	store, err := tapper.LoadAuthStore(ctx, rt, tap.PathService.AuthStorePath())
	require.NoError(t, err)
	store.Set(hub.URL, tapper.AuthEntry{AccessToken: "root-token"})
	require.NoError(t, store.Save(ctx, rt, tap.PathService.AuthStorePath()))
	tap.AuthValidateFn = func(context.Context, *toolkit.Runtime, string, string) (*tapper.WhoAmI, error) {
		return &tapper.WhoAmI{UserID: 1, Username: "root", DefaultNamespace: "root", Namespaces: []string{"root"}}, nil
	}
	session := connectFlightSession(t, ctx, mcp.NewServer(tap, "test", mcp.KegDefaults{}), nil)
	return session, ctx, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), waits...)
	}
}

func TestMCP_LockAcquireSendsBoundedWaitToHub(t *testing.T) {
	t.Parallel()
	session, ctx, waits := newLockWaitHubSession(t)
	acquire := func(args map[string]any) {
		t.Helper()
		args["keg"] = "@root/notes"
		args["node_id"] = "0"
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "lock_acquire", Arguments: args})
		require.NoError(t, err)
		require.False(t, res.IsError, extractText(t, res))
		require.Equal(t, "hub-token", extractText(t, res))
	}

	acquire(map[string]any{})
	acquire(map[string]any{"wait_seconds": 25})
	acquire(map[string]any{"wait_seconds": 600})

	require.Equal(t, []string{"10", "25", "60"}, waits(),
		"the default is 10s, a request inside the bound passes through, and anything above 60s is clamped")
}

func TestMCP_LockAcquireRejectsNegativeWait(t *testing.T) {
	t.Parallel()
	session, ctx, waits := newLockWaitHubSession(t)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "lock_acquire", Arguments: map[string]any{
		"keg": "@root/notes", "node_id": "0", "wait_seconds": -1,
	}})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Contains(t, extractText(t, res), "wait_seconds")
	require.Empty(t, waits(), "an invalid bound must not reach the Hub")
}

// A locally-backed KEG honors the same bound: a held lock fails with
// a lock timeout once wait_seconds pass instead of waiting out the lock's TTL.
func TestMCP_LockAcquireLocalHeldLockTimesOutAtWait(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)
	first, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "lock_acquire", Arguments: map[string]any{"node_id": "0"}})
	require.NoError(t, err)
	require.False(t, first.IsError, extractText(t, first))

	start := time.Now()
	second, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "lock_acquire", Arguments: map[string]any{
		"node_id": "0", "wait_seconds": 1,
	}})
	require.NoError(t, err)
	elapsed := time.Since(start)
	require.True(t, second.IsError, "a held lock must not be acquired twice")
	require.Contains(t, extractText(t, second), "lock acquire timeout")
	require.GreaterOrEqual(t, elapsed, 900*time.Millisecond, "the bound is waited out, not refused at once")
	require.Less(t, elapsed, 5*time.Second)
}

// Overshoot: the bound only caps the wait; a lock released while the caller
// waits is still acquired.
func TestMCP_LockAcquireLocalLockReleasedDuringWaitIsAcquired(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)
	first, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "lock_acquire", Arguments: map[string]any{"node_id": "0"}})
	require.NoError(t, err)
	require.False(t, first.IsError, extractText(t, first))
	token := extractText(t, first)

	released := make(chan error, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "lock_release", Arguments: map[string]any{
			"node_id": "0", "token": token,
		}})
		if err == nil && res.IsError {
			err = fmt.Errorf("lock_release: %s", extractText(t, res))
		}
		released <- err
	}()
	second, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "lock_acquire", Arguments: map[string]any{
		"node_id": "0", "wait_seconds": 5,
	}})
	require.NoError(t, err)
	require.NoError(t, <-released)
	require.False(t, second.IsError, extractText(t, second))
	require.NotEqual(t, token, extractText(t, second))
}
