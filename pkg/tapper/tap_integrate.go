package tapper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/integrations"
)

const localIntegrationMarketplace = "tapper-local"

// IntegrateOptions selects the host, native install scope, dry-run behavior,
// and optional plugins for native plugin installation. The baseline tapper
// plugin is always installed.
type IntegrateOptions struct {
	KegTargetOptions
	Host    string
	DryRun  bool
	Plugins []string
	Scope   string
}

// IntegrateResult describes the extracted marketplace and the host commands
// used to inspect, register, and install it.
type IntegrateResult struct {
	Root     string
	Paths    []string
	Commands [][]string
}

// Integrate atomically refreshes the embedded marketplace for one host,
// reuses a matching registration, and installs the requested plugins through
// the host CLI. Dry-run returns the complete plan without side effects.
func (t *Tap) Integrate(ctx context.Context, opts IntegrateOptions) (*IntegrateResult, error) {
	host := strings.TrimSpace(opts.Host)
	if host == "" {
		return nil, fmt.Errorf("integrate: host is required")
	}
	if !integrationHostExists(host) {
		return nil, fmt.Errorf("integrate: unknown host %q", host)
	}
	scope, err := integrationScope(host, opts.Scope)
	if err != nil {
		return nil, err
	}
	selected, err := selectedIntegrationPlugins(host, opts.Plugins)
	if err != nil {
		return nil, err
	}

	dataDir, err := toolkit.UserDataPath(t.Runtime.Env())
	if err != nil {
		return nil, fmt.Errorf("integrate: resolve user data directory: %w", err)
	}
	root := filepath.Join(dataDir, "tapper", "integrations", host)
	result, err := integrationPreview(host, root, scope, selected)
	if err != nil {
		return nil, err
	}
	if opts.DryRun {
		return result, nil
	}

	executable, err := t.integrationExecutable(host)
	if err != nil {
		return nil, err
	}

	marketplaces, err := t.integrationJSONCommand(ctx, executable, hostMarketplaceListArgs(host))
	if err != nil {
		return nil, fmt.Errorf("integrate: list %s marketplaces: %w", host, err)
	}
	registered, err := checkMarketplaceState(host, marketplaces, root, scope)
	if err != nil {
		return nil, err
	}
	plugins, err := t.integrationJSONCommand(ctx, executable, hostPluginListArgs(host))
	if err != nil {
		return nil, fmt.Errorf("integrate: list %s plugins: %w", host, err)
	}
	installed, err := installedPluginIDs(host, plugins, scope)
	if err != nil {
		return nil, fmt.Errorf("integrate: parse %s plugin list: %w", host, err)
	}

	if err := t.extractIntegration(host, root); err != nil {
		return nil, err
	}
	if !registered {
		if err := t.runIntegrationCommand(ctx, executable, hostMarketplaceAddArgs(host, root, scope)); err != nil {
			return nil, fmt.Errorf("integrate: register %s marketplace: %w", host, err)
		}
	}

	for _, name := range selected {
		id := name + "@" + localIntegrationMarketplace
		args := hostPluginInstallArgs(host, id, scope, installed[id])
		if err := t.runIntegrationCommand(ctx, executable, args); err != nil {
			return nil, fmt.Errorf("integrate: install %s with %s: %w", id, host, err)
		}
	}
	return result, nil
}

func integrationPreview(host, root, scope string, selected []string) (*IntegrateResult, error) {
	srcRoot := path.Join("rendered", host)
	var targets []string
	err := fs.WalkDir(integrations.IntegrationsFS, srcRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, srcRoot), "/")
		targets = append(targets, filepath.Join(root, filepath.FromSlash(rel)))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("integrate: inspect embedded %s marketplace: %w", host, err)
	}
	sort.Strings(targets)
	commands := [][]string{
		append([]string{host}, hostMarketplaceListArgs(host)...),
		append([]string{host}, hostPluginListArgs(host)...),
		append([]string{host}, hostMarketplaceAddArgs(host, root, scope)...),
	}
	for _, name := range selected {
		commands = append(commands, append([]string{host}, hostPluginInstallArgs(host, name+"@"+localIntegrationMarketplace, scope, false)...))
	}
	return &IntegrateResult{Root: root, Paths: targets, Commands: commands}, nil
}

func integrationScope(host, requested string) (string, error) {
	scope := strings.TrimSpace(requested)
	if scope == "" {
		scope = "user"
	}
	switch host {
	case "claude":
		if scope == "user" || scope == "project" || scope == "local" {
			return scope, nil
		}
		return "", fmt.Errorf("integrate: invalid Claude scope %q; use user, project, or local", scope)
	case "codex":
		if scope != "user" {
			return "", fmt.Errorf("integrate: Codex currently supports only --scope user; use Claude for project or local plugin activation")
		}
		return scope, nil
	default:
		return "", fmt.Errorf("integrate: unknown host %q", host)
	}
}

