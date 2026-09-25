package tapper

// EXPERIMENTAL — `tap launch` is a scaffold for exercising Tapper against a
// chosen model and flight without editing config between runs. It is
// deliberately undocumented: agents are expected to move to Tapper Hub, at
// which point this file and its config shape are torn out and redesigned.
// Nothing else in the package should grow a dependency on it.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
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

// noLaunchFlightWarning is emitted when a launch resolves no root. The session
// is not unauthorized — it inherits exactly the identity's own access — but that
// is broader than a flight, so say which authority is in play and how to narrow
// it rather than letting the reader infer either.
const noLaunchFlightWarning = "no flight configured; this agent runs with " +
	"identity-authorized full access to every KEG you can reach. Create a flight " +
	"and set `flight:` in Tapper configuration to restrict it."

// LaunchOptions configures behavior for Tap.Launch.
type LaunchOptions struct {
	// Harness names the agent CLI to start: claude, codex, or pi.
	Harness string
	// Agent names an entry in the config's agents map. Empty falls back to the
	// config's agent key (which TAP_AGENT also feeds).
	Agent string
	// Model is a Hub catalog id. Setting it selects hub mode: the harness is
	// wired to Hub's inference endpoints rather than a configured agent's
	// provider. Hub mode is also what an invocation with neither Model nor
	// any agent (explicit or configured) falls back to.
	Model string
	// Flight is the explicit launch root. Empty falls back through TAP_FLIGHT,
	// project configuration, and user configuration.
	Flight string
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
// Flight is the canonical connection-pinned Hub-backed root exported to the
// child as TAP_FLIGHT. It is empty when no flight is configured; the harness
// then runs under no-flight identity authority and Warnings says so.
//
// Warnings are returned rather than printed so a dry run and a real run report
// the same thing — see ResolveLaunch.
type LaunchResult struct {
	// Source is LaunchSourceAgent for a configured agent or LaunchSourceHub
	// for a Hub catalog model.
	Source string
	// Hub names the hub serving a hub-mode launch.
	Hub       string
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
	Warnings  []string

	// hub finishes a hub-mode invocation once the forwarder is listening; Argv
	// and Env above carry placeholders for its address and key until then.
	hub *hubLaunchPlan
}

// Launch sources, reported in LaunchResult.Source.
const (
	LaunchSourceAgent = "agent"
	LaunchSourceHub   = "hub"
)

// Placeholders a hub-mode dry run shows where the live forwarder's address and
// per-launch key go. Neither exists until Launch starts the forwarder.
const (
	launchForwarderPlaceholder = "http://127.0.0.1:<port>/v1"
	launchKeyPlaceholder       = "<launch key>"
)

// opencodeHubProvider is the provider id hub-mode opencode sessions select
// models under, as in `--model foldwise/<catalog id>`.
const opencodeHubProvider = "foldwise"

// HubModel is one entry of the caller's Hub inference catalog.
type HubModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
	// ContextWindow is the model's token limit, or 0 when its relay did not
	// advertise one.
	ContextWindow int `json:"context_window,omitempty"`
}

// hubLaunchSpec is one resolved hub-mode launch, handed to a harness's hub
// builder.
type hubLaunchSpec struct {
	hubName string
	baseURL string
	apiKey  string
	model   string
	catalog []HubModel
}

// hubLaunchPlan is what Launch needs to render the real invocation once the
// forwarder is up. tailArgs and tailEnv are the parts that do not depend on
// the forwarder: pass-through arguments and the TAP_* variables.
type hubLaunchPlan struct {
	hubURL   string
	token    func() string
	build    func(hubLaunchSpec) ([]string, map[string]string)
	spec     hubLaunchSpec
	tailArgs []string
	tailEnv  map[string]string
}

