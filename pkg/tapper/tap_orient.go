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
