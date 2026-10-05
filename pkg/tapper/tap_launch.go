package tapper

// EXPERIMENTAL — `tap launch` starts an agent harness on a model from the
// caller's Tapper Hub catalog, under the configured flight. Hub is the only
// inference plane: the launcher knows harnesses, not providers, and a model is
// always a catalog id. Nothing else in the package should grow a dependency
// on it.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// noLaunchFlightWarning is emitted when a launch resolves no root. The session
// is not unauthorized — it inherits exactly the identity's own access — but that
// is broader than a flight, so say which authority is in play and how to narrow
// it rather than letting the reader infer either.
const noLaunchFlightWarning = "no flight configured; this agent runs with " +
	"identity-authorized full access to every KEG you can reach. Create a flight " +
	"and set `flight:` in Tapper configuration to restrict it."

// LaunchOptions configures behavior for Tap.Launch.
type LaunchOptions struct {
	// Harness names the agent CLI to start: claude, codex, opencode, or pi.
	Harness string
	// Agent is an explicit Hub agent reference (@namespace/name).
	Agent string
	// Model is a Hub catalog id. Empty starts on the first model in the
	// catalog, which Hub orders by the best priority any contributing relay
	// owner gave each pooled model.
	Model string
	// Flight is the explicit launch root. Empty falls back through TAP_FLIGHT,
	// project configuration, and user configuration.
	Flight string
	// Inference is where a Claude Code launch sends model requests:
	// LaunchInferenceSplit (the default for claude), LaunchInferenceHub, or
	// LaunchInferenceSubscription. Other harnesses use Hub only.
	Inference string
	// Subagents picks the Hub agents a Claude Code launch offers as
	// subagents: "" the agent's own subagents, "all" every agent the caller
	// can see, "none" none.
	Subagents string
	// SubagentBuiltins are the Claude Code built-in tools each subagent gets
	// beside its Hub tools. Nil means DefaultSubagentBuiltins.
	SubagentBuiltins []string
	// DryRun resolves and reports the invocation without executing it.
	DryRun bool
	// Args are extra arguments appended to the harness invocation.
	Args []string
}

// LaunchResult reports the resolved invocation. Env holds only the overlay
// applied on top of the inherited environment; StripEnv names variables removed
// from it. Files are written to a per-launch directory for the harness to read.
// None of them carries a secret: the forwarder's address, its per-launch key,
// and that directory show as placeholders until Launch fills them in.
//
// Flight is the canonical connection-pinned Hub-backed root exported to the
// child as TAP_FLIGHT. It is empty when no flight is configured; the harness
// then runs under no-flight identity authority and Warnings says so.
//
// Warnings are returned rather than printed so a dry run and a real run report
// the same thing — see ResolveLaunch.
type LaunchResult struct {
	// Inference is a Claude Code launch's mode; empty for other harnesses.
	Inference string
	// Hub names the hub serving the models.
	HubAgent string
	Hub      string
	Harness  string
	Model    string
	Flight   string
	Argv     []string
	Env      map[string]string
	StripEnv []string
	Files    map[string]string
	Warnings []string

	plan *launchPlan
}

// Placeholders a dry run shows where the live forwarder's origin, its
// per-launch key, and the per-launch file directory go. None exists until
// Launch starts the forwarder.
const (
	launchForwarderPlaceholder = "http://127.0.0.1:<port>"
	launchKeyPlaceholder       = "<launch key>"
	launchDirPlaceholder       = "<launch dir>"
)

// Inference modes for a Claude Code launch.
const (
	// LaunchInferenceSplit keeps Claude Code on the user's own Claude login
	// for Claude models and adds the Hub catalog beside them: the forwarder
	// routes each request by the model it names.
	LaunchInferenceSplit = "split"
	// LaunchInferenceHub sends every model request to Hub.
	LaunchInferenceHub = "hub"
	// LaunchInferenceSubscription leaves model requests alone entirely;
	// Hub supplies only tools and agents.
	LaunchInferenceSubscription = "subscription"
)

// DefaultSubagentBuiltins are the Claude Code built-in tools a Hub subagent
// gets: read-only, so a Hub agent's authority stays what its tools say.
var DefaultSubagentBuiltins = []string{"Read", "Grep", "Glob"}

