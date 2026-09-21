package tapper

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// opencodeMCPServerName is the key tap owns inside the host config's `mcp`
// block. Anything else under `mcp` belongs to the user and is preserved.
const opencodeMCPServerName = "tapper"

// opencodeConfigSchema is written into a config file tap creates, so editors
// validate and complete the file the user inherits from us.
const opencodeConfigSchema = "https://opencode.ai/config.json"

// opencodeHost installs by writing opencode's own configuration.
//
// opencode has no plugin marketplace to hand a directory to: it reads MCP
// servers from an `mcp` block in opencode.json and discovers Agent Skills by
// directory. So unlike marketplaceHost, tap owns the destination paths here and
// must merge rather than overwrite — the config file it writes is one the user
// also edits.
type opencodeHost struct{}

func (opencodeHost) Name() string { return "opencode" }

// Scopes omits "local". Claude's local scope means gitignored project settings;
// opencode has no such tier, and accepting the word while silently writing
// shared project state would be worse than rejecting it.
func (opencodeHost) Scopes() []string { return []string{"user", "project"} }

func (opencodeHost) ScopeError(scope string) error {
	return fmt.Errorf(
		"integrate: opencode has no %q scope: it reads a user config and a project config and nothing gitignored in between. Use --scope user or --scope project",
		scope)
}

// RequiresHookSupport follows the guard. opencode cannot read the JSON hook
// table Claude and Codex consume, so its guard is a plugin module that shells
// out to `tap hook pre-tool-use` itself — and because that guard fails closed,
// installing it against a tap that cannot answer would block every tool call
// in the session. Without the guard nothing here runs `tap hook`.
func (opencodeHost) RequiresHookSupport(plugins []string) bool {
	return selectionIncludesGuard(plugins)
}

func (opencodeHost) MarketplacePath() string { return ".tapper/marketplace.json" }

func (opencodeHost) PluginManifestPath(plugin string) string {
	return path.Join(plugin, ".tapper", "plugin.json")
}

// opencodeDest is where one scope's install lands.
type opencodeDest struct {
	// Config is the opencode.json tap merges the MCP server into.
	Config string
	// Skills is the directory holding one subdirectory per installed plugin.
	Skills string
	// Plugin is opencode's auto-load plugin directory. The name is singular:
	// opencode loads every module in <scope>/plugin at startup, which is how
	// the guard installs without an entry in the user's config.
	Plugin string
}

// opencodeGuardFile is the guard module's basename inside opencodeDest.Plugin.
// tap owns this exact path, which is why --no-safety can remove it: opencode
// has no command to disable a plugin, so skipping the install alone would
// leave a guard running with no off switch.
const opencodeGuardFile = guardIntegrationPlugin + ".ts"

func (h opencodeHost) dest(t *Tap, scope string) (opencodeDest, error) {
	switch scope {
	case "user":
		root, err := toolkit.UserConfigPath(t.Runtime.Env())
		if err != nil {
			return opencodeDest{}, fmt.Errorf("integrate: resolve user config directory: %w", err)
		}
		base := filepath.Join(root, "opencode")
		return opencodeDest{
			Config: filepath.Join(base, "opencode.json"),
			Skills: filepath.Join(base, "skills"),
			Plugin: filepath.Join(base, "plugin"),
		}, nil
	case "project":
		// PathService.Project() is the project's .tapper directory, so its
		// parent is the project root opencode itself searches from.
		base := filepath.Dir(t.PathService.Project())
		return opencodeDest{
			Config: filepath.Join(base, "opencode.json"),
			Skills: filepath.Join(base, ".opencode", "skills"),
			Plugin: filepath.Join(base, ".opencode", "plugin"),
		}, nil
	default:
		return opencodeDest{}, h.ScopeError(scope)
	}
}

func (h opencodeHost) Plan(t *Tap, p integratePlan) (*IntegrateResult, error) {
	targets, err := extractionTargets(h.Name(), p.Root)
	if err != nil {
		return nil, err
	}
	dest, err := h.dest(t, p.Scope)
	if err != nil {
		return nil, err
	}
	if err := h.checkJSONCConflict(t, dest.Config); err != nil {
		return nil, err
	}

	var steps []string
	for _, name := range p.Plugins {
		if name == guardIntegrationPlugin {
			continue
		}
		steps = append(steps, filepath.Join(dest.Skills, name)+string(filepath.Separator))
	}
	steps = append(steps, h.guardStep(t, dest, p.Plugins)...)
	action := "merge mcp.tapper into"
	if _, err := t.Runtime.Stat(dest.Config, false); err != nil {
		action = "create"
	}
	steps = append(steps, fmt.Sprintf("%s %s", action, dest.Config))
	return &IntegrateResult{Root: p.Root, Paths: targets, Steps: steps}, nil
}