// render builds the invocation against a forwarder at baseURL taking apiKey.
func (p *hubLaunchPlan) render(baseURL, apiKey string) ([]string, map[string]string) {
	spec := p.spec
	spec.baseURL, spec.apiKey = baseURL, apiKey
	argv, env := p.build(spec)
	argv = append(argv, p.tailArgs...)
	if env == nil {
		env = map[string]string{}
	}
	for k, v := range p.tailEnv {
		env[k] = v
	}
	return argv, env
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
	// hub builds a hub-mode invocation. Nil means the harness cannot use Hub
	// catalog models yet.
	hub func(spec hubLaunchSpec) ([]string, map[string]string)
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

// opencodeProtocol builds the invocation for opencode.
//
// opencode selects its model with `--model provider/model`, using the same
// provider names tapper already parses, so the agent's configured model string
// passes through unchanged.
//
// It does NOT read ANTHROPIC_BASE_URL or OPENAI_BASE_URL: an endpoint is a
// provider option in its config. Rather than require the user to edit that
// config before a launch can work, the base URL is injected through
// OPENCODE_CONFIG_CONTENT, an inline config opencode merges after both the
// global and project files — so it wins without replacing either. The API keys
// are ordinary environment variables it does read, so those stay as env.
func opencodeProtocol(spec launchSpec) ([]string, map[string]string) {
	env := map[string]string{}
	if config := opencodeProviderConfig(spec); config != "" {
		env["OPENCODE_CONFIG_CONTENT"] = config
	}
	switch {
	case spec.apiKey != "":
		env[opencodeKeyEnv(spec.provider)] = spec.apiKey
	case spec.provider == ProviderOllama:
		// Unconditional, for the same reason as the Anthropic and OpenAI
		// builders: the placeholder is what stops the client reaching for a
		// stored login and sending it to a host that is not the provider.
		env["OPENAI_API_KEY"] = "ollama"
	}
	return []string{"opencode", "--model", spec.provider + "/" + spec.model}, env
}

// opencodeKeyEnv names the variable opencode reads a provider's key from.
func opencodeKeyEnv(provider string) string {
	if provider == ProviderAnthropic {
		return "ANTHROPIC_API_KEY"
	}
	return "OPENAI_API_KEY"
}

// opencodeProviderConfig renders the inline provider override, or "" when the
// harness needs none.
//
// Ollama always needs one: opencode ships no ollama provider, so the whole
// definition — the OpenAI-compatible npm driver, the endpoint, and the model
// entry — has to be declared. A hosted provider needs one only when the agent
// overrides baseUrl, and then only the endpoint changes.
func opencodeProviderConfig(spec launchSpec) string {
	base := openAIBaseURL(spec.baseURL)
	if base == "" {
		return ""
	}
	type limits struct {
		BaseURL string `json:"baseURL"`
		APIKey  string `json:"apiKey,omitempty"`
	}
	type model struct {
		Name string `json:"name,omitempty"`
	}
	provider := struct {
		NPM     string           `json:"npm,omitempty"`
		Name    string           `json:"name,omitempty"`
		Options limits           `json:"options"`
		Models  map[string]model `json:"models,omitempty"`
	}{Options: limits{BaseURL: base}}
	if spec.provider == ProviderOllama {
		provider.NPM = "@ai-sdk/openai-compatible"
		provider.Name = "Ollama (local)"
		// The placeholder key travels in the config rather than the
		// environment because a custom provider reads its own options first.
		provider.Options.APIKey = "ollama"
		provider.Models = map[string]model{spec.model: {Name: spec.model}}
	}
	body, err := json.Marshal(map[string]any{
		"provider": map[string]any{spec.provider: provider},
	})
	if err != nil {
		// Every field is a plain string or map of strings, so this cannot fail.
		// Returning "" rather than panicking keeps a launch working with the
		// harness's own endpoint if it somehow does.
		return ""
	}
	return string(body)
}

// opencodeHub wires opencode to Hub through one inline provider pointed at
// the launch forwarder. Every catalog model is declared, not just the selected
// one, so opencode's model picker can switch between them mid-session.
func opencodeHub(spec hubLaunchSpec) ([]string, map[string]string) {
	type limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	}
	type model struct {
		Name  string `json:"name"`
		Limit *limit `json:"limit,omitempty"`
	}
	models := make(map[string]model, len(spec.catalog))
	for _, m := range spec.catalog {
		entry := model{Name: m.ID}
		if m.ContextWindow > 0 {
			// opencode wants both halves of the limit and a relay advertises
			// only the context. A quarter of it, capped, leaves room for the
			// conversation while allowing a long reply.
			entry.Limit = &limit{Context: m.ContextWindow, Output: min(m.ContextWindow/4, 32000)}
		}
		models[m.ID] = entry
	}
	name := "Foldwise"
	if spec.hubName != "" {
		name += " (" + spec.hubName + ")"
	}
	// No HTML escaping: the dry run prints this, and "<launch key>" should
	// read as itself rather than as \u003claunch key\u003e.
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(map[string]any{
		"provider": map[string]any{
			opencodeHubProvider: map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": name,
				// The key travels in the config rather than the environment
				// because a custom provider reads its own options first.
				"options": map[string]string{"baseURL": spec.baseURL, "apiKey": spec.apiKey},
				"models":  models,
			},
		},
	})
	env := map[string]string{}
	if err == nil {
		env["OPENCODE_CONFIG_CONTENT"] = strings.TrimSpace(buf.String())
	}
	return []string{"opencode", "--model", opencodeHubProvider + "/" + spec.model}, env
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
		"opencode": {
			command: "opencode",
			providers: map[string]func(launchSpec) ([]string, map[string]string){
				ProviderAnthropic: opencodeProtocol,
				ProviderOpenAI:    opencodeProtocol,
				ProviderOllama:    opencodeProtocol,
			},
			// contextWindowArgs stays nil on purpose. opencode has no flag for
			// it: a context cap is provider.<p>.models.<m>.limit.context in its
			// config. Leaving this nil makes a configured contextWindow an
			// explicit error rather than a setting that silently never applied.
			hub: opencodeHub,
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
	return t.ResolveLaunchContext(context.Background(), opts)
}

