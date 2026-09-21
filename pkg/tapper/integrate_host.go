package tapper

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jlrickert/tapper/pkg/integrations"
)

// integrationHost is the per-host install contract.
//
// The split with pkg/integrations is deliberate: a render adapter decides what
// ships inside the binary, an integrationHost decides where it lands on the
// user's machine. They are separate because the two are not the same shape.
// Claude and Codex both take a plugin marketplace handed to their own CLI, but
// a host whose install is a config file it owns rather than a marketplace fits
// here too. Without this seam every one of those differences becomes another
// `if host == ...` in Tap.Integrate, which is what this replaces.
type integrationHost interface {
	// Name returns the host's CLI name, which is also its directory under the
	// embedded rendered root and the value accepted by `tap integrate HOST`.
	Name() string

	// Scopes returns the accepted --scope values, most-default first. An empty
	// --scope resolves to the first entry.
	Scopes() []string

	// ScopeError returns the rejection for an unsupported scope. It exists so a
	// host can explain *why* a scope it does not accept is missing rather than
	// listing the ones it does.
	ScopeError(scope string) error

	// RequiresHookSupport reports whether installing the given selected
	// plugins needs a tap on PATH that understands `tap hook`. It takes the
	// selection because hooks are per-plugin: an install that skips every
	// hook-carrying plugin has nothing for the check to protect.
	RequiresHookSupport(plugins []string) bool

	// MarketplacePath returns the plugin index inside rendered/<host>/, as a
	// slash-separated relative path. It is the source of truth for which
	// plugins the host ships.
	MarketplacePath() string

	// PluginManifestPath returns one plugin's manifest inside
	// rendered/<host>/, as a slash-separated relative path. Extraction
	// validates that every one of these parses as JSON before activating.
	PluginManifestPath(plugin string) string

	// Plan resolves the complete install without performing it. It must not
	// write anything or start a host process: `tap integrate --dry-run`
	// returns Plan's result directly.
	Plan(t *Tap, p integratePlan) (*IntegrateResult, error)

	// Apply performs the install. The embedded tree has already been extracted
	// to p.Root when it runs.
	Apply(ctx context.Context, t *Tap, p integratePlan) error
}

// baselineIntegrationPlugin is installed by every `tap integrate` run.
// guardIntegrationPlugin carries the PreToolUse guard and is installed by
// default wherever the host's marketplace advertises it, unless --no-safety
// opts out.
const (
	baselineIntegrationPlugin = "tapper"
	guardIntegrationPlugin    = "tapper-guard"
)

// selectionIncludesGuard reports whether the guard plugin is part of one
// install. Every host needs the answer for the same reason — the guard is the
// piece that shells out to `tap hook` — so the scan lives here rather than in
// each host.
func selectionIncludesGuard(plugins []string) bool {
	for _, name := range plugins {
		if name == guardIntegrationPlugin {
			return true
		}
	}
	return false
}

// integratePlan is one resolved install request handed to an integrationHost.
type integratePlan struct {
	// Root is the extraction root under the user data directory.
	Root string
	// Scope is the validated scope, never empty.
	Scope string
	// Plugins are the selected plugin names, baseline "tapper" first.
	Plugins []string
}

// integrationHostRegistry returns every installable host. It is a function
// rather than a package variable so a test can neither mutate it nor observe
// another test's mutation.
func integrationHostRegistry() []integrationHost {
	return []integrationHost{
		claudeIntegrationHost(),
		codexIntegrationHost(),
	}
}

func lookupIntegrationHost(name string) (integrationHost, error) {
	for _, host := range integrationHostRegistry() {
		if host.Name() == name {
			return host, nil
		}
	}
	return nil, fmt.Errorf("integrate: unknown host %q", name)
}

// resolveIntegrationScope validates a requested scope against a host, supplying
// the host's default when the request is empty.
func resolveIntegrationScope(host integrationHost, requested string) (string, error) {
	scope := strings.TrimSpace(requested)
	accepted := host.Scopes()
	if scope == "" {
		return accepted[0], nil
	}
	for _, candidate := range accepted {
		if scope == candidate {
			return scope, nil
		}
	}
	return "", host.ScopeError(scope)
}

// IntegrateHosts returns the hosts `tap integrate` can install for, sorted. It
// backs the positional argument's shell completion.
func IntegrateHosts() []string {
	hosts := integrationHostRegistry()
	out := make([]string, 0, len(hosts))
	for _, host := range hosts {
		out = append(out, host.Name())
	}
	sort.Strings(out)
	return out
}

// IntegrateScopes returns the --scope values a host accepts, most-default
// first. An unknown host returns nil, which shell completion renders as no
// suggestions rather than an error.
func IntegrateScopes(host string) []string {
	resolved, err := lookupIntegrationHost(strings.TrimSpace(host))
	if err != nil {
		return nil
	}
	return resolved.Scopes()
}

// IntegratePlugins returns the plugin names advertised by the host's embedded
// rendered marketplace. The marketplace, rather than installer code, is the
// source of truth as new optional plugins are added.
func IntegratePlugins(host string) ([]string, error) {
	return integratePluginsFromFS(integrations.IntegrationsFS, host)
}

func integratePluginsFromFS(fsys fs.FS, host string) ([]string, error) {
	resolved, err := lookupIntegrationHost(strings.TrimSpace(host))
	if err != nil {
		return nil, err
	}
	manifest := path.Join("rendered", resolved.Name(), resolved.MarketplacePath())
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

// extractionTargets lists the files the embedded tree will occupy under root,
// sorted. Both the dry-run preview and the real extraction walk the same FS, so
// the reported paths cannot drift from the written ones.
func extractionTargets(host, root string) ([]string, error) {
	srcRoot := path.Join("rendered", host)
	var targets []string
	err := fs.WalkDir(integrations.IntegrationsFS, srcRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		targets = append(targets, filepath.Join(root, filepath.FromSlash(relativeToRoot(p, srcRoot))))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("integrate: inspect embedded %s marketplace: %w", host, err)
	}
	sort.Strings(targets)
	return targets, nil
}

func relativeToRoot(p, srcRoot string) string {
	return strings.TrimPrefix(strings.TrimPrefix(p, srcRoot), "/")
}
