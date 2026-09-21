package tapper

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// localIntegrationMarketplace is the name the host CLIs know the extracted
// tree by. Plugin ids are always <plugin>@tapper-local.
const localIntegrationMarketplace = "tapper-local"

// marketplaceHost installs through a host CLI that owns a plugin marketplace:
// tap extracts the embedded tree, hands its path to the host, and lets the host
// decide where the resulting state lives. Claude and Codex both work this way;
// their differences are the fields below rather than branches in the flow.
type marketplaceHost struct {
	name string
	// manifestDir is the per-plugin manifest directory inside the rendered
	// tree, for example ".claude-plugin".
	manifestDir string
	// marketplacePath is the marketplace manifest inside the rendered tree.
	marketplacePath string
	// scopes are the accepted --scope values, most-default first.
	scopes []string
	// scopeError explains a rejected scope in the host's own terms.
	scopeError func(scope string) error
	// scopeFlag reports whether the host CLI takes --scope. When false the
	// host keeps a single install location and its reported state carries no
	// scope to filter on.
	scopeFlag bool
	// refreshByReinstall reports whether an already-installed plugin has to be
	// removed before being added back. Codex caches a plugin at install time
	// and has no update verb, so `plugin add` alone silently keeps the stale
	// copy; Claude re-reads the registered path and needs only `plugin update`.
	refreshByReinstall bool
	// baselineHooks reports whether the baseline plugin itself carries hooks.
	// Codex does (SessionStart orientation); Claude ships hooks only in the
	// separate tapper-guard plugin.
	baselineHooks bool
	// parseMarketplaces reads the host's `plugin marketplace list --json`.
	parseMarketplaces func(body []byte) ([]marketplaceEntry, error)
	// parseInstalled reads the host's `plugin list --json`.
	parseInstalled func(body []byte) ([]installedPlugin, error)
}

// marketplaceEntry is one registered marketplace as the host reports it.
type marketplaceEntry struct {
	Name  string
	Root  string
	Scope string
}

// installedPlugin is one installed plugin as the host reports it.
type installedPlugin struct {
	ID    string
	Scope string
}

func claudeIntegrationHost() *marketplaceHost {
	return &marketplaceHost{
		name:               "claude",
		manifestDir:        ".claude-plugin",
		marketplacePath:    ".claude-plugin/marketplace.json",
		scopes:             []string{"user", "project", "local"},
		scopeFlag:          true,
		refreshByReinstall: false,
		scopeError: func(scope string) error {
			return fmt.Errorf("integrate: invalid Claude scope %q; use user, project, or local", scope)
		},
		parseMarketplaces: parseClaudeMarketplaces,
		parseInstalled:    parseClaudeInstalled,
	}
}

func codexIntegrationHost() *marketplaceHost {
	return &marketplaceHost{
		name:               "codex",
		baselineHooks:      true,
		manifestDir:        ".codex-plugin",
		marketplacePath:    ".agents/plugins/marketplace.json",
		scopes:             []string{"user"},
		scopeFlag:          false,
		refreshByReinstall: true,
		scopeError: func(scope string) error {
			// Not a Tapper limitation: `codex plugin`, `codex plugin add`, and
			// `codex plugin marketplace add` expose no scope flag, and Codex
			// keeps plugin state only in ~/.codex/config.toml. There is nothing
			// for a project scope to write.
			return fmt.Errorf(
				"integrate: Codex has no %s plugin scope: the codex CLI installs plugins only into ~/.codex/config.toml. Use --scope user, or `tap integrate claude` for project-level activation",
				scope)
		},
		parseMarketplaces: parseCodexMarketplaces,
		parseInstalled:    parseCodexInstalled,
	}
}

func (h *marketplaceHost) Name() string     { return h.name }
func (h *marketplaceHost) Scopes() []string { return h.scopes }

// RequiresHookSupport reports whether this install needs a `tap hook`-capable
// binary on PATH. Codex ships a SessionStart hook in the baseline plugin, so
// it always does; Claude's hooks all live in tapper-guard, so a --no-safety
// install there has no hook to verify.
func (h *marketplaceHost) RequiresHookSupport(plugins []string) bool {
	return h.baselineHooks || selectionIncludesGuard(plugins)
}
func (h *marketplaceHost) MarketplacePath() string { return h.marketplacePath }

