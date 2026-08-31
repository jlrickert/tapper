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
	require.Equal(t, "opus", got.Env["TAP_AGENT"])
	require.Equal(t, "@testuser/+root", got.Env["TAP_FLIGHT"])
	require.Equal(t, "@testuser/+root", got.Flight)
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
	require.Equal(t, "@testuser/+root", got.Env["TAP_FLIGHT"])
}

func TestResolveLaunch_OpenAIOnCodexLeavesDefaultEndpoint(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	got, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "codex", Agent: "hosted"})
	require.NoError(t, err)

	require.Equal(t, []string{"codex", "--model", "gpt-5"}, got.Argv)
	require.NotContains(t, got.Env, "OPENAI_BASE_URL")
	// An agent may omit its legacy flight field; the root is independent.
	require.Equal(t, "hosted", got.Env["TAP_AGENT"])
	require.Equal(t, "@testuser/+root", got.Env["TAP_FLIGHT"])
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

	// launchUserConfig sets no top-level agent, so there is no default to fall
	// back to and the omission is an error.
	_, err = tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "an agent is required")

	_, err = tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "bare"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "provider-qualified")
}

// TestResolveLaunch_AgentDefaultsToConfig pins the fallback: --agent wins, and
// omitting it falls back to the top-level agent key, mirroring how flight
// supplies the launch root.
func TestResolveLaunch_AgentDefaultsToConfig(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, "agent: opus\n"+launchUserConfig)

	got, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude"})
	require.NoError(t, err)
	require.Equal(t, "opus", got.Agent)

	// An explicit --agent still overrides the configured default.
	got, err = tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "local"})
	require.NoError(t, err)
	require.Equal(t, "local", got.Agent)

	// A default naming an entry that does not exist fails like any other
	// unknown agent rather than being silently ignored.
	missing := newLaunchTap(t, "agent: ghost\n"+launchUserConfig)
	_, err = missing.ResolveLaunch(tapper.LaunchOptions{Harness: "claude"})
	require.Error(t, err)
	require.Contains(t, err.Error(), `unknown agent "ghost"`)
}

// TestResolveLaunch_AgentDefaultsFromEnv covers the other feed into the same
// key: TAP_AGENT, which is what a launched process inherits.
func TestResolveLaunch_AgentDefaultsFromEnv(t *testing.T) {
	t.Parallel()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/.config/tapper/config.yaml", []byte(launchUserConfig), 0o644))
	require.NoError(t, sb.Runtime().Set("TAP_AGENT", "local"))

	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)

	got, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude"})
	require.NoError(t, err)
	require.Equal(t, "local", got.Agent)
}

// A flight is optional. Requiring one made bootstrapping impossible: creating
// the first flight needs an agent session, and launching that session needed a
// flight. Without one the child gets no TAP_FLIGHT, which is precisely how its
// `tap mcp` decides it is not launcher-bound and resolves identity authority.
func TestResolveLaunch_LaunchesWithoutFlight(t *testing.T) {
	t.Parallel()

	tap := newLaunchTap(t, `agents:
  opus: {model: anthropic/claude-opus-4}
`)
	got, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "opus"})
	require.NoError(t, err)
	require.Empty(t, got.Flight)
	require.NotContains(t, got.Env, "TAP_FLIGHT",
		"a no-flight launch must not pin a root, or the child reports itself launcher-bound")
	require.Equal(t, "opus", got.Env["TAP_AGENT"])
	require.Len(t, got.Warnings, 1)
	require.Contains(t, got.Warnings[0], "full access")
}

func TestResolveLaunch_RequiresHubBackedRoot(t *testing.T) {
	t.Parallel()

	local := newLaunchTap(t, `flight: "@local/+dev"
defaultHub: home
hubs:
  home: {kind: local, basePath: /home/testuser/kegs, defaultNamespace: local}
agents:
  opus: {model: anthropic/claude-opus-4}
`)
	_, err := local.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "opus"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported kind \"local\"")
}

// A configured root is still pinned immutably for the child's lifetime.
func TestResolveLaunch_PinsConfiguredRoot(t *testing.T) {
	t.Parallel()
	tap := newLaunchTap(t, launchUserConfig)

	got, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "claude", Agent: "opus"})
	require.NoError(t, err)
	require.NotEmpty(t, got.Flight)
	require.Equal(t, got.Flight, got.Env["TAP_FLIGHT"])
	require.Empty(t, got.Warnings)
}

func TestResolveLaunch_ExplicitFlightOverridesCascade(t *testing.T) {
	t.Parallel()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	require.NoError(t, sb.Setwd("/home/testuser/work/project"))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/.config/tapper/config.yaml", []byte(`flight: "@user/+root"
defaultHub: atlas
hubs:
  atlas: {kind: remote, url: https://atlas.example.test}
agents:
  opus: {model: anthropic/claude-opus-4}
`), 0o644))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/work/project/.tapper/config.yaml",
		[]byte("flight: '@project/+root'\n"), 0o644))
	require.NoError(t, sb.Runtime().Env().Set("TAP_FLIGHT", "@environment/+root"))

	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	got, err := tap.ResolveLaunch(tapper.LaunchOptions{
		Harness: "claude", Agent: "opus", Flight: "@explicit/+root",
	})
	require.NoError(t, err)
	require.Equal(t, "@explicit/+root", got.Flight)
	require.Equal(t, "@explicit/+root", got.Env["TAP_FLIGHT"])
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
		"/home/testuser/.config/tapper/config.yaml", []byte("flight: '@testuser/+root'\ndefaultHub: atlas\nhubs:\n  atlas: {kind: remote, url: https://atlas.example.test}\n"), 0o644))
	// Agents carry no credentials, so unlike hubs they survive the project
	// config's trust boundary.
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/work/project/.tapper/config.yaml",
		[]byte("agents:\n  proj:\n    model: openai/gpt-5\n    flight: +ignored\n"), 0o644))

	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)

	got, err := tap.ResolveLaunch(tapper.LaunchOptions{Harness: "codex", Agent: "proj"})
	require.NoError(t, err)
	require.Equal(t, "gpt-5", got.Model)
	require.Equal(t, "proj", got.Env["TAP_AGENT"])
	require.Equal(t, "@testuser/+root", got.Flight)
	require.Equal(t, "@testuser/+root", got.Env["TAP_FLIGHT"])
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
