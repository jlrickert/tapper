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

// hostRenderedPath maps a registered adapter name to the file inside the
// embedded rendered tree whose bytes are appended to a tier-2 payload
// when the caller specifies that host. The mapping is small and
// deliberate; if a host exposes multiple artifacts (Codex has AGENTS.md
// plus prompts), the value points at the primary orient surface.
//
// The map lives in pkg/tapper because orient is a tapper-level concept
// shared by every surface (MCP tool, MCP Resources, eventual CLI). A
// richer Adapter interface in pkg/integrations will replace the map in
// a later phase; for now, host additions happen in lockstep with
// adapter additions in a single package.
var hostRenderedPath = map[string]string{
	"claude": "rendered/claude/skills/tapper/SKILL.md",
	"codex":  "rendered/codex/AGENTS.md",
}

// OrientOptions is the input to Tap.Orient. Every field is optional: a
// zero-valued call returns the tier-0 payload with the target keg
// resolved from KegTargetOptions and no host-specific content.
type OrientOptions struct {
	KegTargetOptions

	// Host, if set, causes tier-2 payloads to include the rendered
	// host artifact (SKILL.md, AGENTS.md, etc.). An unknown host
	// returns an error.
	Host string

	// Flight reserves a parameter for flight-scoped manifest payloads.
	// Currently emits a placeholder at tier 1 and above.
	Flight string

	// Tier selects payload depth in [OrientTierMin, OrientTierMax].
	// Out-of-range values clamp to the nearest valid tier.
	Tier int
}

// OrientableHosts returns the host names that have a configured orient
// surface, sorted lexicographically. Callers that enumerate (host, tier)
// pairs for resource registration consult this instead of reaching into
// the package-private host map.
func OrientableHosts() []string {
	out := make([]string, 0, len(hostRenderedPath))
	for h := range hostRenderedPath {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// Orient returns a tapper orientation payload at the requested tier.
// See OrientTierMin / OrientTierMax for tier semantics. MCP tool,
// MCP Resources, and the eventual `tap orient` CLI all delegate here
// so every surface produces identical bytes at matching inputs.
func (t *Tap) Orient(ctx context.Context, opts OrientOptions) (string, error) {
	_ = ctx // reserved for future keg-manifest lookups that will need a runtime-aware call site
	return buildOrientPayload(opts.Host, opts.Keg, opts.Flight, opts.Tier)
}

// buildOrientPayload assembles the orient bytes at tier for the given
// host, keg, and flight. Exposed to other packages via Tap.Orient.
//
// tier is clamped to [OrientTierMin, OrientTierMax]. An unknown host
// returns an error immediately; a known-but-unrendered host (for
// example codex before its adapter ships) surfaces the underlying
// fs.ReadFile error.
func buildOrientPayload(host, keg, flight string, tier int) (string, error) {
	if tier < OrientTierMin {
		tier = OrientTierMin
	}
	if tier > OrientTierMax {
		tier = OrientTierMax
	}
	if host != "" {
		if _, ok := hostRenderedPath[host]; !ok {
			return "", fmt.Errorf("orient: unknown host %q", host)
		}
	}

	var b strings.Builder

	// Tier 0: always emitted.
	b.WriteString("# tapper orient (tier ")
	b.WriteString(strconv.Itoa(tier))
	b.WriteString(")\n\n")
	b.WriteString(orientPurpose)
	b.WriteString("\n\n")
	b.WriteString("Active keg: ")
	if keg != "" {
		b.WriteString("`")
		b.WriteString(keg)
		b.WriteString("`")
	} else {
		b.WriteString("(auto-detect from working directory)")
	}
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
	if keg != "" {
		b.WriteString("\n## Entity-kind manifest for `")
		b.WriteString(keg)
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
		path := hostRenderedPath[host]
		hostBytes, err := fs.ReadFile(integrations.IntegrationsFS, path)
		if err != nil {
			return "", fmt.Errorf("orient: host %s bytes at %s: %w", host, path, err)
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