func selectedIntegrationPlugins(host string, requested []string) ([]string, error) {
	available, err := IntegratePlugins(host)
	if err != nil {
		return nil, err
	}
	valid := make(map[string]bool, len(available))
	for _, name := range available {
		valid[name] = true
	}
	if !valid["tapper"] {
		return nil, fmt.Errorf("integrate: embedded %s marketplace is missing required plugin %q", host, "tapper")
	}
	selected := []string{"tapper"}
	seen := map[string]bool{"tapper": true}
	for _, raw := range requested {
		name := strings.TrimSpace(raw)
		if !valid[name] {
			return nil, fmt.Errorf("integrate: unknown %s plugin %q; available plugins: %s", host, name, strings.Join(available, ", "))
		}
		if !seen[name] {
			selected = append(selected, name)
			seen[name] = true
		}
	}
	return selected, nil
}

func (t *Tap) extractIntegration(host, root string) error {
	stage := root + ".tmp"
	backup := root + ".old"
	_ = t.Runtime.Remove(stage, true)
	_ = t.Runtime.Remove(backup, true)

	srcRoot := path.Join("rendered", host)
	err := fs.WalkDir(integrations.IntegrationsFS, srcRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		body, err := fs.ReadFile(integrations.IntegrationsFS, p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, srcRoot), "/")
		if err := t.Runtime.WriteFile(filepath.Join(stage, filepath.FromSlash(rel)), body, 0o644); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		_ = t.Runtime.Remove(stage, true)
		return fmt.Errorf("integrate: extract embedded %s marketplace: %w", host, err)
	}
	if err := t.validateExtractedIntegration(host, stage); err != nil {
		_ = t.Runtime.Remove(stage, true)
		return err
	}

	hadRoot := false
	if _, err := t.Runtime.Stat(root, false); err == nil {
		hadRoot = true
		if err := t.Runtime.Rename(root, backup); err != nil {
			_ = t.Runtime.Remove(stage, true)
			return fmt.Errorf("integrate: stage existing marketplace: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) && !os.IsNotExist(err) {
		_ = t.Runtime.Remove(stage, true)
		return fmt.Errorf("integrate: inspect existing marketplace: %w", err)
	}
	if err := t.Runtime.Rename(stage, root); err != nil {
		if hadRoot {
			_ = t.Runtime.Rename(backup, root)
		}
		return fmt.Errorf("integrate: activate extracted marketplace: %w", err)
	}
	if hadRoot {
		_ = t.Runtime.Remove(backup, true)
	}
	return nil
}

func (t *Tap) validateExtractedIntegration(host, root string) error {
	manifest := filepath.Join(root, ".claude-plugin", "marketplace.json")
	if host == "codex" {
		manifest = filepath.Join(root, ".agents", "plugins", "marketplace.json")
	}
	filenames := []string{manifest}
	plugins, err := IntegratePlugins(host)
	if err != nil {
		return err
	}
	for _, name := range plugins {
		filenames = append(filenames, filepath.Join(root, name, hostManifestDir(host), "plugin.json"))
	}
	for _, filename := range filenames {
		body, err := t.Runtime.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("integrate: validation missing %s: %w", filename, err)
		}
		var value any
		if err := json.Unmarshal(body, &value); err != nil {
			return fmt.Errorf("integrate: validation invalid JSON %s: %w", filename, err)
		}
	}
	return nil
}

func hostManifestDir(host string) string {
	if host == "codex" {
		return ".codex-plugin"
	}
	return ".claude-plugin"
}

func (t *Tap) integrationExecutable(host string) (string, error) {
	pathValue := t.Runtime.Env().Get("PATH")
	for _, dir := range filepath.SplitList(pathValue) {
		candidate := filepath.Join(dir, host)
		info, err := t.Runtime.Stat(candidate, true)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		hostPath, err := t.Runtime.HostPath(candidate)
		if err != nil {
			return "", fmt.Errorf("integrate: resolve %s executable: %w", host, err)
		}
		return hostPath, nil
	}
	return "", fmt.Errorf("integrate: %s CLI not found on PATH; install %s and retry", host, host)
}

func (t *Tap) integrationJSONCommand(ctx context.Context, executable string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = t.Runtime.Environ()
	if wd, err := t.Runtime.Getwd(); err == nil {
		if hostWD, hostErr := t.Runtime.HostPath(wd); hostErr == nil {
			cmd.Dir = hostWD
		}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdin = t.Runtime.Stream().In
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			_, _ = t.Runtime.Stream().Err.Write(stderr.Bytes())
		}
		return nil, err
	}
	if stderr.Len() > 0 {
		_, _ = t.Runtime.Stream().Err.Write(stderr.Bytes())
	}
	return stdout.Bytes(), nil
}

func (t *Tap) runIntegrationCommand(ctx context.Context, executable string, args []string) error {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = t.Runtime.Environ()
	if wd, err := t.Runtime.Getwd(); err == nil {
		if hostWD, hostErr := t.Runtime.HostPath(wd); hostErr == nil {
			cmd.Dir = hostWD
		}
	}
	cmd.Stdin = t.Runtime.Stream().In
	cmd.Stdout = t.Runtime.Stream().Out
	cmd.Stderr = t.Runtime.Stream().Err
	return cmd.Run()
}

