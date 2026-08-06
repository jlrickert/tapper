package tapper

// EXPERIMENTAL — `tap launch` is a scaffold for exercising Tapper against a
// chosen model and flight without editing config between runs. It is
// deliberately undocumented: agents are expected to move to Tapper Hub, at
// which point this file and its config shape are torn out and redesigned.
// Nothing else in the package should grow a dependency on it.

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Providers understood by the launcher, parsed from an agent model's prefix.
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
	ProviderOllama    = "ollama"
)

// defaultOllamaBaseURL is the local Ollama server. Ollama serves BOTH the
// OpenAI protocol (/v1/chat/completions) and the Anthropic Messages protocol
// (/v1/messages), which is why it is the one provider every harness can use.
const defaultOllamaBaseURL = "http://localhost:11434/v1"

// Auth modes for an agent, selecting where the harness gets its credentials.
const (
	// AuthInherit passes the ambient environment through untouched. It is the
	// default because it matches running the harness bare in your shell.
	AuthInherit = "inherit"
	// AuthSubscription strips inherited provider key variables so the harness
	// falls back to its own stored login. Absence of a key cannot express this
	// on its own, because absence means inherit.
	AuthSubscription = "subscription"
	// AuthAPIKey forwards the variable named by an agent's apiKeyEnv.
	AuthAPIKey = "apiKey"
	// AuthNone means the model needs no credential of ours. It strips the same
	// inherited variables as AuthSubscription but does not imply a stored login
	// to fall back to, which is what a local provider actually wants — and it
	// leaves the placeholder key in place so the harness cannot fall back at
	// all. It is the default for ollama models.
	AuthNone = "none"
)

// providerKeyEnv lists the credential variables each provider's clients read.
// AuthSubscription and AuthNone remove these from the child environment.
var providerKeyEnv = map[string][]string{
	ProviderAnthropic: {"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"},
	ProviderOpenAI:    {"OPENAI_API_KEY"},
	ProviderOllama:    {"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"},
}

// LaunchOptions configures behavior for Tap.Launch.
type LaunchOptions struct {
	// Harness names the agent CLI to start: claude, codex, or pi.
	Harness string
	// Agent names an entry in the config's agents map.
	Agent string
	// DryRun resolves and reports the invocation without executing it.
	DryRun bool
	// Args are extra arguments appended to the harness invocation.
	Args []string
}

// LaunchResult reports the resolved invocation. Env holds only the overlay
// applied on top of the inherited environment; StripEnv names variables removed
// from it. Neither contains a secret value — a forwarded key is reported by the
// variable it came from.
//
// Flight is what the agent points at right now, reported for the operator's
// benefit. It is not what gets exported: the child resolves the flight itself
// from TAP_AGENT, so this value can go stale the moment the config changes.
type LaunchResult struct {
	Harness   string
	Agent     string
	Provider  string
	Model     string
	BaseURL   string
	Flight    string
	Auth      string
	Argv      []string
	Env       map[string]string
	StripEnv  []string
	KeySource string
}

// launchSpec is one resolved agent, handed to a harness builder.
type launchSpec struct {
	provider string
	model    string
	baseURL  string
	apiKey   string
	auth     string
}

// harnessAdapter maps a provider onto an invocation for one agent CLI. Absence
// from providers means the harness cannot speak that provider's protocol, which
// is reported rather than launched.
type harnessAdapter struct {
	command   string
	providers map[string]func(spec launchSpec) ([]string, map[string]string)
	// contextWindowArgs renders an agent's contextWindow into this harness's
	// own flags. Nil means the harness has no equivalent, in which case a
	// configured contextWindow is reported rather than silently dropped —
	// quietly ignoring a context cap is how you discover it never applied.
	contextWindowArgs func(tokens int) []string
}

// openAIBaseURL normalizes a base URL for OpenAI clients, which append
// /chat/completions and therefore expect the /v1 prefix present.
func openAIBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return ""
	}
	if strings.HasSuffix(trimmed, "/v1") {
		return trimmed
	}
	return trimmed + "/v1"
}

// anthropicBaseURL normalizes a base URL for Anthropic clients, which append
// /v1/messages themselves and therefore expect the /v1 suffix absent. One
// configured baseUrl is thus correct for both protocols.
func anthropicBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	return strings.TrimSuffix(trimmed, "/v1")
}