// claudeTapperToolPrefix is Claude Code's name prefix for the tapper plugin's
// MCP tools, mcp__<server>__<tool>. `tap mcp` also serves the caller's relayed
// tools, so they carry the same prefix.
const claudeTapperToolPrefix = "mcp__plugin_tapper_tapper__"

// launchKeyEnv carries the forwarder's per-launch key to harnesses that read
// their API key from a named variable.
const launchKeyEnv = "TAP_LAUNCH_KEY"

// hubProviderID is the provider id harnesses list Hub's models under, as in
// opencode's `foldwise/<catalog id>`.
const hubProviderID = "foldwise"

// HubModel is one entry of the caller's Hub inference catalog.
type HubModel struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by"`
	// ContextWindow is the model's token limit, or 0 when its relay did not
	// advertise one.
	ContextWindow int `json:"context_window,omitempty"`
	// Capabilities is set only for models that are not for chat, such as
	// speech to text.
	Capabilities []string `json:"capabilities,omitempty"`
}

// launchSpec is one resolved launch, handed to a harness builder.
type launchSpec struct {
	hubName string
	// origin is the forwarder's origin, without a path; each builder appends
	// the path of the protocol its harness speaks.
	origin string
	apiKey string
	// dir is where the harness's per-launch files are written.
	dir     string
	model   string
	catalog []HubModel
	agent   *HubAgent
	// inference is the Claude Code launch mode; empty for other harnesses.
	inference string
	// subagents are offered to Claude Code as subagents.
	subagents []HubAgent
	// builtins are the built-in tools each subagent gets.
	builtins []string
	// hostedTools is every hosted Hub tool, for a subagent allowed them all.
	hostedTools []string
	// relayAllowed reports that the launch agent allows relayed tools, which
	// the plugin's `tap mcp` serves; relayTools are their names.
	relayAllowed bool
	relayTools   []string
}

// pinned is the forwarder origin plus its key-in-path prefix.
func (s launchSpec) pinned() string { return s.origin + "/t/" + s.apiKey }

// hubModel reports whether id is in the Hub catalog.
func (s launchSpec) hubModel(id string) bool { return hubCatalogHas(s.catalog, id) }

// contextWindow is the selected model's advertised token limit, or 0.
func (s launchSpec) contextWindow() int {
	for _, m := range s.catalog {
		if m.ID == s.model {
			return m.ContextWindow
		}
	}
	return 0
}

// invocation is what a harness builder produces.
type invocation struct {
	argv []string
	env  map[string]string
	// strip names inherited variables that must not reach the harness.
	strip []string
	// files are written to the launch directory, keyed by file name.
	files map[string]string
}

// launchPlan is what Launch needs to render the real invocation once the
// forwarder is up. tailArgs and tailEnv are the parts that do not depend on
// the forwarder: pass-through arguments and the TAP_* variables.
type launchPlan struct {
	hubURL string
	token  func() string
	// anthropic is where a split launch sends Claude models; empty when
	// unused.
	anthropic string
	build     func(launchSpec) invocation
	spec      launchSpec
	tailArgs  []string
	tailEnv   map[string]string
}

// render builds the invocation against a forwarder at origin taking apiKey,
// with its files in dir.
func (p *launchPlan) render(origin, apiKey, dir string) invocation {
	spec := p.spec
	spec.origin, spec.apiKey, spec.dir = origin, apiKey, dir
	inv := p.build(spec)
	inv.strip = append(inv.strip, "TAP_AGENT")
	if spec.agent != nil && spec.agent.Instructions != "" {
		switch inv.argv[0] {
		case "claude", "pi":
			inv.argv = append(inv.argv, "--append-system-prompt", spec.agent.Instructions)
		case "codex":
			inv.argv = append(inv.argv, "-c", "developer_instructions="+strconv.Quote(spec.agent.Instructions))
		}
	}
	inv.argv = append(inv.argv, p.tailArgs...)
	if inv.env == nil {
		inv.env = map[string]string{}
	}
	for k, v := range p.tailEnv {
		inv.env[k] = v
	}
	return inv
}

