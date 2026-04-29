package tapper

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jlrickert/tapper/pkg/integrations"
)

// OrientTierMin / OrientTierMax are the valid tier bounds for Tap.Orient.
// Tier 0 is purpose + active keg + rules summary; tier 1 adds linking and
// snapshot policy; tier 2 adds the full canonical body plus the rendered
// host artifact when a host is supplied. Inputs outside this range clamp
// to the nearest valid tier rather than erroring.
const (
	OrientTierMin = 0
	OrientTierMax = 2
)

// orientPurpose is the one-paragraph description of tapper served at
// every tier. Higher tiers layer additional canonical sections on top;
// tier 0 stays terse so agents can cheaply bootstrap.
const orientPurpose = "Tapper is a CLI and MCP server for KEG (Knowledge Exchange Graph) systems. A KEG is a numbered collection of markdown nodes with metadata, links, tags, and snapshot history. Agents operate on a KEG through the `mcp__tapper__*` tools; reading or writing node files directly bypasses indexing, locking, and snapshots."

// orientRulesSummary is the terse rules block for tier 0. Full guidance
// lives in the canonical content and surfaces at tier 2.
const orientRulesSummary = "Rules:\n" +
	"- Use the `mcp__tapper__*` tools for every KEG operation; never read or write node files directly.\n" +
	"- The target keg resolves from the working directory unless the `keg` parameter overrides it.\n" +
	"- Take a snapshot before non-trivial edits. Snapshots do not protect against `remove`; preserve content some other way before deletion.\n" +
	"- Intra-keg links use `[title](../NODEID)`; cross-keg links use `keg:ALIAS/NODEID`.\n"

// orientPathFor returns the rendered artifact path inside IntegrationsFS
// for the given host, or "", false if no registered adapter names host or
// the registered adapter has no orient artifact. The lookup walks
// integrations.DefaultAdapters() rather than consulting a parallel map,
// so adapter registration is the single source of truth for which hosts
// the orient surface can serve.
func orientPathFor(host string) (string, bool) {
	for _, a := range integrations.DefaultAdapters() {
		if a.Name() != host {
			continue
		}
		p := a.OrientPath()
		if p == "" {
			return "", false
		}
		return p, true
	}
	return "", false
}

// OrientOptions is the input to Tap.Orient. Every field is optional: a
// zero-valued call returns the tier-0 payload with the target keg
// resolved from KegTargetOptions and no host-specific content.
//
// Flight is part of the embedded KegTargetOptions rather than a
// top-level field so the CLI's root persistent --flight flag and the
// MCP tool's flight parameter flow through the same plumbing every
// other keg-target field uses.
type OrientOptions struct {
	KegTargetOptions

	// Host, if set, causes tier-2 payloads to include the rendered
	// host artifact (SKILL.md, AGENTS.md, etc.). An unknown host
	// returns an error.
	Host string

	// Tier selects payload depth in [OrientTierMin, OrientTierMax].
	// Out-of-range values clamp to the nearest valid tier.
	Tier int
}

// OrientableHosts returns the host names that have a configured orient
// surface, sorted lexicographically. Callers that enumerate (host, tier)
// pairs for resource registration consult this instead of walking the
// adapter registry themselves. The set is derived from
// integrations.DefaultAdapters() filtered on OrientPath() != "".
func OrientableHosts() []string {
	adapters := integrations.DefaultAdapters()
	out := make([]string, 0, len(adapters))
	for _, a := range adapters {
		if a.OrientPath() == "" {
			continue
		}
		out = append(out, a.Name())
	}
	sort.Strings(out)
	return out
}

// Orient returns a tapper orientation payload at the requested tier.
// See OrientTierMin / OrientTierMax for tier semantics. MCP tool,
// MCP Resources, and the eventual `tap orient` CLI all delegate here
// so every surface produces identical bytes at matching inputs.
//
// Active-keg resolution runs against the live KegService so the payload
// names the keg the next mcp__tapper__* call would actually hit, rather
// than a placeholder hint about auto-detection. Resolution failures are
// not propagated — orient is a bootstrap surface and must still describe
// tapper when no keg exists.
func (t *Tap) Orient(ctx context.Context, opts OrientOptions) (string, error) {
	activeKeg := t.resolveActiveKegLabel(ctx, opts.KegTargetOptions)
	return buildOrientPayload(opts.Host, activeKeg, opts.Keg, opts.Flight, opts.Tier)
}

