package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestCLI_InvocationLogging_Success(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Use --log-json so we can parse the structured log entry from stderr.
	h := NewProcess(t, false, "--log-json", "list", "--keg", "personal")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "list command should succeed")

	// Find the invocation log entry in stderr. The log output may contain
	// multiple lines; look for the one with "invocation".
	entry := findJSONLogEntry(t, string(res.Stderr), "invocation")
	require.NotNil(t, entry, "should emit an invocation log entry on stderr")

	require.Equal(t, "cli", entry["surface"])
	require.Contains(t, entry["command"], "list")
	require.Equal(t, true, entry["success"])

	// duration_ms should be present and non-negative. Sandbox uses a frozen
	// clock so the value will be 0.
	durationMs, ok := entry["duration_ms"].(float64) // JSON numbers decode as float64
	require.True(t, ok, "duration_ms should be a number")
	require.GreaterOrEqual(t, durationMs, float64(0), "duration_ms should be non-negative")

	// keg alias should be present when --keg is provided.
	require.Equal(t, "personal", entry["keg"])
}

func TestCLI_InvocationLogging_Error(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Call cat with a nonexistent node to trigger an error.
	h := NewProcess(t, false, "--log-json", "cat", "99999", "--keg", "personal")
	res := h.Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err, "cat of nonexistent node should fail")

	entry := findJSONLogEntry(t, string(res.Stderr), "invocation")
	require.NotNil(t, entry, "should emit an invocation log entry even on error")

	require.Equal(t, "cli", entry["surface"])
	require.Contains(t, entry["command"], "cat")
	require.Equal(t, false, entry["success"])

	// Error message should be present.
	errMsg, hasErr := entry["error"]
	require.True(t, hasErr, "error field should be present on failure")
	require.NotEmpty(t, errMsg, "error message should not be empty")
}

func TestCLI_InvocationLogging_NoStdoutContamination(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	h := NewProcess(t, false, "--log-json", "list", "--keg", "personal")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	// Stdout should not contain any log entries.
	stdout := string(res.Stdout)
	require.NotContains(t, stdout, `"invocation"`,
		"log entries must not appear in stdout")
	require.NotContains(t, stdout, `"surface"`,
		"log entries must not appear in stdout")
}

// findJSONLogEntry scans multi-line output for a JSON line containing the
// given message value and returns the decoded map, or nil if not found.
func findJSONLogEntry(t *testing.T, output, wantMsg string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		if msg, ok := m["msg"].(string); ok && msg == wantMsg {
			return m
		}
	}
	return nil
}
