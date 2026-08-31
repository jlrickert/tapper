package cli_test

import (
	"testing"

	tu "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

const launchConfig = `fallbackNamespace: local
flight: "@testuser/+root"
defaultHub: atlas
hubs:
  atlas:
    kind: remote
    url: https://atlas.example.test
agents:
  opus:
    model: anthropic/claude-opus-4
    flight: +dev
  local:
    model: ollama/qwen3.6:35b-mlx
    flight: "@testuser/+scratch"
  lab:
    model: ollama/qwen3.6:35b-mlx
    baseUrl: http://192.168.50.197:11434/v1
    flight: "@testuser/+scratch"
  sub:
    model: anthropic/claude-opus-4
    auth: subscription
    flight: +dev
`

func newLaunchSandbox(t *testing.T) *tu.Sandbox {
	t.Helper()
	sb := NewSandbox(t)
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/.config/tapper/config.yaml", []byte(launchConfig), 0o644))
	return sb
}

func TestLaunchCommand_DryRunResolvesOllamaThroughOpenAI(t *testing.T) {
	t.Parallel()
	sb := newLaunchSandbox(t)

	res := NewProcess(t, false, "launch", "codex", "--agent", "local", "--dry-run").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	require.Contains(t, out, "agent local -> ollama/qwen3.6:35b-mlx")
	require.Contains(t, out, "flight: @testuser/+root (connection-pinned root)")
	require.Contains(t, out, "codex --oss --local-provider ollama --model qwen3.6:35b-mlx")
	require.Contains(t, out, "CODEX_OSS_BASE_URL=http://localhost:11434/v1")

	require.Contains(t, out, "TAP_AGENT=local")
	require.Contains(t, out, "TAP_FLIGHT=@testuser/+root")
}

func TestLaunchCommand_DryRunResolvesAnthropicThroughEnv(t *testing.T) {
	t.Parallel()
	sb := newLaunchSandbox(t)

	res := NewProcess(t, false, "launch", "claude", "--agent", "opus", "--dry-run").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	require.Contains(t, out, "ANTHROPIC_MODEL=claude-opus-4")
	require.Contains(t, out, "TAP_AGENT=opus")
	require.Contains(t, out, "TAP_FLIGHT=@testuser/+root")
}

func TestLaunchCommand_DryRunHonorsExplicitFlight(t *testing.T) {
	t.Parallel()
	sb := newLaunchSandbox(t)
	require.NoError(t, sb.Runtime().Env().Set("TAP_FLIGHT", "@environment/+root"))

	res := NewProcess(t, false, "launch", "claude", "--agent", "opus", "--dry-run",
		"--flight", "@admin/+mcp-smoke-root").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	require.Contains(t, out, "flight: @admin/+mcp-smoke-root (connection-pinned root)")
	require.Contains(t, out, "TAP_FLIGHT=@admin/+mcp-smoke-root")
	require.NotContains(t, out, "@environment/+root")
	require.NotContains(t, out, "@testuser/+root")
}

func TestLaunchCommand_DryRunPassesThroughExtraArgs(t *testing.T) {
	t.Parallel()
	sb := newLaunchSandbox(t)

	res := NewProcess(t, false, "launch", "codex", "--agent", "local", "--dry-run",
		"--", "--sandbox", "read-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "codex --oss --local-provider ollama --model qwen3.6:35b-mlx --sandbox read-only")
}

// Ollama serves the Anthropic Messages API as well, so Claude Code can drive it
// once ANTHROPIC_BASE_URL points at the server — minus the /v1 suffix, which
// the client appends itself.
func TestLaunchCommand_DryRunDrivesOllamaFromClaude(t *testing.T) {
	t.Parallel()
	sb := newLaunchSandbox(t)

	res := NewProcess(t, false, "launch", "claude", "--agent", "lab", "--dry-run").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	require.Contains(t, out, "ANTHROPIC_BASE_URL=http://192.168.50.197:11434")
	require.NotContains(t, out, "ANTHROPIC_BASE_URL=http://192.168.50.197:11434/v1")
	require.Contains(t, out, "ANTHROPIC_MODEL=qwen3.6:35b-mlx")
}

func TestLaunchCommand_ErrorsBeforeLaunchingOnIncompatiblePair(t *testing.T) {
	t.Parallel()
	sb := newLaunchSandbox(t)

	// A dry run is not needed to prove this: resolution fails first, so no
	// harness is ever started. Codex speaks the OpenAI protocol only.
	res := NewProcess(t, false, "launch", "codex", "--agent", "opus").
		Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "cannot use a anthropic model")
}

func TestLaunchCommand_DryRunReportsSubscriptionStrip(t *testing.T) {
	t.Parallel()
	sb := newLaunchSandbox(t)

	res := NewProcess(t, false, "launch", "claude", "--agent", "sub", "--dry-run").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	require.Contains(t, out, "auth: subscription")
	require.Contains(t, out, "unset: ANTHROPIC_API_KEY (inherited)")
}

// Launching with no flight is the bootstrap path: it must resolve rather than
// error, report no pinned root, and leave TAP_FLIGHT unset so the harness's
// `tap mcp` resolves identity authority. The warning goes to stderr so it cannot
// corrupt a piped dry-run report.
func TestLaunchCommand_DryRunWithoutFlightWarnsAndPinsNothing(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/.config/tapper/config.yaml", []byte(`fallbackNamespace: local
defaultHub: atlas
hubs:
  atlas:
    kind: remote
    url: https://atlas.example.test
agents:
  opus:
    model: anthropic/claude-opus-4
`), 0o644))

	res := NewProcess(t, false, "launch", "claude", "--agent", "opus", "--dry-run").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	require.Contains(t, out, "agent opus -> anthropic/claude-opus-4")
	require.NotContains(t, out, "connection-pinned root")
	require.NotContains(t, out, "TAP_FLIGHT")
	require.Contains(t, out, "TAP_AGENT=opus")

	require.Contains(t, string(res.Stderr), "identity-authorized full access")
	require.NotContains(t, out, "identity-authorized full access",
		"the warning belongs on stderr, clear of the dry-run report")
}

func TestLaunchCommand_ErrorsOnUnknownAgent(t *testing.T) {
	t.Parallel()
	sb := newLaunchSandbox(t)

	res := NewProcess(t, false, "launch", "codex", "--agent", "nope", "--dry-run").
		Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), `unknown agent "nope"`)
}