// activeKegLabel is the structured outcome of orient's active-keg
// resolution. Exposing alias and backend separately lets the renderer
// format them differently (e.g. "alias (backend)", "(backend) (no
// alias)", or the "(none configured)" fallback) without re-deriving
// from a pre-formatted string.
//
// Backend deliberately omits the filesystem path or remote URL: orient
// is the description surface, not the locator. `tap info` is the
// dedicated "where is this keg" command and is allowed to surface
// paths; orient stays path-free so that tier-0 output is portable
// across machines and safe to paste into transcripts.
type activeKegLabel struct {
	// Alias is the configured alias for the resolved keg, or "" when
	// the resolution succeeded but no alias matches the target (e.g. an
	// ad-hoc cwd keg that is not registered in tap config).
	Alias string
	// Backend is a path-free identifier describing the storage backend
	// (e.g. "file-backed", "knut:@alice/blog", "in-memory"). Empty when
	// no keg resolved.
	Backend string
	// Unresolved is true when KegService could not find any keg for the
	// current selection (no kegs configured, no matching alias, etc.).
	// Renderers surface a directed hint instead of a path.
	Unresolved bool
}

// resolveActiveKegLabel runs the same resolution KegService.Resolve does
// for any other tap command and returns a structured label suitable for
// display. It never propagates errors: when resolution fails for an
// implicit (cwd-driven) selection, Unresolved=true so the renderer
// surfaces a directed "(none configured)" hint. When resolution fails
// for an explicit `opts.Keg`, the alias is echoed back without a target
// — orient is a description surface and must not lie about what the
// user typed, even if the alias is not registered yet (the per-alias
// manifest section is independent of resolvability).
func (t *Tap) resolveActiveKegLabel(ctx context.Context, opts KegTargetOptions) activeKegLabel {
	if t == nil || t.KegService == nil {
		return activeKegLabel{Unresolved: true}
	}
	k, err := t.resolveKeg(ctx, opts)
	if err != nil || k == nil || k.Target == nil {
		if alias := strings.TrimSpace(opts.Keg); alias != "" {
			return activeKegLabel{Alias: alias}
		}
		return activeKegLabel{Unresolved: true}
	}

	label := activeKegLabel{Backend: KegBackendLabel(k.Target)}
	if cfg, _ := t.KegService.ConfigService.Config(true); cfg != nil {
		label.Alias = cfg.LookupAliasForTarget(t.Runtime, k.Target.String())
	}
	return label
}