func hostMarketplaceListArgs(host string) []string {
	return []string{"plugin", "marketplace", "list", "--json"}
}

func hostPluginListArgs(host string) []string {
	return []string{"plugin", "list", "--json"}
}

func hostMarketplaceAddArgs(host, root, scope string) []string {
	args := []string{"plugin", "marketplace", "add", root}
	if host == "claude" {
		args = append(args, "--scope", scope)
	}
	return args
}

func hostPluginInstallArgs(host, id, scope string, installed bool) []string {
	if host == "claude" {
		if installed {
			return []string{"plugin", "update", id, "--scope", scope}
		}
		return []string{"plugin", "install", id, "--scope", scope}
	}
	return []string{"plugin", "add", id}
}

func checkMarketplaceState(host string, body []byte, expectedRoot, scope string) (bool, error) {
	type codexEntry struct {
		Name  string `json:"name"`
		Root  string `json:"root"`
		Scope string `json:"scope"`
	}
	var entries []codexEntry
	if host == "codex" {
		var response struct {
			Marketplaces []codexEntry `json:"marketplaces"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return false, fmt.Errorf("integrate: parse codex marketplace list: %w", err)
		}
		entries = response.Marketplaces
	} else {
		var response []struct {
			Name            string `json:"name"`
			Path            string `json:"path"`
			InstallLocation string `json:"installLocation"`
			Scope           string `json:"scope"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return false, fmt.Errorf("integrate: parse claude marketplace list: %w", err)
		}
		for _, item := range response {
			root := item.Path
			if root == "" {
				root = item.InstallLocation
			}
			itemScope := item.Scope
			if itemScope == "" {
				itemScope = "user"
			}
			entries = append(entries, codexEntry{Name: item.Name, Root: root, Scope: itemScope})
		}
	}
	for _, entry := range entries {
		if entry.Name != localIntegrationMarketplace || (host == "claude" && entry.Scope != scope) {
			continue
		}
		if sameIntegrationPath(entry.Root, expectedRoot) {
			return true, nil
		}
		return false, fmt.Errorf("integrate: marketplace %q already points to %s, expected %s; refusing to replace it", localIntegrationMarketplace, entry.Root, expectedRoot)
	}
	return false, nil
}

func sameIntegrationPath(a, b string) bool {
	return filepath.Clean(strings.TrimSpace(a)) == filepath.Clean(strings.TrimSpace(b))
}

func installedPluginIDs(host string, body []byte, scope string) (map[string]bool, error) {
	out := map[string]bool{}
	if host == "codex" {
		var response struct {
			Installed []struct {
				PluginID string `json:"pluginId"`
			} `json:"installed"`
		}
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, err
		}
		for _, plugin := range response.Installed {
			out[plugin.PluginID] = true
		}
		return out, nil
	}
	var response []struct {
		ID    string `json:"id"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	for _, plugin := range response {
		pluginScope := plugin.Scope
		if pluginScope == "" {
			pluginScope = "user"
		}
		if pluginScope == scope {
			out[plugin.ID] = true
		}
	}
	return out, nil
}

func integrationHostExists(host string) bool {
	for _, adapter := range integrations.DefaultAdapters() {
		if adapter.Name() == host {
			return true
		}
	}
	return false
}

func IntegrateHosts() []string {
	adapters := integrations.DefaultAdapters()
	out := make([]string, 0, len(adapters))
	for _, adapter := range adapters {
		out = append(out, adapter.Name())
	}
	sort.Strings(out)
	return out
}

// IntegratePlugins returns the plugin names advertised by the host's embedded
// rendered marketplace. The marketplace, rather than installer code, is the
// source of truth as new optional plugins are added.
func IntegratePlugins(host string) ([]string, error) {
	return integratePluginsFromFS(integrations.IntegrationsFS, host)
}

func integratePluginsFromFS(fsys fs.FS, host string) ([]string, error) {
	manifest := path.Join("rendered", host, ".claude-plugin", "marketplace.json")
	if host == "codex" {
		manifest = path.Join("rendered", host, ".agents", "plugins", "marketplace.json")
	}
	body, err := fs.ReadFile(fsys, manifest)
	if err != nil {
		return nil, fmt.Errorf("integrate: read embedded %s marketplace: %w", host, err)
	}
	var marketplace struct {
		Plugins []struct {
			Name string `json:"name"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(body, &marketplace); err != nil {
		return nil, fmt.Errorf("integrate: parse embedded %s marketplace: %w", host, err)
	}
	seen := make(map[string]bool, len(marketplace.Plugins))
	out := make([]string, 0, len(marketplace.Plugins))
	for _, plugin := range marketplace.Plugins {
		name := strings.TrimSpace(plugin.Name)
		if name != "" && !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil, fmt.Errorf("integrate: embedded %s marketplace contains no plugins", host)
	}
	return out, nil
}
