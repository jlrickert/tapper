package mcp_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// TestMCP_ClientFixableErrorsAreRefusals drives each bad input through its
// tool and asserts the failure names the caller's mistake: a client code and
// operationPerformed false, never INTERNAL with an unknown outcome.
func TestMCP_ClientFixableErrorsAreRefusals(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)
	nodeID := createNodeForTest(t, session, ctx, "# Target\n", "")
	settingsHash := readSettingsHash(t, session, ctx, "")

	cases := []struct {
		name string
		tool string
		args map[string]any
		code string
	}{
		{"unbalanced query", "node_list", map[string]any{"query": "(golang"}, keg.RemoteCodeInvalid},
		{"truncated query", "node_list", map[string]any{"query": "golang and"}, keg.RemoteCodeInvalid},
		{"query on node_read", "node_read", map[string]any{"query": "or"}, keg.RemoteCodeInvalid},
		{"non-numeric node id", "node_read", map[string]any{"node_ids": []string{"abc"}}, keg.RemoteCodeInvalid},
		{"leading-zero node id", "node_stats", map[string]any{"node_id": "007"}, keg.RemoteCodeInvalid},
		{"missing node", "node_read", map[string]any{"node_ids": []string{"9999"}}, keg.RemoteCodeNotFound},
		{"stats of a missing node", "node_stats", map[string]any{"node_id": "9999"}, keg.RemoteCodeNotFound},
		{"settings keg and kegs", "keg_settings_read", map[string]any{"keg": "a", "kegs": []string{"@a/b"}}, keg.RemoteCodeInvalid},
		{"settings full read of two kegs", "keg_settings_read", map[string]any{"kegs": []string{"@a/b", "@a/c"}, "minimal": false}, keg.RemoteCodeInvalid},
		{"removed keg_list all", "keg_list", map[string]any{"all": true}, "INVALID_ARGUMENT"},
		{"settings without kegv", "keg_settings_edit", map[string]any{"data": "title: x\n", "expected_hash": settingsHash}, keg.RemoteCodeInvalid},
		{"settings bad yaml", "keg_settings_edit", map[string]any{"data": "kegv: [\n", "expected_hash": settingsHash}, keg.RemoteCodeInvalid},
		{"settings bad index query", "keg_settings_edit", map[string]any{
			"data": "kegv: 2025-07\nindexes:\n  - file: broken.md\n    query: \"(a\"\n", "expected_hash": settingsHash,
		}, keg.RemoteCodeInvalid},
		{"bad base64", "file_upload", map[string]any{"node_id": nodeID, "filename": "a.txt", "data_base64": "!!!"}, keg.RemoteCodeInvalid},
		{"bad data uri", "file_upload", map[string]any{"node_id": nodeID, "filename": "a.txt", "source_uri": "data:text/plain"}, keg.RemoteCodeInvalid},
		{"no upload source", "file_upload", map[string]any{"node_id": nodeID, "filename": "a.txt"}, keg.RemoteCodeInvalid},
		{"unknown guide topic", "guide", map[string]any{"topic": "nope"}, keg.RemoteCodeInvalid},
	}
	for _, tc := range cases {
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
		require.NoError(t, err, tc.name)
		payload := errorPayload(t, res)
		require.Equal(t, tc.code, payload["code"], "%s: %v", tc.name, payload["message"])
		require.Equal(t, false, payload["operationPerformed"], tc.name)
		require.NotEmpty(t, payload["action"], tc.name)
	}

	// None of the refusals wrote anything.
	require.Equal(t, settingsHash, readSettingsHash(t, session, ctx, ""))

	// Overshoot: an existing node's stats still read.
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "node_stats", Arguments: map[string]any{"node_id": nodeID}})
	require.NoError(t, err)
	require.False(t, res.IsError, "stats of an existing node")
}

// TestMCP_StoredBadIndexQueryIsNotTheCallersFault is the overshoot side: a
// query stored in keg settings (written before edits were validated) fails
// every listing, and that failure must not tell a caller whose own query is
// fine to fix its arguments.
func TestMCP_StoredBadIndexQueryIsNotTheCallersFault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	rt := newTestSandbox(t).Runtime()
	tap := newMemoryTap(t, ctx, rt)
	resolved, err := tap.KegResolver(ctx, tapper.KegTargetOptions{}, tapper.FlightRoleAdmin)
	require.NoError(t, err)
	local, ok := resolved.(*keg.LocalKeg)
	require.True(t, ok)
	cfg, err := local.Settings(ctx)
	require.NoError(t, err)
	cfg.Indexes = append(cfg.Indexes, keg.IndexEntry{File: "broken.md", Query: "(a"})
	require.NoError(t, local.Repo.WriteSettings(ctx, cfg), "seed the stored query below the strict edit check")

	session := connectFlightSession(t, ctx, mcp.NewServer(tap, "test", mcp.KegDefaults{}), nil)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "node_list", Arguments: map[string]any{"query": "hello"}})
	require.NoError(t, err)
	payload := errorPayload(t, res)
	require.Equal(t, keg.RemoteCodeInternal, payload["code"], "%v", payload["message"])
	require.Nil(t, payload["operationPerformed"])
	require.Contains(t, payload["message"], "unable to list keg: dex: settings index \"broken.md\"",
		"the caller's valid query must not be blamed for the stored one")
}

// providerFailures is a flight and agent provider whose creates fail with a
// fixed error, standing in for a Hub's answer on the wire.
type providerFailures struct {
	fakeAgents
	createErr error
}

