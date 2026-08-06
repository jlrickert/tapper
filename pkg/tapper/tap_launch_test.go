package tapper_test

import (
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// newLaunchTap builds a Tap over a sandbox seeded with the given user config.
func newLaunchTap(t *testing.T, userConfig string) *tapper.Tap {
	t.Helper()
	sb := sandbox.NewSandbox(t, &sandbox.Options{
		Home: "/home/testuser",
		User: "testuser",
	})
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/.config/tapper/config.yaml", []byte(userConfig), 0o644))
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	return tap
}

const launchUserConfig = `fallbackNamespace: local
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
  bare:
    model: claude-opus-4
    flight: +dev
  hosted:
    model: openai/gpt-5
  sub:
    model: anthropic/claude-opus-4
    auth: subscription
  work:
    model: openai/gpt-5
    apiKeyEnv: WORK_OPENAI_KEY
  badauth:
    model: openai/gpt-5
    auth: nonsense
  localsub:
    model: ollama/qwen3.6:35b-mlx
    auth: subscription
  capped:
    model: ollama/qwen3.6:35b-mlx
    contextWindow: 150000
    args: ['--search']
`

func TestParseAgentModel(t *testing.T) {
	t.Parallel()

	provider, model, err := tapper.ParseAgentModel("ollama/qwen3.6:35b")
	require.NoError(t, err)
	require.Equal(t, tapper.ProviderOllama, provider)
	// The model id keeps its own colons; only the first slash is the split.
	require.Equal(t, "qwen3.6:35b", model)

	provider, model, err = tapper.ParseAgentModel("anthropic/claude-opus-4")
	require.NoError(t, err)
	require.Equal(t, tapper.ProviderAnthropic, provider)
	require.Equal(t, "claude-opus-4", model)

	// An unqualified model is rejected rather than guessed at, because the
	// provider decides which protocol the harness must speak.
	_, _, err = tapper.ParseAgentModel("claude-opus-4")
	require.Error(t, err)
	require.Contains(t, err.Error(), "provider-qualified")

	_, _, err = tapper.ParseAgentModel("bedrock/some-model")
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown model provider")

	_, _, err = tapper.ParseAgentModel("")
	require.Error(t, err)
}

func TestResolveLaunch_AnthropicOnClaude(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	got, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "opus"})
	require.NoError(t, err)

	require.Equal(t, tapper.ProviderAnthropic, got.Provider)
	require.Equal(t, "claude-opus-4", got.Model)
	// Claude Code takes its model through the environment, not a flag.
	require.Equal(t, []string{"claude"}, got.Argv)
	require.Equal(t, "claude-opus-4", got.Env["ANTHROPIC_MODEL"])
	// The agent, not the flight it currently names: the child re-resolves the
	// flight on every load so a config edit can move a running session.
	require.Equal(t, "opus", got.Env["TAP_AGENT"])
	require.NotContains(t, got.Env, "TAP_FLIGHT")
}

// Codex has first-class local-provider support and configures it through
// --oss/--local-provider plus CODEX_OSS_BASE_URL. It ignores OPENAI_BASE_URL,
// and an OPENAI_API_KEY would push it into API-key billing against the wrong
// provider — `codex doctor` calls that "mixed auth signals".
func TestResolveLaunch_OllamaOnCodexUsesOSSProvider(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	got, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "codex", Agent: "local"})
	require.NoError(t, err)

	require.Equal(t, tapper.ProviderOllama, got.Provider)
	require.Equal(t,
		[]string{"codex", "--oss", "--local-provider", "ollama", "--model", "qwen3.6:35b-mlx"},
		got.Argv)
	require.Equal(t, "http://localhost:11434/v1", got.Env["CODEX_OSS_BASE_URL"])
	require.NotContains(t, got.Env, "OPENAI_BASE_URL")
	require.NotContains(t, got.Env, "OPENAI_API_KEY")
	require.Equal(t, "local", got.Env["TAP_AGENT"])
	require.NotContains(t, got.Env, "TAP_FLIGHT")
}

func TestResolveLaunch_OpenAIOnCodexLeavesDefaultEndpoint(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	got, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "codex", Agent: "hosted"})
	require.NoError(t, err)

	require.Equal(t, []string{"codex", "--model", "gpt-5"}, got.Argv)
	require.NotContains(t, got.Env, "OPENAI_BASE_URL")
	// An agent may omit its flight. The agent is still exported — resolution
	// simply finds no flight on it and falls through to project/user config.
	require.Equal(t, "hosted", got.Env["TAP_AGENT"])
	require.NotContains(t, got.Env, "TAP_FLIGHT")
}

// Ollama serves both /v1/messages and /v1/chat/completions, so it is the one
// provider every harness can drive. Claude Code needs the base URL without the
// /v1 suffix because it appends /v1/messages itself.
func TestResolveLaunch_OllamaOnClaudeUsesAnthropicProtocol(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	got, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "local"})
	require.NoError(t, err)

	require.Equal(t, []string{"claude"}, got.Argv)
	require.Equal(t, "qwen3.6:35b-mlx", got.Env["ANTHROPIC_MODEL"])
	require.Equal(t, "http://localhost:11434", got.Env["ANTHROPIC_BASE_URL"])
	require.Equal(t, "ollama", got.Env["ANTHROPIC_API_KEY"])
}

