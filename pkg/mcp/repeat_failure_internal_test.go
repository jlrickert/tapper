package mcp

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRepeatFailureTrackerStaysBounded(t *testing.T) {
	t.Parallel()
	tracker := newRepeatFailureTracker()
	digest := canonicalArgumentsDigest(json.RawMessage(`{"a":1}`))

	for i := range repeatFailureSessions + 10 {
		tracker.record(fmt.Sprintf("s%d", i), "node_edit", digest, true)
	}
	require.Len(t, tracker.sessions, repeatFailureSessions)
	require.Equal(t, repeatFailureSessions, tracker.lru.Len())
	require.NotContains(t, tracker.sessions, "s0", "the least recently active session is forgotten first")

	for i := range repeatFailureKeysPerSession + 10 {
		tracker.record("busy", fmt.Sprintf("tool%d", i), digest, true)
	}
	busy := tracker.sessions["busy"].Value.(*repeatFailureSession)
	require.Len(t, busy.tools, repeatFailureKeysPerSession)
	require.Equal(t, repeatFailureKeysPerSession, busy.keys.Len())
	require.NotContains(t, busy.tools, "tool0")
}

func TestRepeatFailureTrackerCountsCanonicalArguments(t *testing.T) {
	t.Parallel()
	tracker := newRepeatFailureTracker()
	a := canonicalArgumentsDigest(json.RawMessage(`{"keg":"k","nodes":[{"node_id":"1"}]}`))
	b := canonicalArgumentsDigest(json.RawMessage(`{ "nodes" : [ {"node_id":"1"} ], "keg":"k" }`))
	require.Equal(t, a, b, "key order and whitespace do not make a different call")

	require.Equal(t, 1, tracker.record("s", "node_edit", a, true))
	require.Equal(t, 2, tracker.record("s", "node_edit", b, true))
	require.Equal(t, 1, tracker.record("s", "node_delete", a, true), "another tool counts separately")
	require.Equal(t, 3, tracker.record("s", "node_edit", a, true))
	require.Equal(t, 1, tracker.record("other", "node_edit", a, true), "another session counts separately")
	require.Equal(t, 0, tracker.record("s", "node_edit", a, false))
	require.Equal(t, 1, tracker.record("s", "node_edit", a, true), "a success resets the count")
	require.Equal(t, 0, tracker.record("fresh", "node_read", a, false))
	require.NotContains(t, tracker.sessions, "fresh", "a success alone creates no state")
}