// anthropicProtocol builds the invocation for harnesses speaking the Anthropic
// Messages API. Claude Code selects its model through the environment rather
// than a flag, so argv stays bare.
func anthropicProtocol(command string) func(launchSpec) ([]string, map[string]string) {
	return func(spec launchSpec) ([]string, map[string]string) {
		env := map[string]string{"ANTHROPIC_MODEL": spec.model}
		if base := anthropicBaseURL(spec.baseURL); base != "" {
			env["ANTHROPIC_BASE_URL"] = base
		}
		switch {
		case spec.apiKey != "":
			env["ANTHROPIC_API_KEY"] = spec.apiKey
		case spec.provider == ProviderOllama:
			// Unconditional for a local provider. Ollama ignores the value, but
			// without one the client falls back to its stored login and sends
			// real subscription credentials to a host that is not Anthropic.
			// This placeholder is the thing preventing that, so no auth mode may
			// switch it off.
			env["ANTHROPIC_API_KEY"] = "ollama"
		}
		return []string{command}, env
	}
}

// openAIProtocol builds the invocation for harnesses that take their endpoint
// and key from the conventional OPENAI_* environment variables.
//
// Codex does NOT: it configures providers through ~/.codex/config.toml and its
// own CODEX_OSS_* variables, and ignores OPENAI_BASE_URL entirely. Setting
// OPENAI_API_KEY there is actively harmful — `codex doctor` reports "mixed auth
// signals: ChatGPT login plus API key env var" and switches to API-key billing.
// Codex therefore has its own builders below.
func openAIProtocol(command string) func(launchSpec) ([]string, map[string]string) {
	return func(spec launchSpec) ([]string, map[string]string) {
		env := map[string]string{}
		if base := openAIBaseURL(spec.baseURL); base != "" {
			env["OPENAI_BASE_URL"] = base
		}
		switch {
		case spec.apiKey != "":
			env["OPENAI_API_KEY"] = spec.apiKey
		case spec.provider == ProviderOllama:
			// Unconditional, for the same reason as the Anthropic builder: the
			// placeholder is what stops the client reaching for a stored login
			// and sending it to a host that is not the provider.
			env["OPENAI_API_KEY"] = "ollama"
		}
		return []string{command, "--model", spec.model}, env
	}
}

// codexHosted drives Codex against OpenAI proper. Codex owns its own auth — a
// stored ChatGPT login or OPENAI_API_KEY from the environment — so nothing is
// injected here beyond the model.
func codexHosted(spec launchSpec) ([]string, map[string]string) {
	env := map[string]string{}
	if spec.apiKey != "" {
		env["OPENAI_API_KEY"] = spec.apiKey
	}
	return []string{"codex", "--model", spec.model}, env
}

// codexOSS drives Codex against a local Ollama server. Codex has first-class
// support for this through --oss/--local-provider and reads the endpoint from
// CODEX_OSS_BASE_URL, so the OPENAI_* variables are neither used nor set: an
// OPENAI_API_KEY here would only push Codex into API-key mode against the wrong
// provider.
func codexOSS(spec launchSpec) ([]string, map[string]string) {
	env := map[string]string{}
	if base := openAIBaseURL(spec.baseURL); base != "" {
		env["CODEX_OSS_BASE_URL"] = base
	}
	return []string{"codex", "--oss", "--local-provider", "ollama", "--model", spec.model}, env
}

func harnessAdapters() map[string]harnessAdapter {
	return map[string]harnessAdapter{
		"claude": {
			command: "claude",
			providers: map[string]func(launchSpec) ([]string, map[string]string){
				ProviderAnthropic: anthropicProtocol("claude"),
				// Ollama serves /v1/messages, so Claude Code works against it
				// once ANTHROPIC_BASE_URL points at the server.
				ProviderOllama: anthropicProtocol("claude"),
			},
			// Claude Code has no model-metadata override; the nearest thing is
			// the threshold at which it auto-compacts, which is what a context
			// cap means in practice. It accepts roughly 100k-1M.
			contextWindowArgs: func(tokens int) []string {
				return []string{"--autocompact", strconv.Itoa(tokens)}
			},
		},
		"codex": {
			command: "codex",
			providers: map[string]func(launchSpec) ([]string, map[string]string){
				ProviderOpenAI: codexHosted,
				ProviderOllama: codexOSS,
			},
			// Codex treats it as model metadata. Setting it also silences the
			// "model metadata not found, defaulting to fallback" warning for a
			// local tag Codex does not know.
			contextWindowArgs: func(tokens int) []string {
				return []string{"-c", "model_context_window=" + strconv.Itoa(tokens)}
			},
		},
		"pi": {
			command: "pi",
			providers: map[string]func(launchSpec) ([]string, map[string]string){
				ProviderOpenAI: openAIProtocol("pi"),
				ProviderOllama: openAIProtocol("pi"),
			},
		},
	}
}