func (p *providerFailures) CreateAgent(context.Context, tapper.CreateAgentOptions) (*tapper.HubAgent, error) {
	return nil, p.createErr
}
func (p *providerFailures) ListFlights(context.Context) ([]string, error) { return nil, nil }
func (p *providerFailures) GetFlight(context.Context, string) (*tapper.Flight, error) {
	return nil, keg.ErrNotExist
}
func (p *providerFailures) CreateFlight(context.Context, tapper.CreateFlightOptions) (*tapper.Flight, error) {
	return nil, p.createErr
}
func (p *providerFailures) UpdateFlight(context.Context, tapper.UpdateFlightOptions) (*tapper.Flight, error) {
	return nil, p.createErr
}
func (p *providerFailures) DeleteFlight(context.Context, tapper.DeleteFlightOptions) error {
	return p.createErr
}

// TestMCP_CreateFailuresByKind is the error kind × tool table for the create
// tools: a Hub refusal is reported as a refusal with its own guidance, and only
// an unclassified failure keeps the unknown outcome.
func TestMCP_CreateFailuresByKind(t *testing.T) {
	t.Parallel()
	kinds := []struct {
		name      string
		err       error
		code      string
		performed any
		action    string
		retry     any
	}{
		{"duplicate name", keg.RemoteErrorFromCode(keg.RemoteCodeConflict, http.StatusConflict, "flight or agent already exists"),
			keg.RemoteCodeConflict, false, "conflicts with current state", nil},
		{"rate limited", keg.NewRateLimitError(1500*time.Millisecond, "slow down", nil),
			keg.RemoteCodeRateLimited, false, "retryAfterSeconds", float64(2)},
		{"keg restoring", keg.RemoteErrorFromCode(keg.RemoteCodeKegRestoring, http.StatusConflict, "keg is being restored"),
			keg.RemoteCodeKegRestoring, false, "being restored", nil},
		{"archive too large", keg.RemoteErrorFromCode(keg.RemoteCodeArchiveTooLarge, http.StatusRequestEntityTooLarge, "archive exceeds limit"),
			keg.RemoteCodeArchiveTooLarge, false, "size or count limit", nil},
		{"internal failure", errors.New("database connection reset"),
			keg.RemoteCodeInternal, nil, "outcome is unknown", nil},
	}
	tools := []struct {
		tool string
		args map[string]any
	}{
		{"flight_create", map[string]any{"ref": "@me/+dup", "title": "Dup"}},
		{"agent_create", map[string]any{"agent": "@me/dup", "title": "Dup", "model": "@me/qwen3"}},
	}
	for _, kind := range kinds {
		provider := &providerFailures{fakeAgents: fakeAgents{byRef: map[string]tapper.HubAgent{}}, createErr: kind.err}
		session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{FlightProvider: provider, AgentProvider: provider})
		for _, tool := range tools {
			res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: tool.tool, Arguments: tool.args})
			require.NoError(t, err)
			payload := errorPayload(t, res)
			label := kind.name + "/" + tool.tool
			require.Equal(t, kind.code, payload["code"], "%s: %v", label, payload["message"])
			require.Equal(t, kind.performed, payload["operationPerformed"], label)
			require.Contains(t, payload["action"], kind.action, label)
			require.Equal(t, kind.retry, payload["retryAfterSeconds"], label)
			require.Contains(t, payload["message"], kind.err.Error(), label)
		}
	}
}

// TestMCP_LockedSessionNamesTheRepair pins errMCPFlightRequired's
// classification: nothing was performed, and the action sends the agent to
// repair the selection rather than to try another flight. A real per-call
// denial on an active session keeps the ordinary denial advice.
func TestMCP_LockedSessionNamesTheRepair(t *testing.T) {
	backend := newRefreshFlightBackend("selection")
	session, ctx := newRefreshFlightSession(t, backend, nil)
	for _, args := range []map[string]any{{}, {"flight": "+other"}} {
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_create", Arguments: args})
		require.NoError(t, err)
		payload := errorPayload(t, res)
		require.Equal(t, keg.RemoteCodeOrientationDenied, payload["code"])
		require.Equal(t, false, payload["operationPerformed"])
		require.Contains(t, payload["action"], "session_refresh")
		require.Contains(t, payload["action"], "Passing a different flight will not unlock them")
	}

	active, activeCtx := newPerCallFlightSession(t, newPerCallFlightBackend())
	denied, err := active.CallTool(activeCtx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{"flight": "+missing"}})
	require.NoError(t, err)
	payload := errorPayload(t, denied)
	require.Equal(t, keg.RemoteCodeOrientationDenied, payload["code"])
	require.Contains(t, payload["action"], "Pass a flight that covers this keg")
	require.NotContains(t, payload["action"], "session_refresh")
}

// TestMCP_RepeatFailureCountsTheFlight pins that the loop breaker digests the
// call as sent, flight included: following ORIENTATION_DENIED's advice to try
// another flight is not a repeat, while the same call on one flight still is.
func TestMCP_RepeatFailureCountsTheFlight(t *testing.T) {
	session, ctx := newPerCallFlightSession(t, newPerCallFlightBackend())
	call := func(flight string) map[string]any {
		t.Helper()
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "keg_list", Arguments: map[string]any{"flight": flight}})
		require.NoError(t, err)
		payload := errorPayload(t, res)
		require.Equal(t, keg.RemoteCodeOrientationDenied, payload["code"])
		return payload
	}

	for _, flight := range []string{"+missing-a", "+missing-b", "+missing-c", "+missing-d"} {
		payload := call(flight)
		require.NotContains(t, payload, "repeat_count", "a call on another flight is a different call (%s)", flight)
	}

	call("+missing-e")
	call("+missing-e")
	third := call("+missing-e")
	require.Equal(t, float64(3), third["repeat_count"])
	require.Contains(t, third["action"], "stop retrying")
	require.Equal(t, keg.RemoteCodeOrientationDenied, third["code"], "the repeat keeps its original code")
}
