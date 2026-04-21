package tapper

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"

	"github.com/jlrickert/tapper/pkg/integrations"
)

// IntegrateOptions is the input to Tap.Integrate.
type IntegrateOptions struct {
	KegTargetOptions

	// Host selects which rendered tree to install. Must be a name
	// that appears in OrientableHosts / hostRenderedPath.
	Host string

	// DryRun, when true, causes Integrate to return the target paths
	// it would write without actually writing any files.
	DryRun bool

	// Target overrides the default install directory for Host. When
	// empty, the per-host default under the user's home directory is
	// used (see defaultIntegrateTarget).
	Target string
}

// Integrate copies the embedded rendered tree for the specified host
// into the host's on-disk install location. It returns the absolute
// target paths, one per file copied. When DryRun is true, no writes
// happen and the returned paths describe what would be written.
//
// Every copy flows through the Runtime so the command honors
// sandboxed tests and the project's Runtime Abstraction Rule. Parent
// directories are created on demand by rt.WriteFile.
func (t *Tap) Integrate(ctx context.Context, opts IntegrateOptions) ([]string, error) {
	_ = ctx // reserved for cancellation once writes become long-running

	if opts.Host == "" {
		return nil, fmt.Errorf("integrate: host is required")
	}
	if _, ok := hostRenderedPath[opts.Host]; !ok {
		return nil, fmt.Errorf("integrate: unknown host %q", opts.Host)
	}

	target := opts.Target
	if target == "" {
		var err error
		target, err = t.defaultIntegrateTarget(opts.Host)
		if err != nil {
			return nil, err
		}
	}

	// Source root inside the embedded FS. Everything below this prefix
	// gets copied to target with the prefix stripped.
	srcRoot := path.Join("rendered", opts.Host)

	var targets []string
	err := fs.WalkDir(integrations.IntegrationsFS, srcRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(filepath.FromSlash(srcRoot), filepath.FromSlash(p))
		if err != nil {
			return fmt.Errorf("integrate: relpath %s: %w", p, err)
		}
		destFS := filepath.ToSlash(rel)
		dest := filepath.Join(target, filepath.FromSlash(destFS))
		targets = append(targets, dest)
		if opts.DryRun {
			return nil
		}
		data, err := fs.ReadFile(integrations.IntegrationsFS, p)
		if err != nil {
			return fmt.Errorf("integrate: read embedded %s: %w", p, err)
		}
		if err := t.Runtime.WriteFile(dest, data, 0o644); err != nil {
			return fmt.Errorf("integrate: write %s: %w", dest, err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(targets)
	return targets, nil
}

// defaultIntegrateTarget returns the per-host default install path
// rooted at the user's home directory. Hosts that need a more bespoke
// path (for example Claude Code's plugin directory naming) encode it
// here so callers do not have to compute it themselves.
func (t *Tap) defaultIntegrateTarget(host string) (string, error) {
	home, err := t.Runtime.GetHome()
	if err != nil {
		return "", fmt.Errorf("integrate: resolve home: %w", err)
	}
	switch host {
	case "claude":
		// Claude Code loads local plugins from ~/.claude/plugins/<name>/
		// per the Claude Code plugin convention. The plugin tree under
		// integrations/rendered/claude/ already contains the
		// .claude-plugin/ manifest and skills/ subtree, so copying the
		// whole tree verbatim produces a working installation.
		return filepath.Join(home, ".claude", "plugins", "tapper"), nil
	case "codex":
		// Codex reads ~/.codex/AGENTS.md and ~/.codex/prompts/ as the
		// project-level agent context, plus ~/.codex/config.toml for
		// MCP server registrations. The rendered tree lands at the
		// top of ~/.codex/; config-snippet.toml sits alongside as a
		// merge template the user (or `tap integrate`) reconciles with
		// their existing config.toml.
		return filepath.Join(home, ".codex"), nil
	default:
		return "", fmt.Errorf("integrate: no default target for host %q", host)
	}
}

// IntegrateHosts returns the sorted list of hosts that Integrate can
// install. It intersects the adapter registry with hostRenderedPath
// so newly registered adapters without an orient/install surface are
// excluded. Callers that build CLI completion lists consult this.
func IntegrateHosts() []string {
	orientable := make(map[string]bool)
	for _, h := range OrientableHosts() {
		orientable[h] = true
	}
	seen := make(map[string]bool)
	var out []string
	for _, a := range integrations.DefaultAdapters() {
		name := a.Name()
		if !orientable[name] || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