// harnessBuilders maps each harness to how it is wired to Hub. The harnesses
// speak different protocols, and Hub serves each: Claude Code the Anthropic
// Messages API, Codex the OpenAI Responses API, and opencode and pi OpenAI
// chat completions.
func harnessBuilders() map[string]func(launchSpec) invocation {
	return map[string]func(launchSpec) invocation{
		"claude":   claudeLaunch,
		"codex":    codexLaunch,
		"opencode": opencodeLaunch,
		"pi":       piLaunch,
	}
}

// claudeLaunch points Claude Code at its models, then adds the launch's Hub
// subagents. Where the models come from depends on the
// mode; see claudeHubLaunch and claudeSplitLaunch.
func claudeLaunch(spec launchSpec) invocation {
	var inv invocation
	switch spec.inference {
	case LaunchInferenceSplit:
		inv = claudeSplitLaunch(spec)
	case LaunchInferenceSubscription:
		inv = invocation{argv: []string{"claude"}, env: map[string]string{}}
		if spec.model != "" {
			inv.argv = append(inv.argv, "--model", spec.model)
		}
	default:
		inv = claudeHubLaunch(spec)
	}
	if agents := claudeSubagents(spec); agents != nil {
		inv.argv = append(inv.argv, "--agents", encodeLaunchJSON(agents))
	}
	return inv
}

// claudeHubLaunch points Claude Code at Hub's Anthropic Messages endpoint.
// Every model slot it uses, including the small one for background work, is
// the selected model, so nothing leaves Hub. An inherited ANTHROPIC_API_KEY is
// removed: Claude Code would otherwise send it in preference, and warn about
// the conflict. Claude Code does not know catalog models, so their context
// window, when the relay advertised one, is passed as the limit it compacts
// against; otherwise it assumes 200k.
//
// Claude Code's /model picker lists only its built-in Claude lineup, which
// would all route to the one model above. A session-only modelPicker setting
// replaces that lineup with the catalog, so the picker switches between Hub
// models the way opencode's and pi's do. --settings layers over the user's
// own settings file rather than replacing it.
func claudeHubLaunch(spec launchSpec) invocation {
	env := map[string]string{
		"ANTHROPIC_BASE_URL":             spec.origin + "/anthropic",
		"ANTHROPIC_AUTH_TOKEN":           spec.apiKey,
		"ANTHROPIC_MODEL":                spec.model,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":   spec.model,
		"ANTHROPIC_DEFAULT_SONNET_MODEL": spec.model,
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":  spec.model,
		"ANTHROPIC_SMALL_FAST_MODEL":     spec.model,
	}
	if n := spec.contextWindow(); n > 0 {
		env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] = strconv.Itoa(n)
	}
	settings := encodeLaunchJSON(map[string]any{"modelPicker": map[string]any{"options": claudePickerRows(spec)}})
	return invocation{
		argv:  []string{"claude", "--model", spec.model, "--settings", settings},
		env:   env,
		strip: []string{"ANTHROPIC_API_KEY"},
	}
}

// claudeSplitLaunch keeps Claude Code on the user's own Claude credential and
// adds the Hub catalog beside its built-in models. Claude Code takes one base
// URL, so it is the forwarder's pinned prefix: the forwarder sends requests
// naming a Claude model on to Anthropic with the credential Claude Code sent,
// and the rest to Hub with the Hub credential. Claude Code's own model
// defaults stay, so background work runs on its small Claude model. Nothing
// inherited is stripped: an ANTHROPIC_API_KEY the user set is that user's
// Anthropic credential, and still only reaches Anthropic.
//
// The context limit is set only when the session starts on a Hub model; it
// applies to the whole session, so it would otherwise cap Claude models at a
// Hub model's window.
func claudeSplitLaunch(spec launchSpec) invocation {
	env := map[string]string{"ANTHROPIC_BASE_URL": spec.pinned() + "/anthropic"}
	argv := []string{"claude"}
	if spec.model != "" {
		argv = append(argv, "--model", spec.model)
		if n := spec.contextWindow(); n > 0 && spec.hubModel(spec.model) {
			env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] = strconv.Itoa(n)
		}
	}
	if rows := claudePickerRows(spec); len(rows) > 0 {
		argv = append(argv, "--settings", encodeLaunchJSON(map[string]any{
			"modelPicker": map[string]any{"options": rows, "replaceBuiltInOptions": false},
		}))
	}
	return invocation{argv: argv, env: env}
}

