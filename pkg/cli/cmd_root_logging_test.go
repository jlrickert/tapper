package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// TestCLI_Logging_StderrTextFormat verifies that when --log-file is set,
// stderr receives text-format log entries (not JSON), even if --log-json
// is also set. JSON output should only go to the log file.
func TestCLI_Logging_StderrTextFormat(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	h := NewProcess(t, false,
		"--log-file", "tap.log",
		"--log-json",
		"--log-level", "info",
		"list", "--keg", "personal",
	)
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "list command should succeed")

	stderr := string(res.Stderr)

	// Stderr should NOT contain JSON log entries.
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// JSON entries start with '{'; text entries do not.
		require.False(t, strings.HasPrefix(line, "{"),
			"stderr should use text format, not JSON; got: %s", line)
	}

	// The log file should contain JSON entries.
	logContent, err := sb.Runtime().ReadFile("tap.log")
	require.NoError(t, err, "should be able to read log file")
	entry := findJSONLogEntry(t, string(logContent), "invocation")
	require.NotNil(t, entry, "log file should contain a JSON invocation entry")
	require.Equal(t, "cli", entry["surface"])
}

// TestCLI_Logging_StderrErrorLevelOnly verifies that when a log file is
// configured, stderr only receives error-level entries (not INFO).
func TestCLI_Logging_StderrErrorLevelOnly(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	h := NewProcess(t, false,
		"--log-file", "tap.log",
		"--log-level", "info",
		"list", "--keg", "personal",
	)
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "list command should succeed")

	stderr := string(res.Stderr)

	// Stderr should NOT contain the invocation entry (which is at INFO).
	require.NotContains(t, stderr, "invocation",
		"INFO-level invocation entry should not appear on stderr when log file is set")

	// But the log file should have it.
	logContent, err := sb.Runtime().ReadFile("tap.log")
	require.NoError(t, err, "should be able to read log file")
	require.Contains(t, string(logContent), "invocation",
		"log file should contain the invocation entry")
}

// TestCLI_Logging_InteractiveField verifies that the invocation log entry
// includes the "interactive" field indicating TTY status.
func TestCLI_Logging_InteractiveField(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Non-TTY run
	h := NewProcess(t, false, "--log-json", "list", "--keg", "personal")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	entry := findJSONLogEntry(t, string(res.Stderr), "invocation")
	require.NotNil(t, entry, "should find invocation entry")
	interactive, ok := entry["interactive"]
	require.True(t, ok, "interactive field should be present")
	require.Equal(t, false, interactive, "non-TTY should report interactive=false")

	// TTY run
	hTTY := NewProcess(t, true, "--log-json", "list", "--keg", "personal")
	resTTY := hTTY.Run(sb.Context(), sb.Runtime())
	require.NoError(t, resTTY.Err)
	entryTTY := findJSONLogEntry(t, string(resTTY.Stderr), "invocation")
	require.NotNil(t, entryTTY, "should find invocation entry in TTY mode")
	interactiveTTY, ok := entryTTY["interactive"]
	require.True(t, ok, "interactive field should be present in TTY mode")
	require.Equal(t, true, interactiveTTY, "TTY should report interactive=true")
}

// TestCLI_Logging_PayloadTruncation verifies that large CLI arguments are
// truncated in invocation log entries to prevent log bloat.
func TestCLI_Logging_PayloadTruncation(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Generate a large argument (1024 bytes, well over the 512 limit).
	bigArg := strings.Repeat("x", 1024)

	// Pass the big argument as a flag value that will appear in args.
	// Using --keg with a huge value to trigger truncation in the log.
	h := NewProcess(t, false, "--log-json", "list", "--keg", bigArg)
	res := h.Run(sb.Context(), sb.Runtime())
	// Command may fail (bad keg alias), but we only care about the log entry.

	_ = res.Err
	entry := findJSONLogEntry(t, string(res.Stderr), "invocation")
	require.NotNil(t, entry, "should find invocation entry even on error")

	// The command field should be truncated.
	command, ok := entry["command"].(string)
	require.True(t, ok, "command field should be a string")
	require.Contains(t, command, "...(truncated)",
		"large argument should be truncated in command field")
	require.LessOrEqual(t, len(command), 1024,
		"truncated command should be much shorter than original")

	// Check args array
	args, ok := entry["args"].([]any)
	require.True(t, ok, "args should be an array")
	// Find the big arg in the array
	foundTruncated := false
	for _, a := range args {
		if s, ok := a.(string); ok && strings.Contains(s, "...(truncated)") {
			foundTruncated = true
			break
		}
	}
	require.True(t, foundTruncated, "args array should contain a truncated entry")
}