// LaunchHarnesses returns the launchable harness names, sorted. It backs shell
// completion the way IntegrateHosts does for `tap integrate`.
func LaunchHarnesses() []string {
	adapters := harnessAdapters()
	out := make([]string, 0, len(adapters))
	for name := range adapters {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ParseAgentModel splits a provider-qualified model into its provider and model
// id. An unqualified model is an error rather than a guess, because the
// provider decides which protocol the harness must speak.
func ParseAgentModel(raw string) (provider, model string, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", fmt.Errorf("agent model is empty")
	}
	prefix, rest, ok := strings.Cut(trimmed, "/")
	if !ok || strings.TrimSpace(rest) == "" {
		return "", "", fmt.Errorf(
			"agent model %q must be provider-qualified, e.g. %s/<model>, %s/<model>, or %s/<model>",
			trimmed, ProviderAnthropic, ProviderOpenAI, ProviderOllama)
	}
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	switch prefix {
	case ProviderAnthropic, ProviderOpenAI, ProviderOllama:
		return prefix, strings.TrimSpace(rest), nil
	}
	return "", "", fmt.Errorf("unknown model provider %q in %q", prefix, trimmed)
}

// ResolveLaunch resolves options into a complete invocation without running it.
// Launch is this plus execution, so a dry run and a real run cannot drift.
func (t *Tap) ResolveLaunch(opts LaunchOptions) (*LaunchResult, error) {
	harness := strings.TrimSpace(opts.Harness)
	adapter, ok := harnessAdapters()[harness]
	if !ok {
		return nil, fmt.Errorf("unknown harness %q (available: %s)",
			opts.Harness, strings.Join(LaunchHarnesses(), ", "))
	}

	agentName := strings.TrimSpace(opts.Agent)
	if agentName == "" {
		return nil, fmt.Errorf("an agent is required: pass --agent")
	}
	cfg, err := t.ConfigService.Config()
	if err != nil {
		return nil, err
	}
	agent, ok := cfg.Agent(agentName)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q (configured: %s)",
			agentName, strings.Join(configuredAgentNames(cfg), ", "))
	}

	provider, model, err := ParseAgentModel(agent.Model)
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", agentName, err)
	}
	build, ok := adapter.providers[provider]
	if !ok {
		return nil, fmt.Errorf(
			"harness %q cannot use a %s model: it speaks a different protocol (supported here: %s)",
			harness, provider, strings.Join(adapterProviders(adapter), ", "))
	}

	auth, err := resolveAuthMode(agent, provider)
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", agentName, err)
	}

	// An explicit baseUrl always wins; Ollama otherwise defaults to the local
	// server. Hosted providers stay empty so the harness keeps its own endpoint.
	baseURL := strings.TrimSpace(agent.BaseURL)
	if baseURL == "" && provider == ProviderOllama {
		baseURL = defaultOllamaBaseURL
	}

	apiKey, keySource, err := t.resolveAPIKey(agent, auth)
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", agentName, err)
	}

	argv, env := build(launchSpec{
		provider: provider,
		model:    model,
		baseURL:  baseURL,
		apiKey:   apiKey,
		auth:     auth,
	})
	if agent.ContextWindow > 0 {
		if adapter.contextWindowArgs == nil {
			return nil, fmt.Errorf(
				"agent %q sets contextWindow but harness %q has no way to apply it",
				agentName, harness)
		}
		argv = append(argv, adapter.contextWindowArgs(agent.ContextWindow)...)
	}
	// Agent args first, then the invocation's own, so a one-off can override.
	argv = append(argv, agent.Args...)
	argv = append(argv, opts.Args...)
	if env == nil {
		env = map[string]string{}
	}
	// Export the agent, not the flight it currently resolves to. The launched
	// process looks up agents[TAP_AGENT].flight on every config load, so editing
	// the agent's flight and re-orienting moves a running session. Exporting
	// TAP_FLIGHT here instead would pin the value into an environment that
	// cannot be changed after exec, leaving the session stuck on whatever the
	// flight was at launch no matter what the config later said.
	env["TAP_AGENT"] = agentName

	// Subscription mode has to remove inherited credentials, which an overlay
	// cannot express: appending can override a variable but never unset one.
	var strip []string
	if auth == AuthSubscription || auth == AuthNone {
		for _, name := range providerKeyEnv[provider] {
			if _, set := env[name]; !set {
				strip = append(strip, name)
			}
		}
		sort.Strings(strip)
	}

	return &LaunchResult{
		Harness:   harness,
		Agent:     agentName,
		Provider:  provider,
		Model:     model,
		BaseURL:   baseURL,
		Flight:    strings.TrimSpace(agent.Flight),
		Auth:      auth,
		Argv:      argv,
		Env:       env,
		StripEnv:  strip,
		KeySource: keySource,
	}, nil
}