func (h *marketplaceHost) ScopeError(scope string) error { return h.scopeError(scope) }

func (h *marketplaceHost) PluginManifestPath(plugin string) string {
	return path.Join(plugin, h.manifestDir, "plugin.json")
}

// Plan reports the fresh-install sequence. A dry run deliberately starts no
// host process, so it cannot know whether the marketplace is registered or a
// plugin already installed. A real run against an existing install inserts the
// corresponding removes — see marketplaceCommands and pluginCommands.
func (h *marketplaceHost) Plan(t *Tap, p integratePlan) (*IntegrateResult, error) {
	targets, err := extractionTargets(h.name, p.Root)
	if err != nil {
		return nil, err
	}
	commands := [][]string{
		append([]string{h.name}, marketplaceListArgs()...),
		append([]string{h.name}, pluginListArgs()...),
	}
	for _, args := range h.marketplaceCommands(p.Root, p.Scope, false) {
		commands = append(commands, append([]string{h.name}, args...))
	}
	for _, name := range p.Plugins {
		for _, args := range h.pluginCommands(name+"@"+localIntegrationMarketplace, p.Scope, false) {
			commands = append(commands, append([]string{h.name}, args...))
		}
	}
	return &IntegrateResult{Root: p.Root, Paths: targets, Commands: commands}, nil
}

func (h *marketplaceHost) Apply(ctx context.Context, t *Tap, p integratePlan) error {
	executable, err := t.integrationExecutable(h.name)
	if err != nil {
		return err
	}

	marketplaces, err := t.integrationJSONCommand(ctx, executable, marketplaceListArgs())
	if err != nil {
		return fmt.Errorf("integrate: list %s marketplaces: %w", h.name, err)
	}
	registered, err := h.checkMarketplaceState(marketplaces, p.Root, p.Scope)
	if err != nil {
		return err
	}
	plugins, err := t.integrationJSONCommand(ctx, executable, pluginListArgs())
	if err != nil {
		return fmt.Errorf("integrate: list %s plugins: %w", h.name, err)
	}
	installed, err := h.installedPluginIDs(plugins, p.Scope)
	if err != nil {
		return fmt.Errorf("integrate: parse %s plugin list: %w", h.name, err)
	}

	if err := t.extractIntegration(h, p.Root, p.Plugins); err != nil {
		return err
	}
	// The marketplace goes first: Codex installs plugins from its snapshot, so
	// recapturing it has to happen before any plugin is added back.
	for _, args := range h.marketplaceCommands(p.Root, p.Scope, registered) {
		if err := t.runIntegrationCommand(ctx, executable, args); err != nil {
			return fmt.Errorf("integrate: register %s marketplace: %w", h.name, err)
		}
	}

	// Each plugin's removal sits immediately before its own add, so a failure
	// part-way through leaves at most one plugin missing rather than all of them.
	for _, name := range p.Plugins {
		id := name + "@" + localIntegrationMarketplace
		for _, args := range h.pluginCommands(id, p.Scope, installed[id]) {
			if err := t.runIntegrationCommand(ctx, executable, args); err != nil {
				return fmt.Errorf("integrate: install %s with %s: %w", id, h.name, err)
			}
		}
	}
	return nil
}

func marketplaceListArgs() []string { return []string{"plugin", "marketplace", "list", "--json"} }

func pluginListArgs() []string { return []string{"plugin", "list", "--json"} }

// marketplaceCommands returns the commands that make the host's view of the
// marketplace match the freshly extracted tree, given whether it is already
// registered.
//
// Codex serves plugins from a snapshot taken when the marketplace was added, so
// re-extracting files it has already snapshotted changes nothing. Its
// `marketplace upgrade` only refreshes Git sources, which a local marketplace is
// not, leaving remove-then-add as the sole way to recapture. Claude reads the
// registered path directly, so a re-register would be pure churn.
func (h *marketplaceHost) marketplaceCommands(root, scope string, registered bool) [][]string {
	add := []string{"plugin", "marketplace", "add", root}
	if h.scopeFlag {
		if registered {
			return nil
		}
		return [][]string{append(add, "--scope", scope)}
	}
	if registered {
		return [][]string{
			{"plugin", "marketplace", "remove", localIntegrationMarketplace},
			add,
		}
	}
	return [][]string{add}
}