// buildOrientPayload assembles the orient bytes at tier for the given
// host, active-keg label, manifest-keg, and flight. Exposed to other
// packages via Tap.Orient.
//
// active is the resolved active-keg label used for the tier-0 "Active
// keg" line. manifestKeg is the explicit alias passed to the API
// (opts.Keg) and gates the tier-1 entity-kind manifest placeholder; the
// two are different because the manifest section is per-alias, while
// the active-keg line names whichever keg would actually be operated on.
//
// tier is clamped to [OrientTierMin, OrientTierMax]. An unknown host
// returns an error immediately; a known-but-unrendered host (for
// example codex before its adapter ships) surfaces the underlying
// fs.ReadFile error.
func buildOrientPayload(host string, active activeKegLabel, manifestKeg, flight string, tier int) (string, error) {
	if tier < OrientTierMin {
		tier = OrientTierMin
	}
	if tier > OrientTierMax {
		tier = OrientTierMax
	}
	var hostPath string
	if host != "" {
		p, ok := orientPathFor(host)
		if !ok {
			return "", fmt.Errorf("orient: unknown host %q", host)
		}
		hostPath = p
	}

	var b strings.Builder

	// Tier 0: always emitted.
	b.WriteString("# tapper orient (tier ")
	b.WriteString(strconv.Itoa(tier))
	b.WriteString(")\n\n")
	b.WriteString(orientPurpose)
	b.WriteString("\n\n")
	b.WriteString("Active keg: ")
	b.WriteString(formatActiveKegLine(active))
	b.WriteString("\n\n")
	b.WriteString(orientRulesSummary)

	if tier < 1 {
		return b.String(), nil
	}

	// Tier 1: linking + snapshot policy + manifest placeholders.
	b.WriteString("\n")
	if err := appendCanonical(&b, "linking.md"); err != nil {
		return "", err
	}
	b.WriteString("\n")
	if err := appendCanonical(&b, "snapshot-policy.md"); err != nil {
		return "", err
	}
	if manifestKeg != "" {
		b.WriteString("\n## Entity-kind manifest for `")
		b.WriteString(manifestKeg)
		b.WriteString("`\n\n")
		b.WriteString("(Per-keg manifest is not yet populated; the orient surface reserves the field for a future release.)\n")
	}
	if flight != "" {
		b.WriteString("\n## Flight `")
		b.WriteString(flight)
		b.WriteString("`\n\n")
		b.WriteString("(Flight-scoped manifest is not yet populated; the orient surface reserves the field for a future release.)\n")
	}

	if tier < 2 {
		return b.String(), nil
	}

	// Tier 2: full canonical body + host-rendered bytes.
	b.WriteString("\n")
	if err := appendCanonical(&b, "agent-orient.md"); err != nil {
		return "", err
	}
	b.WriteString("\n")
	if err := appendCanonical(&b, "tool-inventory.md"); err != nil {
		return "", err
	}
	b.WriteString("\n")
	if err := appendCanonical(&b, "troubleshooting.md"); err != nil {
		return "", err
	}

	if host != "" {
		hostBytes, err := fs.ReadFile(integrations.IntegrationsFS, hostPath)
		if err != nil {
			return "", fmt.Errorf("orient: host %s bytes at %s: %w", host, hostPath, err)
		}
		b.WriteString("\n## Host: ")
		b.WriteString(host)
		b.WriteString("\n\n")
		b.Write(hostBytes)
		if n := len(hostBytes); n == 0 || hostBytes[n-1] != '\n' {
			b.WriteByte('\n')
		}
	}

	return b.String(), nil
}

// formatActiveKegLine renders the right-hand side of the "Active keg:"
// line for tier 0. Three shapes:
//
//	Unresolved:         "(none configured; run `tap init` to register one)"
//	Alias + backend:    "`alias` (file-backed)"
//	Backend only:       "(file-backed; no alias)"
//
// The directed hint on the unresolved branch matches the surface area
// users land on when they bootstrap tapper for the first time, so it
// names the next concrete step instead of saying nothing. Backend is a
// path-free identifier (see KegBackendLabel) so orient output never
// reveals filesystem location — `tap info` is the dedicated locator.
func formatActiveKegLine(active activeKegLabel) string {
	if active.Unresolved {
		return "(none configured; run `tap init` to register one)"
	}
	if active.Alias != "" {
		if active.Backend == "" {
			return "`" + active.Alias + "`"
		}
		return "`" + active.Alias + "` (" + active.Backend + ")"
	}
	if active.Backend != "" {
		return "(" + active.Backend + "; no alias)"
	}
	return "(none configured; run `tap init` to register one)"
}

// appendCanonical reads integrations/content/<name> from the embedded
// FS and appends it to b. A trailing newline is added when the source
// file does not end with one so later sections start on a fresh line.
func appendCanonical(b *strings.Builder, name string) error {
	raw, err := fs.ReadFile(integrations.IntegrationsFS, "content/"+name)
	if err != nil {
		return fmt.Errorf("orient: canonical %s: %w", name, err)
	}
	b.Write(raw)
	if n := len(raw); n == 0 || raw[n-1] != '\n' {
		b.WriteByte('\n')
	}
	return nil
}
