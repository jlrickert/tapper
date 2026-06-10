package adapters

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"

	"github.com/jlrickert/cli-toolkit/toolkit"

	"github.com/jlrickert/tapper/pkg/integrations"
)

// codexAGENTSPreamble is the Codex-specific opening of AGENTS.md. It
// precedes the canonical body and establishes the single "# tapper" H1
// for the file; all subsequent canonical sections have their H1 stripped
// at concat time so the rendered file has exactly one top-level heading.
const codexAGENTSPreamble = "# tapper\n\n" +
	"This file configures the Codex CLI for the **tapper** KEG toolset. When these instructions are active, the agent operates on Knowledge Exchange Graphs through tapper's MCP server (`tap mcp`); direct edits to node storage files are unsupported and bypass the index, locking, and snapshot machinery.\n\n" +
	"Register the MCP server by merging `config-snippet.toml` into `~/.codex/config.toml`. Saved prompts under `prompts/` surface as Codex slash commands; consult them for common orientation and maintenance flows.\n\n"

// codexAGENTSOrder is the canonical concat order for AGENTS.md. It
// matches the Claude SKILL.md order so agents migrating between hosts
// see the same structural progression: purpose and rules, tool
// inventory, snapshot policy, linking, troubleshooting.
var codexAGENTSOrder = []string{
	"agent-orient.md",
	"tool-inventory.md",
	"snapshot-policy.md",
	"linking.md",
	"troubleshooting.md",
}

// codexConfigSnippet is copy-pasted into the user's `~/.codex/config.toml`
// to register the tapper MCP server. Kept minimal so the snippet composes
// cleanly with other servers the user may already have configured.
const codexConfigSnippet = `# Append this snippet to ~/.codex/config.toml to register the tapper MCP
# server. If an [mcp_servers] section already exists in your config, merge
# the [mcp_servers.tapper] entry into it rather than duplicating the
# header.

[mcp_servers.tapper]
command = "tap"
args = ["mcp"]
`

// codexPromptOrient walks the agent through a fresh-session orientation
// flow. Emitted as a Codex slash command so users can ask for a keg
// overview without retyping the tool sequence.
const codexPromptOrient = `Orient to the current tapper keg:

1. Call ` + "`mcp__tapper__info`" + ` for the tapper version and current configuration.
2. Call ` + "`mcp__tapper__keg_info`" + ` for the resolved target keg, its path, and node count.
3. Call ` + "`mcp__tapper__tags`" + ` to list the keg's tag inventory.
4. Call ` + "`mcp__tapper__list`" + ` with ` + "`sort: \"updated\"`" + ` and ` + "`limit: 20`" + ` to see recent activity.

Summarize what this keg is about and what work has been in flight recently.
`

// codexPromptSearch guides the agent to the right tapper search tool
// based on the nature of the user's query (content regex vs. tag/
// attribute filter) and emphasizes the id_only trick for large result
// sets.
const codexPromptSearch = `Find nodes in the tapper keg that match a user-provided query.

- For a regex over node content, use ` + "`mcp__tapper__grep`" + `.
- For filters by tag, attribute, or stat field, use ` + "`mcp__tapper__tags`" + ` or ` + "`mcp__tapper__list`" + ` with a boolean expression (for example ` + "`tapper and .created>2026-01-01`" + `).
- Pass ` + "`id_only: true`" + ` when the result set is large so token usage stays bounded, then read specific nodes with ` + "`mcp__tapper__cat`" + `.

Report the node IDs that match and, for each, a one-line summary of how it relates to the query.
`

// codexPromptSnapshot codifies the snapshot-before-edit discipline. The
// prompt mirrors the guidance in canonical snapshot-policy.md but
// compresses it to a procedure the agent can follow without the whole
// AGENTS.md in context.
const codexPromptSnapshot = `Before a non-trivial edit to a tapper node, capture a snapshot:

1. Call ` + "`mcp__tapper__node_snapshot`" + ` with the target node ID.
2. Make the edit with ` + "`mcp__tapper__edit`" + ` or ` + "`mcp__tapper__meta`" + `.
3. Verify the result with ` + "`mcp__tapper__cat`" + `.

To inspect old content, list prior revisions with ` + "`mcp__tapper__node_history`" + ` and read one with ` + "`mcp__tapper__node_snapshot_view`" + `. To recover the current node, call ` + "`mcp__tapper__node_restore`" + `.

Snapshots do NOT protect against ` + "`mcp__tapper__remove`" + `. Before any destructive operation, read the content with ` + "`mcp__tapper__cat`" + ` and keep it in the working context or copy it into another node first.
`

