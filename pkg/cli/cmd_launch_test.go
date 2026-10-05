package cli_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	tu "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

// newLaunchSandbox configures a sandbox whose hub serves a one-model
// catalog, with extra appended to the user config.
func newLaunchSandbox(t *testing.T, extra string) *tu.Sandbox {
	t.Helper()
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference/openai/v1/models" || r.Header.Get("Authorization") != "Bearer hub-token" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"@me/qwen3:8b","object":"model","owned_by":"pool:@me","contributors":2,"context_window":32768,"capabilities":["chat","stream"]}]}`)
	}))
	t.Cleanup(hub.Close)
	sb := NewSandbox(t)
	cfg := fmt.Sprintf("hub: atlas\nhubs:\n  atlas: {url: %s, token: hub-token}\n%s", hub.URL, extra)
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(cfg), 0o644))
	return sb
}

func TestLaunchCommand_DryRunClaude(t *testing.T) {
	t.Parallel()
	sb := newLaunchSandbox(t, "flight: \"@testuser/+root\"\n")

	res := NewProcess(t, false, "launch", "claude", "--hub", "--dry-run", "--", "--verbose").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	out := string(res.Stdout)
	require.Contains(t, out, "hub atlas -> @me/qwen3:8b (via loopback forwarder)")
	require.Contains(t, out, "flight: @testuser/+root (connection-pinned root)")
	require.Contains(t, out, "unset: ANTHROPIC_API_KEY (inherited)")
	require.Contains(t, out, `claude --model @me/qwen3:8b --settings {"modelPicker":{"options":[{"model":"@me/qwen3:8b"`)
	require.Contains(t, out, `}]}} --verbose`, "passthrough arguments follow the launcher's own")
	require.Contains(t, out, "ANTHROPIC_BASE_URL=http://127.0.0.1:<port>/anthropic")
	require.Contains(t, out, "ANTHROPIC_AUTH_TOKEN=<launch key>")
	require.Contains(t, out, "TAP_HARNESS=claude")
	require.Contains(t, out, "TAP_MODEL=@me/qwen3:8b")
	require.Contains(t, out, "TAP_FLIGHT=@testuser/+root")
	require.NotContains(t, out, "hub-token", "the Hub credential never appears")
}

// Claude Code defaults to split mode: its own login for Claude models, Hub's
// catalog beside them, and no Hub key in its environment.
func TestLaunchCommand_DryRunClaudeSplit(t *testing.T) {
	t.Parallel()
	sb := newLaunchSandbox(t, "flight: \"@testuser/+root\"\n")

	res := NewProcess(t, false, "launch", "claude", "--dry-run").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	out := string(res.Stdout)
	require.Contains(t, out, "split: Claude models via your Claude login, hub atlas models via loopback forwarder; starting on Claude Code's default")
	require.Contains(t, out, `"replaceBuiltInOptions":false`)
	require.Contains(t, out, "ANTHROPIC_BASE_URL=http://127.0.0.1:<port>/t/<launch key>/anthropic")
	require.NotContains(t, out, "ANTHROPIC_AUTH_TOKEN")
	require.NotContains(t, out, "unset: ANTHROPIC_API_KEY")
	require.NotContains(t, out, "hub-token")

	res = NewProcess(t, false, "launch", "claude", "--subscription", "--dry-run").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "subscription: models via your Claude login")
	require.NotContains(t, string(res.Stdout), "ANTHROPIC_BASE_URL")

	res = NewProcess(t, false, "launch", "claude", "--hub", "--subscription", "--dry-run").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err, "--hub and --subscription conflict")
}

func TestLaunchCommand_DryRunShowsGeneratedFiles(t *testing.T) {
	t.Parallel()
	sb := newLaunchSandbox(t, "")

	res := NewProcess(t, false, "launch", "pi", "--dry-run").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	out := string(res.Stdout)
	require.Contains(t, out, "pi -e <launch dir>/foldwise-pi.ts --provider foldwise --model @me/qwen3:8b")
	require.Contains(t, out, "Writing <launch dir>/foldwise-pi.ts:")
	require.Contains(t, out, `pi.registerProvider("foldwise"`)
	require.Contains(t, string(res.Stderr), "no flight configured", "a no-flight launch warns")
}

func TestLaunchCommand_Errors(t *testing.T) {
	t.Parallel()
	sb := newLaunchSandbox(t, "")

	res := NewProcess(t, false, "launch", "codex", "--agent", "opus", "--dry-run").Run(sb.Context(), sb.Runtime())
	require.ErrorContains(t, res.Err, "must be @namespace/name", "local configured agents remain retired")

	res = NewProcess(t, false, "launch", "codex", "--model", "@other/x", "--dry-run").Run(sb.Context(), sb.Runtime())
	require.ErrorContains(t, res.Err, "is a relay serving it connected")

	res = NewProcess(t, false, "launch", "emacs", "--dry-run").Run(sb.Context(), sb.Runtime())
	require.ErrorContains(t, res.Err, "unknown harness")
}