// One configured baseUrl is correct for both protocols: the launcher adds the
// /v1 suffix for OpenAI clients and removes it for Anthropic ones.
func TestResolveLaunch_BaseURLNormalizesPerProtocol(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	viaClaude, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "lab"})
	require.NoError(t, err)
	require.Equal(t, "http://192.168.50.197:11434", viaClaude.Env["ANTHROPIC_BASE_URL"])

	viaCodex, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "codex", Agent: "lab"})
	require.NoError(t, err)
	require.Equal(t, "http://192.168.50.197:11434/v1", viaCodex.Env["CODEX_OSS_BASE_URL"])
}

func TestResolveLaunch_RejectsIncompatibleProvider(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	// Codex speaks the OpenAI protocol and the Anthropic API is not that, so
	// this pair stays refused.
	_, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "codex", Agent: "opus"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot use a anthropic model")

	// Symmetrically, Claude Code cannot drive a hosted OpenAI model.
	_, err = tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "hosted"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot use a openai model")
}

// A local model must never cause real credentials to be sent to it. The
// placeholder key is what stops the harness falling back to a stored login and
// posting it to the ollama host, so no auth mode may suppress it — and asking
// for subscription auth on a local model is rejected rather than honoured.
func TestResolveLaunch_LocalModelNeverLeaksRealCredentials(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	got, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "local"})
	require.NoError(t, err)

	// Defaults to none for a local provider without the user asking.
	require.Equal(t, tapper.AuthNone, got.Auth)
	require.Equal(t, "ollama", got.Env["ANTHROPIC_API_KEY"])
	// And the ambient cloud credentials are removed rather than passed along.
	require.Contains(t, got.StripEnv, "OPENAI_API_KEY")
	require.Contains(t, got.StripEnv, "ANTHROPIC_AUTH_TOKEN")

	_, err = tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "localsub"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "meaningless for a local ollama model")
}

func TestResolveLaunch_SubscriptionStripsInheritedKeys(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	got, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "sub"})
	require.NoError(t, err)

	require.Equal(t, tapper.AuthSubscription, got.Auth)
	// Absence of a key cannot express "use my login", because absence means
	// inherit. The inherited variables must be actively removed.
	require.Contains(t, got.StripEnv, "ANTHROPIC_API_KEY")
	require.Contains(t, got.StripEnv, "ANTHROPIC_AUTH_TOKEN")
	require.NotContains(t, got.Env, "ANTHROPIC_API_KEY")
}

func TestResolveLaunch_APIKeyEnvForwardsByName(t *testing.T) {
	t.Parallel()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/.config/tapper/config.yaml", []byte(launchUserConfig), 0o644))
	require.NoError(t, sb.Runtime().Env().Set("WORK_OPENAI_KEY", "sk-secret-value"))
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)

	got, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "codex", Agent: "work"})
	require.NoError(t, err)

	require.Equal(t, "sk-secret-value", got.Env["OPENAI_API_KEY"])
	// The reported source is the variable name, so it stays safe to print.
	require.Equal(t, "WORK_OPENAI_KEY", got.KeySource)
}

func TestResolveLaunch_APIKeyEnvErrorsWhenUnset(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	_, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "codex", Agent: "work"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "WORK_OPENAI_KEY")
}

func TestResolveLaunch_RejectsBadAuthMode(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	_, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "codex", Agent: "badauth"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown auth mode")
}

func TestResolveLaunch_ErrorsOnUnknownInputs(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	_, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "nope", Agent: "opus"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown harness")

	_, err = tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "missing"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown agent")

	_, err = tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "an agent is required")

	_, err = tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "bare"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "provider-qualified")
}

func TestResolveLaunch_AppendsPassthroughArgs(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	got, err := tap.ResolveLaunch(tapper.LaunchOptions{
		Harness: "codex", Agent: "hosted", Args: []string{"--sandbox", "read-only"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"codex", "--model", "gpt-5", "--sandbox", "read-only"}, got.Argv)
}

func TestResolveLaunch_ReadsAgentsFromProjectConfig(t *testing.T) {
	t.Parallel()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	require.NoError(t, sb.Setwd("/home/testuser/work/project"))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/.config/tapper/config.yaml", []byte("fallbackNamespace: local\n"), 0o644))
	// Agents carry no credentials, so unlike hubs they survive the project
	// config's trust boundary.
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/work/project/.tapper/config.yaml",
		[]byte("agents:\n  proj:\n    model: openai/gpt-5\n    flight: +proj\n"), 0o644))

	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)

	got, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "codex", Agent: "proj"})
	require.NoError(t, err)
	require.Equal(t, "gpt-5", got.Model)
	require.Equal(t, "proj", got.Env["TAP_AGENT"])
	// Still reported, so a dry run can show what the agent currently points at.
	require.Equal(t, "+proj", got.Flight)
}

// A context cap means the same thing to a user on either harness but is spelled
// differently by each, so the launcher translates rather than passing a raw
// flag through.
func TestResolveLaunch_ContextWindowTranslatesPerHarness(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	viaCodex, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "codex", Agent: "capped"})
	require.NoError(t, err)
	require.Contains(t, viaCodex.Argv, "model_context_window=150000")
	// Agent args ride along, before any one-off passed at the call site.
	require.Contains(t, viaCodex.Argv, "--search")

	viaClaude, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "capped"})
	require.NoError(t, err)
	require.Contains(t, viaClaude.Argv, "--autocompact")
	require.Contains(t, viaClaude.Argv, "150000")

	// pi has no known equivalent, so the cap is reported rather than dropped —
	// silently ignoring it is how you find out later that it never applied.
	_, err = tap.ResolveLaunch(tapper.LaunchOptions{Harness: "pi", Agent: "capped"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no way to apply it")
}

func TestLaunchHarnesses(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"claude", "codex", "pi"}, tapper.LaunchHarnesses())
}
