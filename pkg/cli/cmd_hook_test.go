package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func hookProcess(t *testing.T, profile Profile, args ...string) *sandbox.Process {
	t.Helper()
	return sandbox.NewProcess(func(ctx context.Context, rt *toolkit.Runtime) (int, error) {
		return RunWithProfile(ctx, rt, args, profile)
	}, false)
}

func TestHookPreToolUse_GuardsCommands(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		command string
		deny    bool
	}{
		{name: "tap direct", command: "tap list", deny: true},
		{name: "keg direct", command: "keg", deny: true},
		{name: "absolute path", command: "/usr/local/bin/tap cat 1", deny: true},
		{name: "escaped basename", command: `\tap list`, deny: true},
		{name: "environment assignments", command: "FOO=bar OTHER=two tap list", deny: true},
		{name: "wrapper", command: "command tap list", deny: true},
		{name: "wrapper with assignment", command: "sudo FOO=bar keg cat 1", deny: true},
		{name: "pipeline", command: "printf ok | tap list", deny: true},
		{name: "boolean pipeline", command: "printf ok && keg list", deny: true},
		{name: "nested shell", command: `sh -c 'tap list | head'`, deny: true},
		{name: "double quoted nested shell", command: `bash -c "keg cat 1"`, deny: true},
		{name: "help long", command: "tap --help", deny: false},
		{name: "help short", command: "keg -h", deny: false},
		{name: "version long", command: "tap --version", deny: false},
		{name: "version short", command: "keg -v", deny: false},
		{name: "completion", command: "tap completion zsh", deny: false},
		{name: "quoted command text", command: `echo "tap list && keg cat 1"`, deny: false},
		{name: "substring", command: "taproom list", deny: false},
		{name: "lowercase assignment is not shell env prefix", command: "foo=bar tap list", deny: false},
		{name: "unbalanced quote fails open", command: `echo 'tap list`, deny: false},
		{name: "shell recursion is one level", command: `sh -c "sh -c 'tap list'"`, deny: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.deny, hookCommandDenied(tc.command, 0))
		})
	}
}

func TestHookPreToolUse_Protocol(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		input    string
		exitCode int
		deny     bool
	}{
		{name: "deny", input: `{"hook_event_name":"PreToolUse","tool_input":{"command":"tap list"}}`, deny: true},
		{name: "allow", input: `{"tool_input":{"command":"tap --help"}}`},
		{name: "missing tool input", input: `{}`},
		{name: "missing command", input: `{"tool_input":{}}`},
		{name: "non-string command", input: `{"tool_input":{"command":42}}`},
		{name: "empty stdin", input: "", exitCode: 2},
		{name: "malformed json", input: `{`, exitCode: 2},
		{name: "non-object json", input: `[]`, exitCode: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sb := newTestSandbox(t)
			proc := hookProcess(t, TapProfile(), "hook", "pre-tool-use")
			proc.SetStdin(strings.NewReader(tc.input))
			res := proc.Run(sb.Context(), sb.Runtime())
			require.Equal(t, tc.exitCode, res.ExitCode, "stderr=%s", res.Stderr)
			if tc.exitCode != 0 {
				require.Error(t, res.Err)
				require.Empty(t, res.Stdout)
				require.Contains(t, string(res.Stderr), "hook pre-tool-use")
				return
			}
			require.NoError(t, res.Err)
			if !tc.deny {
				require.Empty(t, res.Stdout)
				return
			}
			var output struct {
				HookSpecificOutput struct {
					HookEventName      string `json:"hookEventName"`
					PermissionDecision string `json:"permissionDecision"`
				} `json:"hookSpecificOutput"`
			}
			require.NoError(t, json.Unmarshal(res.Stdout, &output))
			require.Equal(t, "PreToolUse", output.HookSpecificOutput.HookEventName)
			require.Equal(t, "deny", output.HookSpecificOutput.PermissionDecision)
		})
	}
}