// pluginCommands returns the commands that install or refresh one plugin.
//
// Codex has no update verb and caches the plugin at install time, so an
// already-installed plugin must be removed before adding it back or the new
// content never lands — `plugin add` alone silently keeps the cached copy.
func (h *marketplaceHost) pluginCommands(id, scope string, installed bool) [][]string {
	if !h.refreshByReinstall {
		if installed {
			return [][]string{{"plugin", "update", id, "--scope", scope}}
		}
		return [][]string{{"plugin", "install", id, "--scope", scope}}
	}
	add := []string{"plugin", "add", id}
	if installed {
		return [][]string{{"plugin", "remove", id}, add}
	}
	return [][]string{add}
}

func (h *marketplaceHost) checkMarketplaceState(body []byte, expectedRoot, scope string) (bool, error) {
	entries, err := h.parseMarketplaces(body)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Name != localIntegrationMarketplace || (h.scopeFlag && entry.Scope != scope) {
			continue
		}
		if sameIntegrationPath(entry.Root, expectedRoot) {
			return true, nil
		}
		return false, fmt.Errorf("integrate: marketplace %q already points to %s, expected %s; refusing to replace it", localIntegrationMarketplace, entry.Root, expectedRoot)
	}
	return false, nil
}

func (h *marketplaceHost) installedPluginIDs(body []byte, scope string) (map[string]bool, error) {
	plugins, err := h.parseInstalled(body)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, plugin := range plugins {
		if h.scopeFlag && plugin.Scope != scope {
			continue
		}
		out[plugin.ID] = true
	}
	return out, nil
}

func parseClaudeMarketplaces(body []byte) ([]marketplaceEntry, error) {
	var response []struct {
		Name            string `json:"name"`
		Path            string `json:"path"`
		InstallLocation string `json:"installLocation"`
		Scope           string `json:"scope"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("integrate: parse claude marketplace list: %w", err)
	}
	entries := make([]marketplaceEntry, 0, len(response))
	for _, item := range response {
		root := item.Path
		if root == "" {
			root = item.InstallLocation
		}
		entries = append(entries, marketplaceEntry{
			Name:  item.Name,
			Root:  root,
			Scope: defaultScope(item.Scope),
		})
	}
	return entries, nil
}

func parseClaudeInstalled(body []byte) ([]installedPlugin, error) {
	var response []struct {
		ID    string `json:"id"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	out := make([]installedPlugin, 0, len(response))
	for _, plugin := range response {
		out = append(out, installedPlugin{ID: plugin.ID, Scope: defaultScope(plugin.Scope)})
	}
	return out, nil
}

func parseCodexMarketplaces(body []byte) ([]marketplaceEntry, error) {
	var response struct {
		Marketplaces []struct {
			Name string `json:"name"`
			Root string `json:"root"`
		} `json:"marketplaces"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("integrate: parse codex marketplace list: %w", err)
	}
	entries := make([]marketplaceEntry, 0, len(response.Marketplaces))
	for _, item := range response.Marketplaces {
		entries = append(entries, marketplaceEntry{Name: item.Name, Root: item.Root})
	}
	return entries, nil
}

func parseCodexInstalled(body []byte) ([]installedPlugin, error) {
	var response struct {
		Installed []struct {
			PluginID string `json:"pluginId"`
		} `json:"installed"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	out := make([]installedPlugin, 0, len(response.Installed))
	for _, plugin := range response.Installed {
		out = append(out, installedPlugin{ID: plugin.PluginID})
	}
	return out, nil
}

// defaultScope treats an absent scope as "user", which is what every host means
// by it and what a pre-scope release of the host CLI reports.
func defaultScope(scope string) string {
	if scope == "" {
		return "user"
	}
	return scope
}

func sameIntegrationPath(a, b string) bool {
	return filepath.Clean(strings.TrimSpace(a)) == filepath.Clean(strings.TrimSpace(b))
}