// resolveAuthMode validates an agent's auth field and supplies the default.
//
// A local provider defaults to none rather than inherit: cloud credentials have
// no business reaching a model running on your own hardware, and inheriting
// them is how an exported OPENAI_API_KEY ends up confusing a harness that is
// not talking to OpenAI at all.
func resolveAuthMode(agent AgentEntry, provider string) (string, error) {
	mode := strings.TrimSpace(agent.Auth)
	if mode == "" {
		switch {
		case strings.TrimSpace(agent.APIKeyEnv) != "":
			return AuthAPIKey, nil
		case provider == ProviderOllama:
			return AuthNone, nil
		default:
			return AuthInherit, nil
		}
	}
	switch mode {
	case AuthSubscription:
		if provider == ProviderOllama {
			// There is no subscription behind a local model, and honouring the
			// request would mean withholding the placeholder key that stops the
			// harness reaching for a real stored login.
			return "", fmt.Errorf(
				"auth %q is meaningless for a local %s model; use %q (the default) instead",
				AuthSubscription, ProviderOllama, AuthNone)
		}
		return mode, nil
	case AuthInherit, AuthAPIKey, AuthNone:
		return mode, nil
	default:
		return "", fmt.Errorf("unknown auth mode %q (want %s, %s, %s, or %s)",
			mode, AuthInherit, AuthSubscription, AuthAPIKey, AuthNone)
	}
}

// resolveAPIKey reads the variable named by apiKeyEnv. The name is configured,
// never the secret, so nothing sensitive lands in a config file. The returned
// source is the variable name, safe to print.
func (t *Tap) resolveAPIKey(agent AgentEntry, auth string) (key, source string, err error) {
	name := strings.TrimSpace(agent.APIKeyEnv)
	if name == "" {
		if auth == AuthAPIKey {
			return "", "", fmt.Errorf("auth %q requires apiKeyEnv naming the variable holding the key", AuthAPIKey)
		}
		return "", "", nil
	}
	if auth == AuthSubscription || auth == AuthNone {
		return "", "", fmt.Errorf("auth %q cannot be combined with apiKeyEnv", auth)
	}
	value := strings.TrimSpace(t.Runtime.Env().Get(name))
	if value == "" {
		return "", "", fmt.Errorf("apiKeyEnv names %s but that variable is empty or unset", name)
	}
	return value, name, nil
}

// Launch resolves the agent and starts the harness, wiring it to the runtime's
// streams so it runs interactively. With DryRun set it resolves and returns
// without executing.
func (t *Tap) Launch(ctx context.Context, opts LaunchOptions) (*LaunchResult, error) {
	resolved, err := t.ResolveLaunch(opts)
	if err != nil {
		return nil, err
	}
	if opts.DryRun {
		return resolved, nil
	}

	if _, err := exec.LookPath(resolved.Argv[0]); err != nil {
		return nil, fmt.Errorf("harness %q is not installed or not on PATH: %w", resolved.Argv[0], err)
	}

	cmd := exec.CommandContext(ctx, resolved.Argv[0], resolved.Argv[1:]...)
	stream := t.Runtime.Stream()
	cmd.Stdin = stream.In
	cmd.Stdout = stream.Out
	cmd.Stderr = stream.Err
	cmd.Env = append(stripEnv(t.Runtime.Environ(), resolved.StripEnv), envPairs(resolved.Env)...)
	if err := cmd.Run(); err != nil {
		return resolved, fmt.Errorf("%s exited: %w", resolved.Harness, err)
	}
	return resolved, nil
}

// stripEnv removes the named variables from a KEY=VALUE environment. Unsetting
// is why subscription mode cannot be expressed as an overlay: appending can
// override a variable's value but never make it absent.
func stripEnv(environ []string, names []string) []string {
	if len(names) == 0 {
		return environ
	}
	drop := make(map[string]struct{}, len(names))
	for _, n := range names {
		drop[n] = struct{}{}
	}
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		if _, skip := drop[key]; skip {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// envPairs renders an overlay as sorted KEY=VALUE pairs. Sorting keeps the
// child environment reproducible, which matters for the dry-run output.
func envPairs(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}

func configuredAgentNames(cfg *Config) []string {
	agents := cfg.Agents()
	if len(agents) == 0 {
		return []string{"none"}
	}
	out := make([]string, 0, len(agents))
	for name := range agents {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func adapterProviders(a harnessAdapter) []string {
	out := make([]string, 0, len(a.providers))
	for p := range a.providers {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
