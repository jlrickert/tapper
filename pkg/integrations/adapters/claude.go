// Package adapters hosts the host-specific integration renderers. Each
// adapter is registered with the parent integrations package via init() and
// owns a single output directory under the rendered root.
package adapters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"

	"github.com/jlrickert/tapper/pkg/integrations"
)

// Claude plugin metadata. Values mirror today's plugin.json byte-for-byte;
// the em-dash in Description is U+2014 (UTF-8 0xE2 0x80 0x94), not a
// double-hyphen fallback.
const (
	claudePluginName        = "tapper"
	claudePluginDescription = "Tapper KEG CLI integration — registers the `tap mcp` server and ships the `/tapper` skill for MCP-first KEG workflows. Requires the `tap` binary on PATH."
	claudePluginVersion     = "0.18.1"
	claudePluginAuthorName  = "Jared Rickert"
	claudePluginHomepage    = "https://github.com/jlrickert/tapper"
)

// claudeSKILLFrontmatter is the YAML frontmatter prepended to the concat
// SKILL.md. The description string ends with "MCP-first workflow." to match
// today's file; the em-dash is the same U+2014 character.
const claudeSKILLFrontmatter = "---\n" +
	"name: tapper\n" +
	"description: Interact with tapper KEGs via the mcp__tapper__* MCP interface — search, navigate, create, and maintain notes. MCP-first workflow.\n" +
	"---\n\n"

// claudeSKILLOrder is the canonical SKILL.md concat order. agent-orient.md
// keeps its "# tapper" H1 because today's SKILL.md body opens with it; the
// other four files have their H1 stripped at concat time so the rendered
// file has a single top-level heading.
var claudeSKILLOrder = []string{
	"agent-orient.md",
	"tool-inventory.md",
	"snapshot-policy.md",
	"linking.md",
	"troubleshooting.md",
}

// ClaudeAdapter renders the Claude Code plugin tree:
//   - claude/.claude-plugin/plugin.json
//   - claude/.claude-plugin/.mcp.json
//   - claude/skills/tapper/SKILL.md
type ClaudeAdapter struct{}

// Name returns the adapter's subdirectory under the rendered root.
func (ClaudeAdapter) Name() string { return "claude" }

// Render emits the three Claude plugin files into dst.
func (a ClaudeAdapter) Render(content fs.FS, dst integrations.DestWriter) error {
	// 1. plugin.json — byte-parity with today's file via the struct encoder.
	plugin, err := renderClaudePluginJSON()
	if err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), ".claude-plugin", "plugin.json"), plugin); err != nil {
		return err
	}

	// 2. .mcp.json — hand-formatted to preserve today's inline "args" array.
	// json.Encoder with SetIndent always expands slice elements onto their
	// own lines; today's file keeps ["mcp"] inline, so the adapter owns the
	// template to guarantee byte-parity.
	mcp := renderClaudeMCPJSON()
	if err := dst.Write(path.Join(a.Name(), ".claude-plugin", ".mcp.json"), mcp); err != nil {
		return err
	}

	// 3. SKILL.md — frontmatter injected by the adapter, then canonical
	// bodies concatenated in claudeSKILLOrder. agent-orient keeps its H1;
	// the other four files drop theirs.
	skill, err := renderClaudeSKILL(content)
	if err != nil {
		return err
	}
	return dst.Write(path.Join(a.Name(), "skills", "tapper", "SKILL.md"), skill)
}

// claudePluginAuthor mirrors the nested author object in plugin.json.
type claudePluginAuthor struct {
	Name string `json:"name"`
}

// claudePluginJSON mirrors plugin.json's top-level object. Field order is
// locked by the struct declaration order: name, description, version,
// author, homepage — matching today's file.
type claudePluginJSON struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Version     string             `json:"version"`
	Author      claudePluginAuthor `json:"author"`
	Homepage    string             `json:"homepage"`
}

// renderClaudePluginJSON builds plugin.json using encoding/json with 2-space
// indent. The encoder appends a trailing newline for us.
func renderClaudePluginJSON() ([]byte, error) {
	v := claudePluginJSON{
		Name:        claudePluginName,
		Description: claudePluginDescription,
		Version:     claudePluginVersion,
		Author:      claudePluginAuthor{Name: claudePluginAuthorName},
		Homepage:    claudePluginHomepage,
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("claude: encode plugin.json: %w", err)
	}
	return buf.Bytes(), nil
}

// renderClaudeMCPJSON produces .mcp.json with the inline "args" array that
// today's file uses. encoding/json cannot emit this layout, so the adapter
// hand-templates it. The output ends with a single trailing newline.
func renderClaudeMCPJSON() []byte {
	// If multi-server support lands later, switch to a slice of struct-keyed
	// pairs and loop here. Today the template is single-server by design.
	const tmpl = `{
  "mcpServers": {
    "tapper": {
      "command": "tap",
      "args": ["mcp"]
    }
  }
}
`
	return []byte(tmpl)
}

// renderClaudeSKILL concatenates the canonical markdown bodies in the order
// claudeSKILLOrder, prepending the frontmatter. agent-orient.md keeps its H1;
// the other four files drop their first line (their H1) so the rendered
// SKILL.md has exactly one "# tapper" at the top.
func renderClaudeSKILL(content fs.FS) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(claudeSKILLFrontmatter)
	for i, name := range claudeSKILLOrder {
		body, err := fs.ReadFile(content, name)
		if err != nil {
			return nil, fmt.Errorf("claude: skill %s: %w", name, err)
		}
		section := stripTrailingNewlines(body)
		if i > 0 {
			section = stripLeadingH1(section)
		}
		if i > 0 {
			out.WriteString("\n\n")
		}
		out.Write(section)
	}
	// Trailing newline to match today's SKILL.md, which ends with one \n.
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// stripLeadingH1 removes the first line of b if it begins with "# ". A blank
// line immediately after the H1 is also removed so the remaining body starts
// at its first meaningful paragraph. This lets standalone canonical files
// keep their H1 for readability while still concatenating cleanly.
func stripLeadingH1(b []byte) []byte {
	if !bytes.HasPrefix(b, []byte("# ")) {
		return b
	}
	nl := bytes.IndexByte(b, '\n')
	if nl < 0 {
		return nil
	}
	rest := b[nl+1:]
	// Also consume exactly one blank line following the H1, if present.
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	return rest
}

// stripTrailingNewlines trims trailing \n bytes so callers can rejoin with a
// controlled separator. bytes.TrimRight does the same thing but is explicit
// about the character set it trims.
func stripTrailingNewlines(b []byte) []byte {
	return bytes.TrimRight(b, "\n")
}

// Register the Claude adapter with the parent integrations package. init()
// mirrors pkg/mcp's register*Tools pattern: the top-level package does not
// need to know any adapter by type.
func init() {
	integrations.Register(ClaudeAdapter{})
}
