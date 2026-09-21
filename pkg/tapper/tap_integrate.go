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
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/integrations"
)

// IntegrateOptions selects the host, native install scope, dry-run behavior,
// and optional plugins for native plugin installation. The baseline tapper
// plugin is always installed, and so is the tapper-guard safety plugin unless
// NoSafety is set.
type IntegrateOptions struct {
	KegTargetOptions
	Host   string
	DryRun bool
	// Plugins are additional embedded plugins to install, such as
	// tapper-dev. Request order is preserved and duplicates are ignored.
	Plugins []string
	// NoSafety skips the tapper-guard plugin for hosts that ship it. It does
	// not uninstall a guard the host already has; removing or disabling that
	// one is a host-side action.
	NoSafety bool
	Scope    string
}

// IntegrateResult describes the extracted marketplace and what the install
// does with it.
//
// Commands and Steps are the two shapes an install takes and a host uses one or
// the other: Commands for a host driven through its own CLI, Steps for a host
// installed by writing its configuration. A dry run reports whichever is set.
type IntegrateResult struct {
	Root     string
	Paths    []string
	Commands [][]string
	Steps    []string
}

// Integrate atomically refreshes the embedded marketplace for one host and
// installs the requested plugins the way that host expects. Dry-run returns the
// complete plan without side effects.
func (t *Tap) Integrate(ctx context.Context, opts IntegrateOptions) (*IntegrateResult, error) {
	name := strings.TrimSpace(opts.Host)
	if name == "" {
		return nil, fmt.Errorf("integrate: host is required")
	}
	host, err := lookupIntegrationHost(name)
	if err != nil {
		return nil, err
	}
	scope, err := resolveIntegrationScope(host, opts.Scope)
	if err != nil {
		return nil, err
	}
	selected, err := selectedIntegrationPlugins(host.Name(), opts.Plugins, opts.NoSafety)
	if err != nil {
		return nil, err
	}

	dataDir, err := toolkit.UserDataPath(t.Runtime.Env())
	if err != nil {
		return nil, fmt.Errorf("integrate: resolve user data directory: %w", err)
	}
	plan := integratePlan{
		Root:    filepath.Join(dataDir, "tapper", "integrations", host.Name()),
		Scope:   scope,
		Plugins: selected,
	}
	result, err := host.Plan(t, plan)
	if err != nil {
		return nil, err
	}
	if opts.DryRun {
		return result, nil
	}

	if host.RequiresHookSupport(plan.Plugins) {
		if err := t.verifyIntegrationHookSupport(ctx); err != nil {
			return nil, err
		}
	}
	if err := host.Apply(ctx, t, plan); err != nil {
		return nil, err
	}
	return result, nil
}

// verifyIntegrationHookSupport prevents a refresh from replacing a legacy,
// self-contained plugin with one whose Go-backed hooks cannot run. The check
// deliberately resolves tap through PATH because that is how hosts invoke the
// commands stored in hooks.json.
func (t *Tap) verifyIntegrationHookSupport(ctx context.Context) error {
	executable, err := t.integrationExecutable("tap")
	if err != nil {
		return fmt.Errorf("integrate: a current tap executable with `tap hook` support is required on PATH; install or upgrade tap and retry: %w", err)
	}
	cmd := exec.CommandContext(ctx, executable, "hook", "--help")
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
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			detail = ": " + detail
		}
		return fmt.Errorf("integrate: tap on PATH does not support `tap hook`; install or upgrade tap and retry%s", detail)
	}
	return nil
}

// selectedIntegrationPlugins resolves the plugin list for one install. The
// baseline plugin always comes first, the guard follows by default on every
// host whose embedded marketplace advertises it, and the caller's extras keep
// their request order. A host whose marketplace advertises no guard makes
// noSafety a no-op rather than an error.
func selectedIntegrationPlugins(host string, requested []string, noSafety bool) ([]string, error) {
	available, err := IntegratePlugins(host)
	if err != nil {
		return nil, err
	}
	valid := make(map[string]bool, len(available))
	for _, name := range available {
		valid[name] = true
	}
	if !valid[baselineIntegrationPlugin] {
		return nil, fmt.Errorf("integrate: embedded %s marketplace is missing required plugin %q", host, baselineIntegrationPlugin)
	}
	selected := []string{baselineIntegrationPlugin}
	seen := map[string]bool{baselineIntegrationPlugin: true}
	if valid[guardIntegrationPlugin] && !noSafety {
		selected = append(selected, guardIntegrationPlugin)
		seen[guardIntegrationPlugin] = true
	}
	for _, raw := range requested {
		name := strings.TrimSpace(raw)
		if !valid[name] {
			return nil, fmt.Errorf("integrate: unknown %s plugin %q; available plugins: %s", host, name, strings.Join(available, ", "))
		}
		if name == guardIntegrationPlugin && noSafety {
			return nil, fmt.Errorf("integrate: --no-safety conflicts with --plugin %s", guardIntegrationPlugin)
		}
		if !seen[name] {
			selected = append(selected, name)
			seen[name] = true
		}
	}
	return selected, nil
}