func (h opencodeHost) Apply(ctx context.Context, t *Tap, p integratePlan) error {
	dest, err := h.dest(t, p.Scope)
	if err != nil {
		return err
	}
	if err := h.checkJSONCConflict(t, dest.Config); err != nil {
		return err
	}
	// Read and vet the user's config before anything is written. A foreign
	// mcp.tapper has to fail while the install is still untouched, so a refusal
	// never leaves half of one behind.
	config, err := h.readConfig(t, dest.Config)
	if err != nil {
		return err
	}

	if err := t.extractIntegration(h, p.Root, p.Plugins); err != nil {
		return err
	}
	entry, err := h.mcpEntry(t, p.Root)
	if err != nil {
		return err
	}
	merged, err := marshalOpenCodeConfig(setOpenCodeServer(config, entry))
	if err != nil {
		return err
	}

	for _, name := range p.Plugins {
		src := filepath.Join(p.Root, name, "skills", name)
		// The guard ships a plugin module and no skill, so there is nothing
		// here to copy. Without this the whole install fails on it.
		if _, err := t.Runtime.Stat(src, false); err != nil {
			if isNotExist(err) {
				continue
			}
			return fmt.Errorf("integrate: inspect opencode skill %s: %w", src, err)
		}
		dst := filepath.Join(dest.Skills, name)
		// Remove first so a refresh drops files a previous version shipped and
		// this one does not; a plain overwrite would leave them behind.
		if err := t.Runtime.Remove(dst, true); err != nil && !isNotExist(err) {
			return fmt.Errorf("integrate: replace opencode skill %s: %w", dst, err)
		}
		if err := copyRuntimeTree(t, src, dst); err != nil {
			return fmt.Errorf("integrate: install opencode skill %s: %w", name, err)
		}
	}
	if err := h.applyGuard(t, p.Root, dest, p.Plugins); err != nil {
		return err
	}
	return writeFileAtomic(t, dest.Config, merged)
}

// guardStep describes what Apply will do with the guard module, so a dry run
// reports the removal as plainly as the install.
func (h opencodeHost) guardStep(t *Tap, dest opencodeDest, plugins []string) []string {
	target := filepath.Join(dest.Plugin, opencodeGuardFile)
	if selectionIncludesGuard(plugins) {
		return []string{"write " + target}
	}
	if _, err := t.Runtime.Stat(target, false); err != nil {
		return nil
	}
	return []string{"remove " + target}
}

// applyGuard installs or removes the guard module. opencode loads plugins from
// a directory rather than from a registry, so both directions are file
// operations tap performs itself.
func (h opencodeHost) applyGuard(t *Tap, root string, dest opencodeDest, plugins []string) error {
	target := filepath.Join(dest.Plugin, opencodeGuardFile)
	if !selectionIncludesGuard(plugins) {
		if err := t.Runtime.Remove(target, false); err != nil && !isNotExist(err) {
			return fmt.Errorf("integrate: remove opencode guard %s: %w", target, err)
		}
		return nil
	}
	body, err := t.Runtime.ReadFile(filepath.Join(root, guardIntegrationPlugin, "plugin", opencodeGuardFile))
	if err != nil {
		return fmt.Errorf("integrate: read embedded opencode guard: %w", err)
	}
	if err := writeFileAtomic(t, target, body); err != nil {
		return fmt.Errorf("integrate: install opencode guard %s: %w", target, err)
	}
	return nil
}

// checkJSONCConflict refuses to work around a JSONC config.
//
// opencode accepts opencode.jsonc, and encoding/json cannot round-trip its
// comments. Writing the sibling .json would shadow settings the user thinks are
// live, and rewriting the .jsonc would silently delete their comments. Saying so
// is the only honest option.
func (h opencodeHost) checkJSONCConflict(t *Tap, configPath string) error {
	if _, err := t.Runtime.Stat(configPath, false); err == nil {
		return nil
	}
	jsonc := configPath + "c"
	if _, err := t.Runtime.Stat(jsonc, false); err != nil {
		return nil
	}
	return fmt.Errorf(
		"integrate: %s exists and tap cannot edit it without discarding its comments. Add this to its \"mcp\" block by hand:\n    \"tapper\": { \"type\": \"local\", \"command\": [\"tap\", \"mcp\"], \"enabled\": true }",
		jsonc)
}

// mcpEntry reads the rendered fragment and returns just the server definition
// tap owns, so the shape lives in the render adapter rather than here.
func (h opencodeHost) mcpEntry(t *Tap, root string) (json.RawMessage, error) {
	filename := filepath.Join(root, "tapper", "opencode.json")
	body, err := t.Runtime.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("integrate: read embedded opencode config fragment: %w", err)
	}
	var fragment struct {
		MCP map[string]json.RawMessage `json:"mcp"`
	}
	if err := json.Unmarshal(body, &fragment); err != nil {
		return nil, fmt.Errorf("integrate: parse embedded opencode config fragment: %w", err)
	}
	entry, ok := fragment.MCP[opencodeMCPServerName]
	if !ok {
		return nil, fmt.Errorf("integrate: embedded opencode fragment has no mcp.%s server", opencodeMCPServerName)
	}
	return entry, nil
}