func TestHookSessionStart_EmitsOrientationForLifecycleSources(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"startup", "resume", "clear", "compact"} {
		t.Run(source, func(t *testing.T) {
			sb := newTestSandbox(t)
			proc := hookProcess(t, TapProfile(), "hook", "session-start")
			proc.SetStdin(strings.NewReader(`{"hook_event_name":"SessionStart","source":"` + source + `"}`))
			res := proc.Run(sb.Context(), sb.Runtime())
			require.NoError(t, res.Err, "stderr=%s", res.Stderr)
			var output struct {
				HookSpecificOutput struct {
					HookEventName     string `json:"hookEventName"`
					AdditionalContext string `json:"additionalContext"`
				} `json:"hookSpecificOutput"`
			}
			require.NoError(t, json.Unmarshal(res.Stdout, &output))
			require.Equal(t, "SessionStart", output.HookSpecificOutput.HookEventName)
			for _, want := range []string{
				"mcp__tapper__orient", "flight and KEG instructions", "Tapper MCP connection is unavailable",
				"reconnect or restart the host session", "never kill host-owned processes",
				"mcp__tapper__node_snapshot", "never read or write tapper node storage files directly",
				"Direct `tap` / `keg` CLI use",
			} {
				require.Contains(t, output.HookSpecificOutput.AdditionalContext, want)
			}
		})
	}
}

func TestHookSessionStart_FailsOpen(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	proc := hookProcess(t, TapProfile(), "hook", "session-start")
	proc.SetStdin(strings.NewReader("not-json"))
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Zero(t, res.ExitCode)
	require.Empty(t, res.Stdout)
	require.Contains(t, string(res.Stderr), "allowing session startup")
}

func TestHookCommands_ProfileGateAndHidden(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	tapRoot := NewRootCmd(&Deps{Profile: TapProfile(), Runtime: sb.Runtime()})
	hook, _, err := tapRoot.Find([]string{"hook"})
	require.NoError(t, err)
	require.True(t, hook.Hidden)
	require.True(t, commandNames(t, sb.Runtime(), TapProfile())["integrate"])
	require.True(t, commandNames(t, sb.Runtime(), TapProfile())["hook"])
	require.False(t, commandNames(t, sb.Runtime(), KegProfile())["integrate"])
	require.False(t, commandNames(t, sb.Runtime(), KegProfile())["hook"])
}

func TestHookCommands_BypassRootInitialization(t *testing.T) {
	sb := newTestSandbox(t)
	badConfig := "/home/testuser/bad-config.yaml"
	logFile := "/home/testuser/hook.log"
	require.NoError(t, sb.Runtime().WriteFile(badConfig, []byte("not: [yaml"), 0o644))

	srv, refreshCalls := countingRefreshHub(t)
	now := sb.Runtime().Clock().Now()
	seedAuthStore(t, sb, map[string]tapper.AuthEntry{
		srv.URL: {
			AccessToken: "expired", ExpiresAt: now.Add(-time.Hour),
			RefreshToken: "refresh", ClientID: "tapper-cli", TokenEndpoint: srv.URL,
		},
	})

	proc := hookProcess(t, TapProfile(), "--strict", "--config", badConfig, "--log-json", "--log-file", logFile, "hook", "session-start")
	proc.SetStdin(strings.NewReader(`{"hook_event_name":"SessionStart","source":"startup"}`))
	res := proc.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "stderr=%s", res.Stderr)
	require.Empty(t, res.Stderr)
	require.Equal(t, int64(0), refreshCalls.Load())
	_, err := sb.Runtime().Stat(filepath.Clean(logFile), false)
	require.Error(t, err, "hook invocation must not initialize or write the CLI log")
}

func TestSplitHookWords_ReconstructsQuotedFragments(t *testing.T) {
	words, err := splitHookWords(`t""ap 'hello world'`)
	require.NoError(t, err)
	require.Equal(t, []string{"tap", "hello world"}, words)
}

func TestRunSessionStartHook_WriteFailureIsFailOpen(t *testing.T) {
	sb := newTestSandbox(t)
	var stderr bytes.Buffer
	stream := sb.Runtime().Stream()
	stream.In = strings.NewReader(`{"hook_event_name":"SessionStart"}`)
	stream.Out = failingWriter{}
	stream.Err = &stderr
	require.NoError(t, sb.Runtime().SetStream(stream))
	runSessionStartHook(sb.Runtime())
	require.Contains(t, stderr.String(), "allowing session startup")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, context.Canceled }