// ResolveLaunchContext is ResolveLaunch with a context for the one network
// call it can make: a hub-mode launch lists the Hub catalog.
func (t *Tap) ResolveLaunchContext(ctx context.Context, opts LaunchOptions) (*LaunchResult, error) {
	harness := strings.TrimSpace(opts.Harness)
	adapter, ok := harnessAdapters()[harness]
	if !ok {
		return nil, fmt.Errorf("unknown harness %q (available: %s)",
			opts.Harness, strings.Join(LaunchHarnesses(), ", "))
	}

	cfg, err := t.ConfigService.Config()
	if err != nil {
		return nil, err
	}
	// --agent wins; otherwise fall back to the config's agent key, which
	// TAP_AGENT also feeds. This mirrors ActiveFlightName's explicit-then-config
	// resolution used for the launch root just below. Reading the fallback off
	// the same cfg snapshot as the lookup keeps the two from drifting.
	agentName := strings.TrimSpace(opts.Agent)
	hubModel := strings.TrimSpace(opts.Model)
	if agentName != "" && hubModel != "" {
		return nil, fmt.Errorf("--model and --agent are mutually exclusive: --model picks a Hub catalog model, --agent a configured one")
	}
	if agentName == "" && hubModel == "" {
		agentName = cfg.AgentName()
	}

	root, hasRoot, warnings, err := t.resolveLaunchRoot(cfg, opts.Flight)
	if err != nil {
		return nil, err
	}
	// No agent anywhere means the Hub catalog, which is where models come
	// from once the agents map is retired.
	if agentName == "" {
		return t.resolveHubLaunch(ctx, harness, adapter, hubModel, opts.Args, cfg, root, hasRoot, warnings)
	}

	agent, ok := cfg.Agent(agentName)
	if !ok {
		return nil, fmt.Errorf("unknown agent %q (configured: %s)",
			agentName, strings.Join(configuredAgentNames(cfg), ", "))
	}
	if agent.invalid != nil {
		return nil, fmt.Errorf("agent %q: %w", agentName, agent.invalid)
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
	// TAP_AGENT is model selection and telemetry only; launchTapEnv supplies
	// the hub, keg, and flight every launch shares.
	env["TAP_AGENT"] = agentName
	tapEnv, err := t.launchTapEnv(cfg, root, hasRoot)
	if err != nil {
		return nil, err
	}
	for k, v := range tapEnv {
		env[k] = v
	}

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

	flight := ""
	if hasRoot {
		flight = root.Canonical()
	}
	return &LaunchResult{
		Source:    LaunchSourceAgent,
		Harness:   harness,
		Agent:     agentName,
		Provider:  provider,
		Model:     model,
		BaseURL:   baseURL,
		Flight:    flight,
		Auth:      auth,
		Argv:      argv,
		Env:       env,
		StripEnv:  strip,
		KeySource: keySource,
		Warnings:  warnings,
	}, nil
}

// resolveLaunchRoot resolves the optional launch root flight.
func (t *Tap) resolveLaunchRoot(cfg *Config, explicit string) (root FlightRef, hasRoot bool, warnings []string, err error) {
	// A launch root is optional. Without one the child runs under no-flight
	// identity authority — the same state bare `tap mcp` and hosted /mcp reach —
	// which is what makes bootstrapping possible: you cannot be required to
	// select a flight in order to launch the session that creates your first one.
	// There is nothing to validate in that case, and no namespace to resolve a
	// hub from; the child uses the selected Hub.
	if rootRef := strings.TrimSpace(t.ActiveFlightName(explicit)); rootRef != "" {
		parsed, err := ParseFlightRef(rootRef, t.defaultFlightNamespace(cfg))
		if err != nil {
			return root, false, nil, fmt.Errorf("resolve launch flight %q: %w", rootRef, err)
		}
		if parsed.Namespace == "" {
			return root, false, nil, fmt.Errorf("tap launch requires a canonical Hub-backed root flight; %q has no namespace", rootRef)
		}
		if _, _, err := t.ConfigService.SelectedHub(""); err != nil {
			return root, false, nil, fmt.Errorf("resolve launch flight %q: %w", rootRef, err)
		}
		root, hasRoot = parsed, true
	} else {
		warnings = append(warnings, noLaunchFlightWarning)
	}
	return root, hasRoot, warnings, nil
}

// launchTapEnv is the TAP_* overlay every launch exports, whichever way its
// model was chosen. TAP_FLIGHT pins the canonical launch root for the child
// process lifetime; governed calls may select a live accessible descendant but
// never replace that root. It is left unset when no flight is configured,
// which is exactly how the child's `tap mcp` decides it is not launcher-bound
// and resolves identity authority instead (see cmd_mcp.go).
func (t *Tap) launchTapEnv(cfg *Config, root FlightRef, hasRoot bool) (map[string]string, error) {
	hubName, _, err := t.ConfigService.SelectedHub("")
	if err != nil {
		return nil, err
	}
	env := map[string]string{"TAP_HUB": hubName}
	if cfg.Keg() != "" {
		env["TAP_KEG"] = cfg.Keg()
	}
	if hasRoot {
		env["TAP_FLIGHT"] = root.Canonical()
	}
	return env, nil
}

// resolveHubLaunch resolves a hub-mode launch: the model is a Hub catalog id,
// and the harness reaches it through a loopback forwarder Launch starts, so
// no Hub credential is handed to the harness.
func (t *Tap) resolveHubLaunch(ctx context.Context, harness string, adapter harnessAdapter, model string, args []string, cfg *Config, root FlightRef, hasRoot bool, warnings []string) (*LaunchResult, error) {
	if adapter.hub == nil {
		return nil, fmt.Errorf(
			"harness %q cannot use Hub models yet (hub mode supports: %s); pass --agent to use a configured agent",
			harness, strings.Join(hubHarnesses(), ", "))
	}
	hubName, entry, err := t.ConfigService.SelectedHub("")
	if err != nil {
		return nil, err
	}
	hubURL := strings.TrimRight(hubURLWithScheme(entry.URL), "/")
	if hubURL == "" {
		return nil, fmt.Errorf("hub %q has no url", hubName)
	}
	token := func() string { return t.hubToken(entry) }
	catalog, err := fetchHubCatalog(ctx, hubURL, token())
	if err != nil {
		return nil, fmt.Errorf("hub %q: %w", hubName, err)
	}
	if len(catalog) == 0 {
		return nil, fmt.Errorf(
			"hub %q has no models for you yet; run `tap relay` to contribute your own (or pass --agent to use a configured agent)",
			hubName)
	}
	if model == "" {
		model = catalog[0].ID
	} else if !hubCatalogHas(catalog, model) {
		return nil, fmt.Errorf("model %q is not in your catalog on hub %q (is its relay connected?); available: %s",
			model, hubName, strings.Join(hubCatalogIDs(catalog), ", "))
	}
	tailEnv, err := t.launchTapEnv(cfg, root, hasRoot)
	if err != nil {
		return nil, err
	}
	plan := &hubLaunchPlan{
		hubURL:   hubURL,
		token:    token,
		build:    adapter.hub,
		spec:     hubLaunchSpec{hubName: hubName, model: model, catalog: catalog},
		tailArgs: append([]string(nil), args...),
		tailEnv:  tailEnv,
	}
	argv, env := plan.render(launchForwarderPlaceholder, launchKeyPlaceholder)
	flight := ""
	if hasRoot {
		flight = root.Canonical()
	}
	return &LaunchResult{
		Source:   LaunchSourceHub,
		Hub:      hubName,
		Harness:  harness,
		Provider: LaunchSourceHub,
		Model:    model,
		BaseURL:  launchForwarderPlaceholder,
		Flight:   flight,
		Auth:     LaunchSourceHub,
		Argv:     argv,
		Env:      env,
		Warnings: warnings,
		hub:      plan,
	}, nil
}

// fetchHubCatalog lists the caller's models from Hub's inference endpoint.
func fetchHubCatalog(ctx context.Context, hubURL, token string) ([]HubModel, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("not signed in; run `tap auth login`")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hubURL+hubInferencePath+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := hubHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("the hub rejected your credential; run `tap auth login`")
	case http.StatusNotFound:
		return nil, fmt.Errorf("the hub does not serve relay inference (relay disabled, or an older hub)")
	default:
		return nil, fmt.Errorf("list models: hub returned %s", resp.Status)
	}
	var list struct {
		Data []HubModel `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&list); err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	return list.Data, nil
}

func hubCatalogHas(catalog []HubModel, id string) bool {
	for _, m := range catalog {
		if m.ID == id {
			return true
		}
	}
	return false
}

func hubCatalogIDs(catalog []HubModel) []string {
	out := make([]string, 0, len(catalog))
	for _, m := range catalog {
		out = append(out, m.ID)
	}
	return out
}

// hubHarnesses lists the harnesses with a hub mode, sorted.
func hubHarnesses() []string {
	var out []string
	for name, a := range harnessAdapters() {
		if a.hub != nil {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
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
//
// Warnings are written to stderr here, before the harness takes the terminal.
// Reporting them from the returned result would be too late: Launch does not
// return until the agent session has ended, so the reader would learn what
// authority the session had only after it was over. Stderr also keeps them clear
// of a piped dry-run report.
func (t *Tap) Launch(ctx context.Context, opts LaunchOptions) (*LaunchResult, error) {
	resolved, err := t.ResolveLaunchContext(ctx, opts)
	if err != nil {
		return nil, err
	}
	if stream := t.Runtime.Stream(); stream != nil && stream.Err != nil {
		for _, warning := range resolved.Warnings {
			fmt.Fprintln(stream.Err, "warning: "+warning)
		}
	}
	if opts.DryRun {
		return resolved, nil
	}

	if _, err := exec.LookPath(resolved.Argv[0]); err != nil {
		return nil, fmt.Errorf("harness %q is not installed or not on PATH: %w", resolved.Argv[0], err)
	}

	argv, env := resolved.Argv, resolved.Env
	if resolved.hub != nil {
		// Up before the harness and down after it, so every request the
		// harness makes has somewhere to go.
		fw, err := startLaunchForwarder(resolved.hub.hubURL, resolved.hub.token)
		if err != nil {
			return nil, err
		}
		defer func() { _ = fw.Close() }()
		argv, env = resolved.hub.render(fw.BaseURL(), fw.Secret())
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	stream := t.Runtime.Stream()
	cmd.Stdin = stream.In
	cmd.Stdout = stream.Out
	cmd.Stderr = stream.Err
	cmd.Env = append(stripEnv(t.Runtime.Environ(), resolved.StripEnv), envPairs(env)...)
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