// openCodeConfig is a host config being edited: every key the user already had,
// carried as raw JSON so a setting tap does not understand survives untouched.
type openCodeConfig struct {
	top     map[string]json.RawMessage
	servers map[string]json.RawMessage
}

// readConfig loads the host config and verifies tap may write to it. A missing
// file is not an error — it is the fresh-install case, and the schema reference
// goes in so the file the user inherits validates in their editor.
func (h opencodeHost) readConfig(t *Tap, configPath string) (*openCodeConfig, error) {
	config := &openCodeConfig{
		top:     map[string]json.RawMessage{},
		servers: map[string]json.RawMessage{},
	}
	body, err := t.Runtime.ReadFile(configPath)
	switch {
	case isNotExist(err):
		schema, err := json.Marshal(opencodeConfigSchema)
		if err != nil {
			return nil, err
		}
		config.top["$schema"] = schema
		return config, nil
	case err != nil:
		return nil, fmt.Errorf("integrate: read %s: %w", configPath, err)
	}
	if err := json.Unmarshal(body, &config.top); err != nil {
		return nil, fmt.Errorf("integrate: parse %s: %w", configPath, err)
	}
	if raw, ok := config.top["mcp"]; ok {
		if err := json.Unmarshal(raw, &config.servers); err != nil {
			return nil, fmt.Errorf("integrate: parse mcp block in %s: %w", configPath, err)
		}
	}
	if existing, ok := config.servers[opencodeMCPServerName]; ok {
		if err := checkOpenCodeServerOwnership(configPath, existing); err != nil {
			return nil, err
		}
	}
	return config, nil
}

// setOpenCodeServer installs the tapper MCP entry, leaving every other key as
// the user wrote it.
func setOpenCodeServer(config *openCodeConfig, entry json.RawMessage) *openCodeConfig {
	config.servers[opencodeMCPServerName] = entry
	return config
}

// checkOpenCodeServerOwnership refuses to replace an mcp.tapper that is not
// ours, the same way marketplaceHost refuses a tapper-local marketplace pointing
// somewhere unexpected. A user who wired their own tapper server — a wrapper
// script, a remote hub — should get an error, not a silent overwrite.
func checkOpenCodeServerOwnership(configPath string, existing json.RawMessage) error {
	var server struct {
		Command []string `json:"command"`
	}
	if err := json.Unmarshal(existing, &server); err != nil {
		return fmt.Errorf("integrate: parse mcp.%s in %s: %w", opencodeMCPServerName, configPath, err)
	}
	if len(server.Command) > 0 && server.Command[0] == "tap" {
		return nil
	}
	return fmt.Errorf(
		"integrate: %s already defines mcp.%s running %q; refusing to replace it",
		configPath, opencodeMCPServerName, server.Command)
}

// marshalOpenCodeConfig renders the merged config with the same 2-space,
// unescaped style the rendered fragments use, so a file tap writes reads like
// the one it shipped.
func marshalOpenCodeConfig(config *openCodeConfig) ([]byte, error) {
	servers, err := json.Marshal(config.servers)
	if err != nil {
		return nil, err
	}
	config.top["mcp"] = servers
	encoded, err := json.Marshal(config.top)
	if err != nil {
		return nil, err
	}
	var indented []byte
	indented, err = jsonIndent(encoded)
	if err != nil {
		return nil, err
	}
	return append(indented, '\n'), nil
}

// copyRuntimeTree copies a file or directory through the runtime, so writes stay
// inside the sandbox jail the Runtime Abstraction Rule requires.
func copyRuntimeTree(t *Tap, src, dst string) error {
	info, err := t.Runtime.Stat(src, false)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		body, err := t.Runtime.ReadFile(src)
		if err != nil {
			return err
		}
		return t.Runtime.WriteFile(dst, body, 0o644)
	}
	entries, err := t.Runtime.ReadDir(src)
	if err != nil {
		return err
	}
	// Sorted so a partial failure aborts at the same place every run, which is
	// what makes the failure reproducible.
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if len(names) == 0 {
		return t.Runtime.Mkdir(dst, 0o755, true)
	}
	for _, name := range names {
		if err := copyRuntimeTree(t, filepath.Join(src, name), filepath.Join(dst, name)); err != nil {
			return err
		}
	}
	return nil
}

// writeFileAtomic writes through a sibling temp file so a crash mid-write cannot
// leave the user with a truncated opencode.json.
func writeFileAtomic(t *Tap, filename string, body []byte) error {
	tmp := filename + ".tap-tmp"
	if err := t.Runtime.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("integrate: write %s: %w", tmp, err)
	}
	if err := t.Runtime.Rename(tmp, filename); err != nil {
		_ = t.Runtime.Remove(tmp, false)
		return fmt.Errorf("integrate: activate %s: %w", filename, err)
	}
	return nil
}
