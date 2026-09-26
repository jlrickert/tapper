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
	// Model is a Hub catalog id. Empty starts on the first model in the
	// catalog, which Hub orders by the relay owners' preference.
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
	// Hub names the hub serving the models.
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
}

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
	hubURL   string
	token    func() string
	build    func(launchSpec) invocation
	spec     launchSpec
	tailArgs []string
	tailEnv  map[string]string
}

// render builds the invocation against a forwarder at origin taking apiKey,
// with its files in dir.
func (p *launchPlan) render(origin, apiKey, dir string) invocation {
	spec := p.spec
	spec.origin, spec.apiKey, spec.dir = origin, apiKey, dir
	inv := p.build(spec)
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

// claudeLaunch points Claude Code at Hub's Anthropic Messages endpoint. Every
// model slot it uses, including the small one for background work, is the
// selected model, so nothing leaves Hub. An inherited ANTHROPIC_API_KEY is
// removed: Claude Code would otherwise send it in preference, and warn about
// the conflict. Claude Code does not know catalog models, so their context
// window, when the relay advertised one, is passed as the limit it compacts
// against; otherwise it assumes 200k.
func claudeLaunch(spec launchSpec) invocation {
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
	return invocation{
		argv:  []string{"claude", "--model", spec.model},
		env:   env,
		strip: []string{"ANTHROPIC_API_KEY"},
	}
}

// codexLaunch declares Hub as a Codex model provider on the command line.
// Codex only speaks the Responses API, so the provider's wire API is
// "responses", and it reads the key from the variable env_key names. No
// OPENAI_API_KEY is set: Codex treats one alongside a stored ChatGPT login as
// mixed auth, and this provider does not use it.
func codexLaunch(spec launchSpec) invocation {
	name := "Foldwise"
	if spec.hubName != "" {
		name += " (" + spec.hubName + ")"
	}
	provider := fmt.Sprintf(`model_providers.%s={name=%s, base_url=%s, env_key=%s, wire_api="responses"}`,
		hubProviderID, strconv.Quote(name), strconv.Quote(spec.origin+"/v1"), strconv.Quote(launchKeyEnv))
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
	return invocation{
		argv: []string{"opencode", "--model", hubProviderID + "/" + spec.model},
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
		if len(m.Capabilities) == 0 {
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
	root, hasRoot, warnings, err := t.resolveLaunchRoot(cfg, opts.Flight)
	if err != nil {
		return nil, err
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
	chat := chatModels(catalog)
	if len(chat) == 0 {
		return nil, fmt.Errorf("hub %q has no models for you yet; run `tap relay` to contribute your own", hubName)
	}
	model := strings.TrimSpace(opts.Model)
	if model == "" {
		model = chat[0].ID
	} else if !hubCatalogHas(chat, model) {
		return nil, fmt.Errorf("model %q is not in your catalog on hub %q (is its relay connected?); available: %s",
			model, hubName, strings.Join(hubCatalogIDs(chat), ", "))
	}

	tailEnv, err := t.launchTapEnv(cfg, root, hasRoot)
	if err != nil {
		return nil, err
	}
	tailEnv["TAP_HARNESS"] = harness
	tailEnv["TAP_MODEL"] = model
	plan := &launchPlan{
		hubURL:   hubURL,
		token:    token,
		build:    build,
		spec:     launchSpec{hubName: hubName, model: model, catalog: catalog},
		tailArgs: append([]string(nil), opts.Args...),
		tailEnv:  tailEnv,
	}
	inv := plan.render(launchForwarderPlaceholder, launchKeyPlaceholder, launchDirPlaceholder)
	flight := ""
	if hasRoot {
		flight = root.Canonical()
	}
	return &LaunchResult{
		Hub:      hubName,
		Harness:  harness,
		Model:    model,
		Flight:   flight,
		Argv:     inv.argv,
		Env:      inv.env,
		StripEnv: inv.strip,
		Files:    inv.files,
		Warnings: warnings,
		plan:     plan,
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
	fw, err := startLaunchForwarder(resolved.plan.hubURL, resolved.plan.token)
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