// TestCLI_Logging_LogFileViaSandbox verifies that the log file is created
// through the Runtime abstraction (rt.OpenFile), enabling sandbox tests to
// capture log output without touching the real filesystem.
func TestCLI_Logging_LogFileViaSandbox(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	h := NewProcess(t, false,
		"--log-file", "state/tapper/tap.log",
		"--log-level", "info",
		"list", "--keg", "personal",
	)
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "list command should succeed")

	// The log file should exist in the sandbox and contain an entry.
	content, err := sb.Runtime().ReadFile("state/tapper/tap.log")
	require.NoError(t, err, "log file should be readable in sandbox")
	require.Contains(t, string(content), "invocation",
		"log file should contain an invocation entry")
}

// TestCLI_Logging_LogLevelCompletion verifies that --log-level offers
// shell completion suggestions for valid log levels.
func TestCLI_Logging_LogLevelCompletion(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Complete --log-level with no prefix.
	h := NewCompletionProcess(t, false, 0, "--log-level", "")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	suggestions := parseCompletionSuggestions(string(res.Stdout))
	require.Contains(t, suggestions, "debug")
	require.Contains(t, suggestions, "info")
	require.Contains(t, suggestions, "warn")
	require.Contains(t, suggestions, "error")
}

// TestCLI_Logging_LogLevelCompletionPrefix verifies that --log-level
// filters suggestions by prefix.
func TestCLI_Logging_LogLevelCompletionPrefix(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Complete --log-level with "d" prefix.
	h := NewCompletionProcess(t, false, 0, "--log-level", "d")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	suggestions := parseCompletionSuggestions(string(res.Stdout))
	require.Contains(t, suggestions, "debug")
	require.NotContains(t, suggestions, "info")
	require.NotContains(t, suggestions, "warn")
	require.NotContains(t, suggestions, "error")
}

// TestCLI_Logging_TildeExpansion verifies that logFile config values with
// tildes are expanded to the user's home directory.
func TestCLI_Logging_TildeExpansion(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t,
		testutils.WithFixture("joe", "~"),
	)

	// Write a config with a tilde-prefixed logFile path.
	rt := sb.Runtime()
	configContent := []byte(`defaultKeg: personal
logFile: ~/logs/tap.log
logLevel: info
kegs:
  personal: ~/kegs/personal
`)
	err := rt.WriteFile(".config/tapper/config.yaml", configContent, 0o644)
	require.NoError(t, err)

	h := NewProcess(t, false, "list", "--keg", "personal")
	res := h.Run(sb.Context(), rt)
	require.NoError(t, res.Err, "list command should succeed")

	// The log file should be created under the sandbox home.
	logContent, err := rt.ReadFile("logs/tap.log")
	require.NoError(t, err, "tilde-expanded log file should exist")
	require.Contains(t, string(logContent), "invocation",
		"expanded log file should contain an invocation entry")
}

// TestCLI_Logging_AbsoluteLogFilePath verifies that absolute logFile
// config values are used as-is without expansion.
func TestCLI_Logging_AbsoluteLogFilePath(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	h := NewProcess(t, false,
		"--log-file", "absolute/path/tap.log",
		"--log-level", "info",
		"list", "--keg", "personal",
	)
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "list command should succeed")

	content, err := sb.Runtime().ReadFile("absolute/path/tap.log")
	require.NoError(t, err, "log file at specified path should exist")
	require.Contains(t, string(content), "invocation",
		"log file should contain an invocation entry")
}

// TestCLI_Logging_JSONAutoDetect verifies that a .json log file extension
// auto-detects JSON format without requiring --log-json.
func TestCLI_Logging_JSONAutoDetect(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	h := NewProcess(t, false,
		"--log-file", "tap.json",
		"--log-level", "info",
		"list", "--keg", "personal",
	)
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "list command should succeed")

	logContent, err := sb.Runtime().ReadFile("tap.json")
	require.NoError(t, err, "JSON log file should exist")
	entry := findJSONLogEntry(t, string(logContent), "invocation")
	require.NotNil(t, entry, "JSON log file should contain a parseable invocation entry")
	require.Equal(t, "cli", entry["surface"])
}

// findTextLogEntry scans multi-line text-format log output for a line
// containing the given message substring and returns true if found.
func findTextLogEntry(output, wantMsg string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, wantMsg) {
			return true
		}
	}
	return false
}

// findJSONLogEntryInFile is a convenience helper that reads a file from the
// sandbox runtime and looks for a JSON log entry with the given message.
func findJSONLogEntryInFile(t *testing.T, content []byte, wantMsg string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(string(content), "\n") {
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