// claudePickerRow is one /model picker entry.
type claudePickerRow struct {
	Model       string `json:"model"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

func claudePickerRows(spec launchSpec) []claudePickerRow {
	var rows []claudePickerRow
	for _, m := range chatModels(spec.catalog) {
		rows = append(rows, claudePickerRow{Model: m.ID, Label: m.ID, Description: catalogDescription(spec, m)})
	}
	return rows
}

// claudeSubagent is one entry of Claude Code's --agents JSON.
type claudeSubagent struct {
	Description string   `json:"description"`
	Prompt      string   `json:"prompt"`
	Tools       []string `json:"tools"`
	Model       string   `json:"model,omitempty"`
}

// claudeSubagents renders the launch's Hub subagents for --agents, or nil for
// none. Each gets only its own Hub tools, as Claude Code names them, plus the
// launch's built-ins. Hub still gates every call to the main agent's tools:
// the MCP sessions run as the launch agent, so a subagent's list can narrow
// what it is offered but never widen it.
func claudeSubagents(spec launchSpec) map[string]claudeSubagent {
	if len(spec.subagents) == 0 {
		return nil
	}
	out := make(map[string]claudeSubagent, len(spec.subagents))
	for _, a := range spec.subagents {
		tools := slices.Clone(spec.builtins)
		names := a.ToolNames()
		if len(names) == 0 {
			names = spec.hostedTools
		}
		for _, name := range names {
			tools = append(tools, claudeTapperToolPrefix+name)
		}
		if a.RelayTools && spec.relayAllowed {
			for _, name := range spec.relayTools {
				tools = append(tools, claudeTapperToolPrefix+name)
			}
		}
		description := strings.TrimSpace(a.Description)
		if description == "" {
			description = strings.TrimSpace(a.Title)
		}
		if description == "" {
			description = "Hub agent " + a.Ref
		}
		entry := claudeSubagent{Description: description, Prompt: a.Instructions, Tools: tools}
		if a.Model != "" && claudeModelUsable(spec, a.Model) {
			entry.Model = a.Model
		}
		out[a.Namespace+"-"+a.Name] = entry
	}
	return out
}

// claudeModelUsable reports whether a Claude Code launch in spec's mode can
// reach model: Hub models in hub and split mode, Claude models in split and
// subscription mode.
func claudeModelUsable(spec launchSpec, model string) bool {
	switch spec.inference {
	case LaunchInferenceSplit:
		return spec.hubModel(model) || IsClaudeModel(model)
	case LaunchInferenceSubscription:
		return IsClaudeModel(model)
	default:
		return spec.hubModel(model)
	}
}

// catalogDescription says where a catalog model comes from and how much
// context it takes, for a harness's model picker: the Foldwise hub that
// provides it and the pool serving it.
func catalogDescription(spec launchSpec, m HubModel) string {
	parts := []string{providerName(spec)}
	owner := strings.TrimSpace(m.OwnedBy)
	if pool, ok := strings.CutPrefix(owner, "pool:"); ok {
		parts = append(parts, pool+" pool")
	} else if owner != "" {
		// An older hub names the relay itself.
		parts = append(parts, owner)
	}
	if m.ContextWindow > 0 {
		parts = append(parts, fmt.Sprintf("%dk context", m.ContextWindow/1024))
	}
	return strings.Join(parts, " · ")
}

// codexLaunch declares Hub as a Codex model provider on the command line.
// Codex only speaks the Responses API, so the provider's wire API is
// "responses", and it reads the key from the variable env_key names. No
// OPENAI_API_KEY is set: Codex treats one alongside a stored ChatGPT login as
// mixed auth, and this provider does not use it.
func codexLaunch(spec launchSpec) invocation {
	provider := fmt.Sprintf(`model_providers.%s={name=%s, base_url=%s, env_key=%s, wire_api="responses"}`,
		hubProviderID, strconv.Quote(providerName(spec)), strconv.Quote(spec.origin+"/v1"), strconv.Quote(launchKeyEnv))
	argv := []string{"codex", "-c", provider, "-c", `model_provider="` + hubProviderID + `"`}
	if n := spec.contextWindow(); n > 0 {
		// Codex keeps this as model metadata for a model it does not know,
		// which decides when it compacts.
		argv = append(argv, "-c", "model_context_window="+strconv.Itoa(n))
	}
	argv = append(argv, "--model", spec.model)
	return invocation{
		argv: argv,
		env:  map[string]string{launchKeyEnv: spec.apiKey},
	}
}

// opencodeLaunch wires opencode to Hub through one inline provider. Every
// catalog model is declared, not just the selected one, so opencode's model
// picker can switch between them mid-session.
func opencodeLaunch(spec launchSpec) invocation {
	type limit struct {
		Context int `json:"context"`
		Output  int `json:"output"`
	}
	type model struct {
		Name  string `json:"name"`
		Limit *limit `json:"limit,omitempty"`
	}
	models := make(map[string]model, len(spec.catalog))
	for _, m := range chatModels(spec.catalog) {
		entry := model{Name: m.ID}
		if m.ContextWindow > 0 {
			entry.Limit = &limit{Context: m.ContextWindow, Output: outputLimit(m.ContextWindow)}
		}
		models[m.ID] = entry
	}
	config := map[string]any{
		"provider": map[string]any{
			hubProviderID: map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": providerName(spec),
				// The key travels in the config rather than the environment
				// because a custom provider reads its own options first.
				"options": map[string]string{"baseURL": spec.origin + "/v1", "apiKey": spec.apiKey},
				"models":  models,
			},
		},
	}
	argv := []string{"opencode", "--model", hubProviderID + "/" + spec.model}
	if spec.agent != nil {
		name := spec.agent.Namespace + "-" + spec.agent.Name
		config["agent"] = map[string]any{name: map[string]any{"mode": "primary", "model": hubProviderID + "/" + spec.model, "prompt": spec.agent.Instructions, "description": spec.agent.Description}}
		argv = append(argv, "--agent", name)
	}
	return invocation{
		argv: argv,
		env:  map[string]string{"OPENCODE_CONFIG_CONTENT": encodeLaunchJSON(config)},
	}
}

// piLaunch wires pi to Hub with a generated extension that registers one
// provider for the whole catalog. pi takes custom endpoints only from its
// models.json or an extension; an extension loaded with -e leaves the user's
// own pi directory, logins, and sessions alone. The key is read from the
// environment when pi resolves it, so it is never written to the file.
func piLaunch(spec launchSpec) invocation {
	type cost struct {
		Input      int `json:"input"`
		Output     int `json:"output"`
		CacheRead  int `json:"cacheRead"`
		CacheWrite int `json:"cacheWrite"`
	}
	type model struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		Reasoning     bool     `json:"reasoning"`
		Input         []string `json:"input"`
		Cost          cost     `json:"cost"`
		ContextWindow int      `json:"contextWindow"`
		MaxTokens     int      `json:"maxTokens"`
	}
	var models []model
	for _, m := range chatModels(spec.catalog) {
		window := m.ContextWindow
		if window == 0 {
			window = 128000
		}
		models = append(models, model{
			ID: m.ID, Name: m.ID, Input: []string{"text", "image"},
			ContextWindow: window, MaxTokens: outputLimit(window),
		})
	}
	config := map[string]any{
		"name":    providerName(spec),
		"baseUrl": spec.origin + "/v1",
		"apiKey":  "$" + launchKeyEnv,
		"api":     "openai-completions",
		"models":  models,
	}
	const file = "foldwise-pi.ts"
	source := "// Written by `tap launch pi` for this session only.\n" +
		"export default function (pi: any) {\n" +
		"  pi.registerProvider(\"" + hubProviderID + "\", " + encodeLaunchJSON(config) + ");\n" +
		"}\n"
	return invocation{
		argv:  []string{"pi", "-e", filepath.Join(spec.dir, file), "--provider", hubProviderID, "--model", spec.model},
		env:   map[string]string{launchKeyEnv: spec.apiKey},
		files: map[string]string{file: source},
	}
}

// chatModels drops models that cannot chat, such as speech to text.
func chatModels(catalog []HubModel) []HubModel {
	out := make([]HubModel, 0, len(catalog))
	for _, m := range catalog {
		if len(m.Capabilities) == 0 || slices.Contains(m.Capabilities, "chat") {
			out = append(out, m)
		}
	}
	return out
}

// outputLimit is how much of a context window a harness may spend on one
// reply. Relays advertise only the context; a quarter of it, capped, leaves
// room for the conversation while allowing a long reply.
func outputLimit(window int) int {
	return min(window/4, 32000)
}

func providerName(spec launchSpec) string {
	if spec.hubName == "" {
		return "Foldwise"
	}
	return "Foldwise (" + spec.hubName + ")"
}

// encodeLaunchJSON renders v without HTML escaping, so a dry run shows
// "<launch key>" as itself rather than as <launch key>.
func encodeLaunchJSON(v any) string {
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "{}"
	}
	return strings.TrimSpace(buf.String())
}

// LaunchHarnesses returns the launchable harness names, sorted. It backs shell
// completion the way IntegrateHosts does for `tap integrate`.
func LaunchHarnesses() []string {
	builders := harnessBuilders()
	out := make([]string, 0, len(builders))
	for name := range builders {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ResolveLaunch resolves options into a complete invocation without running it.
// Launch is this plus execution, so a dry run and a real run cannot drift.
func (t *Tap) ResolveLaunch(opts LaunchOptions) (*LaunchResult, error) {
	return t.ResolveLaunchContext(context.Background(), opts)
}

// ResolveLaunchContext is ResolveLaunch with a context for the one network
// call it makes: listing the Hub catalog.
func (t *Tap) ResolveLaunchContext(ctx context.Context, opts LaunchOptions) (*LaunchResult, error) {
	harness := strings.TrimSpace(opts.Harness)
	build, ok := harnessBuilders()[harness]
	if !ok {
		return nil, fmt.Errorf("unknown harness %q (available: %s)",
			opts.Harness, strings.Join(LaunchHarnesses(), ", "))
	}
	cfg, err := t.ConfigService.Config()
	if err != nil {
		return nil, err
	}
	if opts.Agent != "" && opts.Model != "" {
		return nil, fmt.Errorf("--model and --agent are mutually exclusive")
	}
	var agent *HubAgent
	if ref := strings.TrimSpace(opts.Agent); ref != "" {
		agent, err = t.HubAgent(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("agent %s: %w", ref, err)
		}
	}

	root, hasRoot, warnings, err := t.resolveLaunchRoot(cfg, opts.Flight)
	if err != nil {
		return nil, err
	}

	if agent != nil && !hasRoot && strings.TrimSpace(agent.Flight) != "" {
		root, err = ParseFlightRef(agent.Flight, agent.Namespace)
		if err != nil {
			return nil, fmt.Errorf("agent %s flight: %w", agent.Ref, err)
		}
		hasRoot, warnings = true, nil
	}

	if agent != nil && !hasRoot {
		warnings = []string{fmt.Sprintf("agent %s has no memory flight; KEG tools remain locked until a flight is assigned or explicitly selected", agent.Ref)}
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
	inference, err := launchInference(harness, opts.Inference)
	if err != nil {
		return nil, err
	}
	model := strings.TrimSpace(opts.Model)
	if agent != nil {
		model = agent.Model
	}
	var catalog []HubModel
	switch inference {
	case LaunchInferenceSubscription:
		if model != "" && !IsClaudeModel(model) {
			warnings = append(warnings, fmt.Sprintf("model %q is a Hub model; a subscription launch starts on Claude Code's default instead", model))
			model = ""
		}
	case LaunchInferenceSplit:
		// Claude models work without Hub's catalog, so a hub that cannot
		// list one degrades the launch rather than failing it.
		catalog, err = fetchHubCatalog(ctx, hubURL, token())
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("hub %q: %v; launching with Claude models only", hubName, err))
		}
		if model != "" && !IsClaudeModel(model) && !hubCatalogHas(chatModels(catalog), model) {
			return nil, fmt.Errorf("model %q is neither a Claude model nor in your catalog on hub %q (is a relay serving it connected?)", model, hubName)
		}
	default:
		catalog, err = fetchHubCatalog(ctx, hubURL, token())
		if err != nil {
			return nil, fmt.Errorf("hub %q: %w", hubName, err)
		}
		chat := chatModels(catalog)
		if len(chat) == 0 {
			return nil, fmt.Errorf("hub %q has no models for you yet; run `tap relay` to contribute your own", hubName)
		}
		if model == "" {
			model = chat[0].ID
		} else if !hubCatalogHas(chat, model) {
			return nil, fmt.Errorf("model %q is not in your catalog on hub %q (is a relay serving it connected?); available: %s",
				model, hubName, strings.Join(hubCatalogIDs(chat), ", "))
		}
	}

	tailEnv, err := t.launchTapEnv(cfg, root, hasRoot)
	if err != nil {
		return nil, err
	}
	agentRef := ""
	if agent != nil {
		agentRef = agent.Ref
		tailEnv["TAP_AGENT"] = agentRef
	}
	tailEnv["TAP_HARNESS"] = harness
	if model != "" {
		tailEnv["TAP_MODEL"] = model
	}
	spec := launchSpec{hubName: hubName, model: model, catalog: catalog, agent: agent}
	if harness == "claude" {
		spec.inference = inference
		more, err := t.resolveClaudeExtras(ctx, &spec, hubURL, token(), opts)
		if err != nil {
			return nil, err
		}
		warnings = append(warnings, more...)
	}
	plan := &launchPlan{
		hubURL:   hubURL,
		token:    token,
		build:    build,
		spec:     spec,
		tailArgs: append([]string(nil), opts.Args...),
		tailEnv:  tailEnv,
	}
	if spec.inference == LaunchInferenceSplit {
		plan.anthropic = t.Runtime.Env().Get("ANTHROPIC_BASE_URL")
		if plan.anthropic == "" {
			plan.anthropic = DefaultAnthropicURL
		}
	}
	inv := plan.render(launchForwarderPlaceholder, launchKeyPlaceholder, launchDirPlaceholder)
	flight := ""
	if hasRoot {
		flight = root.Canonical()
	}
	return &LaunchResult{
		Inference: spec.inference,
		HubAgent:  agentRef,
		Hub:       hubName,
		Harness:   harness,
		Model:     model,
		Flight:    flight,
		Argv:      inv.argv,
		Env:       inv.env,
		StripEnv:  inv.strip,
		Files:     inv.files,
		Warnings:  warnings,
		plan:      plan,
	}, nil
}

// launchInference validates a launch's inference mode, defaulting Claude Code
// to split and every other harness to Hub, the only mode they support.
func launchInference(harness, mode string) (string, error) {
	mode = strings.TrimSpace(mode)
	if harness != "claude" {
		if mode != "" && mode != LaunchInferenceHub {
			return "", fmt.Errorf("%s launches use Hub models only; --%s is for claude", harness, mode)
		}
		return LaunchInferenceHub, nil
	}
	switch mode {
	case "":
		return LaunchInferenceSplit, nil
	case LaunchInferenceSplit, LaunchInferenceHub, LaunchInferenceSubscription:
		return mode, nil
	}
	return "", fmt.Errorf("unknown inference mode %q (want %s, %s or %s)", mode, LaunchInferenceSplit, LaunchInferenceHub, LaunchInferenceSubscription)
}

// resolveClaudeExtras fills in a Claude Code launch's subagents and the relayed
// tool names they get. What cannot be fetched is left out with a warning: neither is
// needed to start the session.
func (t *Tap) resolveClaudeExtras(ctx context.Context, spec *launchSpec, hubURL, token string, opts LaunchOptions) ([]string, error) {
	var warnings []string
	spec.builtins = opts.SubagentBuiltins
	if spec.builtins == nil {
		spec.builtins = DefaultSubagentBuiltins
	}
	spec.relayAllowed = spec.agent != nil && spec.agent.RelayTools

	switch mode := strings.TrimSpace(opts.Subagents); mode {
	case "none":
	case "all":
		all, err := ListHubAgents(ctx, hubURL, token, "")
		if err != nil {
			return nil, fmt.Errorf("list agents for subagents: %w", err)
		}
		for _, a := range all {
			if spec.agent == nil || a.Ref != spec.agent.Ref {
				spec.subagents = append(spec.subagents, a)
			}
		}
	case "":
		if spec.agent == nil {
			break
		}
		for _, ref := range spec.agent.Subagents {
			a, err := t.HubAgent(ctx, ref)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("subagent %s skipped: %v", ref, err))
				continue
			}
			spec.subagents = append(spec.subagents, *a)
		}
	default:
		return nil, fmt.Errorf("unknown --subagents value %q (want all or none)", mode)
	}

	needHosted, needRelay := false, false
	for _, a := range spec.subagents {
		needHosted = needHosted || len(a.ToolNames()) == 0
		needRelay = needRelay || (a.RelayTools && spec.relayAllowed)
	}
	if needHosted {
		if tools, err := GetHubTools(ctx, hubURL, token); err != nil {
			warnings = append(warnings, fmt.Sprintf("subagents allowed every Hub tool get none: %v", err))
		} else {
			spec.hostedTools = append(slices.Clone(tools.Tools), tools.AlwaysAvailable...)
			slices.Sort(spec.hostedTools)
			spec.hostedTools = slices.Compact(spec.hostedTools)
		}
	}
	if needRelay {
		if names, err := ListHubRelayToolNames(ctx, hubURL, token); err != nil {
			warnings = append(warnings, fmt.Sprintf("subagents get no relayed tools: %v", err))
		} else {
			spec.relayTools = names
		}
	}
	return warnings, nil
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

// launchTapEnv is the TAP_* overlay every launch exports. TAP_FLIGHT pins the
// canonical launch root for the child process lifetime; governed calls may
// select a live accessible descendant but never replace that root. It is left
// unset when no flight is configured, which is exactly how the child's `tap
// mcp` decides it is not launcher-bound and resolves identity authority
// instead (see cmd_mcp.go).
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

// fetchHubCatalog lists the caller's models from Hub's inference endpoint.
func fetchHubCatalog(ctx context.Context, hubURL, token string) ([]HubModel, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("not signed in; run `tap auth login`")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hubURL+hubOpenAIPath+"/models", nil)
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

// Launch resolves the model and starts the harness, wiring it to the runtime's
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

	// Up before the harness and down after it, so every request the harness
	// makes has somewhere to go.
	fw, err := startLaunchForwarderWith(launchForwarderConfig{
		hubURL: resolved.plan.hubURL, harness: resolved.Harness, token: resolved.plan.token,
		anthropic: resolved.plan.anthropic,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = fw.Close() }()
	dir := ""
	if len(resolved.Files) > 0 {
		// Named after the forwarder's secret, so it is unguessable and
		// private to this launch.
		dir = filepath.Join(t.Runtime.GetTempDir(), "tap-launch-"+fw.Secret()[len(fw.Secret())-16:])
		if err := t.Runtime.Mkdir(dir, 0o700, true); err != nil {
			return nil, fmt.Errorf("create launch directory: %w", err)
		}
		defer func() { _ = t.Runtime.Remove(dir, true) }()
	}
	inv := resolved.plan.render(fw.Origin(), fw.Secret(), dir)
	for name, content := range inv.files {
		if err := t.Runtime.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			return nil, fmt.Errorf("write launch file %s: %w", name, err)
		}
	}

	cmd := exec.CommandContext(ctx, inv.argv[0], inv.argv[1:]...)
	stream := t.Runtime.Stream()
	cmd.Stdin = stream.In
	cmd.Stdout = stream.Out
	cmd.Stderr = stream.Err
	cmd.Env = append(stripEnv(t.Runtime.Environ(), inv.strip), envPairs(inv.env)...)
	if err := cmd.Run(); err != nil {
		return resolved, fmt.Errorf("%s exited: %w", resolved.Harness, err)
	}
	return resolved, nil
}

// stripEnv removes the named variables from a KEY=VALUE environment. An
// overlay can override a variable's value but never make it absent, which is
// what keeping a stray provider key away from a harness needs.
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
