package mcp

import (
	"context"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/integrations"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// Orient tier bounds. Tier 0 is purpose + active keg + rules summary;
// tier 1 adds linking and snapshot policy; tier 2 adds the full canonical
// body plus the host-rendered bytes when host is set. Tier values outside
// this range are clamped to the nearest valid tier.
const (
	OrientTierMin = 0
	OrientTierMax = 2
)

// orientPurpose is the one-paragraph description of tapper served at every
// tier. Higher tiers layer additional canonical sections on top; tier 0
// is deliberately terse so agents can cheaply bootstrap.
const orientPurpose = "Tapper is a CLI and MCP server for KEG (Knowledge Exchange Graph) systems. A KEG is a numbered collection of markdown nodes with metadata, links, tags, and snapshot history. Agents operate on a KEG through the `mcp__tapper__*` tools; reading or writing node files directly bypasses indexing, locking, and snapshots."

// orientRulesSummary is the terse rules block for tier 0. Full guidance
// lives in the canonical content and surfaces at tier 2.
const orientRulesSummary = "Rules:\n" +
	"- Use the `mcp__tapper__*` tools for every KEG operation; never read or write node files directly.\n" +
	"- The target keg resolves from the working directory unless the `keg` parameter overrides it.\n" +
	"- Take a snapshot before non-trivial edits. Snapshots do not protect against `remove`; preserve content some other way before deletion.\n" +
	"- Intra-keg links use `[title](../NODEID)`; cross-keg links use `keg:ALIAS/NODEID`.\n"

// hostRenderedPath maps a registered adapter name to the file inside the
// embedded rendered tree whose bytes are appended to a tier-2 payload when
// the caller specifies that host. The mapping is small and deliberate;
// if a host exposes multiple artifacts (Codex has AGENTS.md plus prompts),
// the value points at the primary orient surface for that host.
var hostRenderedPath = map[string]string{
	"claude": "rendered/claude/skills/tapper/SKILL.md",
	"codex":  "rendered/codex/AGENTS.md",
}

// orientInput is the parameter surface of mcp__tapper__orient. Every
// field is optional: a bare call returns the tier-0 payload with an
// auto-detected keg and no host-specific content.
type orientInput struct {
	Host   string `json:"host,omitempty"   jsonschema:"host identifier for host-specific payload (e.g. 'claude' or 'codex')"`
	Keg    string `json:"keg,omitempty"    jsonschema:"keg alias; reserved for per-keg manifest payloads"`
	Flight string `json:"flight,omitempty" jsonschema:"flight identifier; reserved for flight-scoped manifest payloads"`
	Tier   int    `json:"tier,omitempty"   jsonschema:"payload depth: 0 (purpose + active keg + rules summary), 1 (adds linking + snapshot), 2 (adds full canonical body and host-rendered bytes)"`
}

// registerOrientTools wires the orient surface onto srv. Called from
// NewServer alongside the other register*Tools helpers.
func registerOrientTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerOrient(srv, tap, defaults)
}

func registerOrient(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "orient",
		Description: "Return a tapper orientation payload at the requested tier. Tier 0 is bounded (purpose + active keg + rules summary). Tier 1 adds linking conventions and snapshot policy. Tier 2 adds the full canonical body; when host is set, the rendered host-specific bytes are appended.",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in orientInput) (*sdkmcp.CallToolResult, any, error) {
		keg := resolveKegTarget(in.Keg, defaults).Keg
		payload, err := buildOrientPayload(in.Host, keg, in.Flight, in.Tier)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(payload), nil, nil
	})
}

// buildOrientPayload assembles the orient bytes at tier for the given
// host, keg, and flight. The Resource handlers call this with the same
// inputs so resources/read returns bytes byte-equal to the tool's output
// at matching parameters.
//
// tier is clamped to [OrientTierMin, OrientTierMax]. An unknown host
// returns an error immediately; a known-but-unrendered host (for example
// codex before its adapter ships) surfaces the underlying fs.ReadFile
// error.
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

// appendCanonical reads integrations/content/<name> from the embedded FS
// and appends it to b. A trailing newline is added when the canonical
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