func (t *Tap) extractIntegration(host integrationHost, root string, plugins []string) error {
	stage := root + ".tmp"
	backup := root + ".old"
	_ = t.Runtime.Remove(stage, true)
	_ = t.Runtime.Remove(backup, true)

	srcRoot := path.Join("rendered", host.Name())
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
		rel := relativeToRoot(p, srcRoot)
		if err := t.Runtime.WriteFile(filepath.Join(stage, filepath.FromSlash(rel)), body, 0o644); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		_ = t.Runtime.Remove(stage, true)
		return fmt.Errorf("integrate: extract embedded %s marketplace: %w", host.Name(), err)
	}
	if err := t.stampPluginVersions(host, stage); err != nil {
		_ = t.Runtime.Remove(stage, true)
		return err
	}
	if err := t.validateExtractedIntegration(host, stage, plugins); err != nil {
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
	} else if !isNotExist(err) {
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

// stampPluginVersions rewrites the version in every extracted plugin manifest
// to the version of the binary doing the install.
//
// The rendered tree carries a placeholder, because a version committed to the
// repo is stale the moment anyone commits past a tag — and Claude Code uses
// this field as its update gate, so a plugin that changed while claiming the
// last release may never refresh. The binary knows what it is; the repo does
// not.
//
// It stamps every plugin the marketplace advertises rather than only the
// selected ones, so a plugin installed later by hand through the host's own
// CLI reports the same version as one tap installed.
//
// The manifest round-trips through a map, so keys come back out in
// alphabetical order. These are generated install artifacts, and the
// alternative is a regex over a JSON line.
func (t *Tap) stampPluginVersions(host integrationHost, root string) error {
	plugins, err := IntegratePlugins(host.Name())
	if err != nil {
		return err
	}
	for _, name := range plugins {
		filename := filepath.Join(root, filepath.FromSlash(host.PluginManifestPath(name)))
		body, err := t.Runtime.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("integrate: read manifest %s: %w", filename, err)
		}
		var manifest map[string]json.RawMessage
		if err := json.Unmarshal(body, &manifest); err != nil {
			return fmt.Errorf("integrate: parse manifest %s: %w", filename, err)
		}
		if _, ok := manifest["version"]; !ok {
			continue
		}
		version, err := json.Marshal(t.Version)
		if err != nil {
			return fmt.Errorf("integrate: encode version for %s: %w", filename, err)
		}
		manifest["version"] = version
		stamped, err := json.Marshal(manifest)
		if err != nil {
			return fmt.Errorf("integrate: encode manifest %s: %w", filename, err)
		}
		indented, err := jsonIndent(stamped)
		if err != nil {
			return fmt.Errorf("integrate: format manifest %s: %w", filename, err)
		}
		indented = append(indented, '\n')
		if err := t.Runtime.WriteFile(filename, indented, 0o644); err != nil {
			return fmt.Errorf("integrate: write manifest %s: %w", filename, err)
		}
	}
	return nil
}

// validateExtractedIntegration parses the manifests the host will read before
// the staged tree replaces the live one, so a corrupt render fails while the
// previous install is still intact.
func (t *Tap) validateExtractedIntegration(host integrationHost, root string, plugins []string) error {
	filenames := []string{filepath.Join(root, filepath.FromSlash(host.MarketplacePath()))}
	for _, name := range plugins {
		filenames = append(filenames, filepath.Join(root, filepath.FromSlash(host.PluginManifestPath(name))))
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

// isNotExist covers both the fs sentinel and the os predicate, because a
// Runtime backed by a sandbox and one backed by the real filesystem do not
// always report a missing path the same way.
func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err)
}

// jsonIndent reformats compact JSON with the 2-space, HTML-unescaped style the
// rendered fragments use.
func jsonIndent(body []byte) ([]byte, error) {
	var out bytes.Buffer
	if err := json.Indent(&out, body, "", "  "); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