// codexPromptCrossKeg covers the cross-keg read workflow. The tapper
// MCP tools accept an optional `keg` parameter that overrides the
// server-default target; this prompt walks the agent through reading
// a node from a non-default keg without restarting the server. Flagged
// by a Phase 4 reviewer as a common workflow the original prompt set
// missed.
const codexPromptCrossKeg = `Read or list nodes from a tapper keg other than the active default.

Every tapper MCP tool accepts an optional ` + "`keg`" + ` parameter that overrides the server's current target. Use it instead of restarting the server or switching directories.

1. Call ` + "`mcp__tapper__hub_list`" + ` to see which kegs the configured hubs expose (qualified as ` + "`@namespace/keg`" + `).
2. Call ` + "`mcp__tapper__keg_info`" + ` with ` + "`keg: \"REF\"`" + ` to confirm the resolved path and node count of the target keg.
3. Call ` + "`mcp__tapper__cat`" + ` with ` + "`keg: \"REF\"`" + ` and the node IDs to read. Pass ` + "`content_only: true`" + ` or ` + "`meta_only: true`" + ` to bound the payload.
4. For searches, call ` + "`mcp__tapper__list`" + ` or ` + "`mcp__tapper__grep`" + ` with the same ` + "`keg`" + ` override.

Report the node IDs read, the keg they came from, and a one-line summary of each.
`

// codexPrompts is the manifest of Codex saved prompts the adapter
// emits. Adding a prompt is one struct entry plus the body string above.
var codexPrompts = []struct {
	name string
	body string
}{
	{"tapper-orient.md", codexPromptOrient},
	{"tapper-search.md", codexPromptSearch},
	{"tapper-snapshot.md", codexPromptSnapshot},
	{"tapper-cross-keg.md", codexPromptCrossKeg},
}

// CodexAdapter renders the Codex install tree:
//   - codex/AGENTS.md                  (preamble + canonical body, single H1)
//   - codex/prompts/tapper-*.md        (saved prompt shortcuts)
//   - codex/config-snippet.toml        (MCP server registration)
type CodexAdapter struct{}

// Name returns the adapter's subdirectory under the rendered root.
func (CodexAdapter) Name() string { return "codex" }

// OrientPath returns the rendered AGENTS.md path inside IntegrationsFS.
// Tier-2 orient calls with host="codex" append these bytes to the
// canonical body; Codex users see the same preamble and concat as
// the install-tree AGENTS.md.
func (CodexAdapter) OrientPath() string {
	return "rendered/codex/AGENTS.md"
}

// Render emits the Codex install tree under dst. The three output
// groups (AGENTS.md, prompts, config snippet) are independent; a
// failure on any one aborts the render. rt is accepted for interface
// consistency — the Codex adapter has no release-time configuration
// today — and is not consulted.
func (a CodexAdapter) Render(rt *toolkit.Runtime, content fs.FS, dst integrations.DestWriter) error {
	_ = rt
	agents, err := renderCodexAGENTS(content)
	if err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), "AGENTS.md"), agents); err != nil {
		return err
	}

	for _, p := range codexPrompts {
		relPath := path.Join(a.Name(), "prompts", p.name)
		if err := dst.Write(relPath, []byte(p.body)); err != nil {
			return err
		}
	}

	return dst.Write(path.Join(a.Name(), "config-snippet.toml"), []byte(codexConfigSnippet))
}

// renderCodexAGENTS concatenates the codex preamble and the canonical
// bodies in codexAGENTSOrder. Every canonical file has its leading H1
// stripped so the rendered AGENTS.md has exactly the one "# tapper"
// declared by the preamble.
func renderCodexAGENTS(content fs.FS) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(codexAGENTSPreamble)
	for i, name := range codexAGENTSOrder {
		body, err := fs.ReadFile(content, name)
		if err != nil {
			return nil, fmt.Errorf("codex: section %s: %w", name, err)
		}
		section := stripTrailingNewlines(body)
		section = stripLeadingH1(section)
		if i > 0 {
			out.WriteString("\n\n")
		}
		out.Write(section)
	}
	// Terminal newline for POSIX-friendly file endings.
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// Register the Codex adapter with the parent integrations package.
// init() mirrors the ClaudeAdapter registration in claude.go; the
// top-level package stays unaware of the adapter types.
func init() {
	integrations.Register(CodexAdapter{})
}
