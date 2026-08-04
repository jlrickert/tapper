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
)

// providerKeyEnv lists the credential variables each provider's clients read.
// AuthSubscription removes these from the child environment.
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
		case spec.provider == ProviderOllama && spec.auth != AuthSubscription:
			// Ollama ignores the value, but without one the client would try
			// the stored subscription login against the wrong host.
			env["ANTHROPIC_API_KEY"] = "ollama"
		}
		return []string{command}, env
	}
}

// openAIProtocol builds the invocation for harnesses speaking the OpenAI API.
func openAIProtocol(command string) func(launchSpec) ([]string, map[string]string) {
	return func(spec launchSpec) ([]string, map[string]string) {
		env := map[string]string{}
		if base := openAIBaseURL(spec.baseURL); base != "" {
			env["OPENAI_BASE_URL"] = base
		}
		switch {
		case spec.apiKey != "":
			env["OPENAI_API_KEY"] = spec.apiKey
		case spec.provider == ProviderOllama && spec.auth != AuthSubscription:
			env["OPENAI_API_KEY"] = "ollama"
		}
		return []string{command, "--model", spec.model}, env
	}
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
		},
		"codex": {
			command: "codex",
			providers: map[string]func(launchSpec) ([]string, map[string]string){
				ProviderOpenAI: openAIProtocol("codex"),
				ProviderOllama: openAIProtocol("codex"),
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

	auth, err := resolveAuthMode(agent)
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
	argv = append(argv, opts.Args...)
	if env == nil {
		env = map[string]string{}
	}
	// The launched process resolves its own flight through the normal chain,
	// where TAP_FLIGHT outranks project and user config. No new plumbing.
	if flight := strings.TrimSpace(agent.Flight); flight != "" {
		env["TAP_FLIGHT"] = flight
	}

	// Subscription mode has to remove inherited credentials, which an overlay
	// cannot express: appending can override a variable but never unset one.
	var strip []string
	if auth == AuthSubscription {
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

// resolveAuthMode validates an agent's auth field, defaulting to inherit.
func resolveAuthMode(agent AgentEntry) (string, error) {
	switch mode := strings.TrimSpace(agent.Auth); mode {
	case "":
		if strings.TrimSpace(agent.APIKeyEnv) != "" {
			return AuthAPIKey, nil
		}
		return AuthInherit, nil
	case AuthInherit, AuthSubscription, AuthAPIKey:
		return mode, nil
	default:
		return "", fmt.Errorf("unknown auth mode %q (want %s, %s, or %s)",
			mode, AuthInherit, AuthSubscription, AuthAPIKey)
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
	if auth == AuthSubscription {
		return "", "", fmt.Errorf("auth %q cannot be combined with apiKeyEnv", AuthSubscription)
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
