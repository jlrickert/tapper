package cli_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tu "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestWatchCommand_ErrorHandling(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		expectedErr string
	}{
		{
			name:        "watch_missing_node_id",
			args:        []string{"watch"},
			expectedErr: "accepts 1 arg",
		},
		{
			name:        "watch_invalid_node_id",
			args:        []string{"watch", "invalid"},
			expectedErr: "invalid node ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(innerT *testing.T) {
			innerT.Parallel()
			sb := NewSandbox(innerT, tu.WithFixture("joe", "~"))

			h := NewProcess(innerT, false, tt.args...)
			res := h.Run(sb.Context(), sb.Runtime())

			require.Error(innerT, res.Err)
			require.Contains(innerT, string(res.Stderr), tt.expectedErr)
		})
	}
}

// TestWatchCommand_TimeoutWithNoEvents verifies that --timeout terminates the
// watch cleanly with no output when nothing changes.
func TestWatchCommand_TimeoutWithNoEvents(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, tu.WithFixture("joe", "~"))

	h := NewProcess(t, false, "watch", "0", "--keg", "personal", "--timeout", "300ms")
	res := h.Run(sb.Context(), sb.Runtime())

	require.NoError(t, res.Err)
	require.Empty(t, strings.TrimSpace(string(res.Stdout)),
		"no events expected when the node is untouched")
}

// TestWatchCommand_EmitsEventOnContentChange runs the watch in the background
// and modifies the node's README.md until the watcher reports the change.
func TestWatchCommand_EmitsEventOnContentChange(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, tu.WithFixture("joe", "~"))

	done := make(chan *tu.ProcessResult, 1)
	go func() {
		h := NewProcess(t, false,
			"watch", "0", "--keg", "personal", "--json", "--count", "1", "--timeout", "15s")
		res := h.Run(sb.Context(), sb.Runtime())
		done <- res
	}()

	// The watcher needs a moment to register before writes are observable.
	// Keep writing until the watch exits (or times out); each write changes
	// the content so fsnotify fires.
	contentPath := "~/kegs/@local/personal/0/README.md"
	var res *tu.ProcessResult
	deadline := time.After(20 * time.Second)
	i := 0
loop:
	for {
		select {
		case res = <-done:
			break loop
		case <-deadline:
			t.Fatal("watch did not observe a content change in time")
		case <-time.After(200 * time.Millisecond):
			i++
			body := "# Watch Test\n\nrevision " + strings.Repeat("x", i) + "\n"
			sb.MustWriteFile(contentPath, []byte(body), 0o644)
		}
	}

	require.NoError(t, res.Err)
	stdout := strings.TrimSpace(string(res.Stdout))
	require.NotEmpty(t, stdout, "expected one event line")

	var ev struct {
		Kind  string `json:"kind"`
		Node  string `json:"node"`
		Field string `json:"field"`
		Ts    string `json:"ts"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.Split(stdout, "\n")[0]), &ev))
	require.Equal(t, "0", ev.Node)
	require.Equal(t, "content", ev.Field)
	require.Contains(t, []string{"modified", "created"}, ev.Kind)
	require.NotEmpty(t, ev.Ts)
}
