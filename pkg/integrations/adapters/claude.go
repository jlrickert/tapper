package adapters

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/integrations"
)

type ClaudeAdapter struct{}

func (ClaudeAdapter) Name() string { return "claude" }

func (a ClaudeAdapter) Render(rt *toolkit.Runtime, content fs.FS, dst integrations.DestWriter) error {
	version := pluginVersion(rt)
	marketplace, err := renderClaudeMarketplace()
	if err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), ".claude-plugin", "marketplace.json"), marketplace); err != nil {
		return err
	}

	baselineManifest, err := renderClaudeManifest("tapper", version, "MCP-first Tapper KEG access, flight orientation, and safety guidance.", nil)
	if err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), "tapper", ".claude-plugin", "plugin.json"), baselineManifest); err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), "tapper", ".mcp.json"), renderClaudeMCP()); err != nil {
		return err
	}
	body, err := fs.ReadFile(content, "claude/hooks/hooks.json")
	if err != nil {
		return fmt.Errorf("claude: hooks.json: %w", err)
	}
	if err := dst.Write(path.Join(a.Name(), "tapper", "hooks", "hooks.json"), body); err != nil {
		return err
	}
	baseline, err := renderSkill(content, "tapper", "Orient to Tapper flights and operate on KEGs through MCP-first safety rules.", baselineOrder)
	if err != nil {
		return fmt.Errorf("claude: baseline skill: %w", err)
	}
	if err := dst.Write(path.Join(a.Name(), "tapper", "skills", "tapper", "SKILL.md"), baseline); err != nil {
		return err
	}
	devManifest, err := renderClaudeManifest("tapper-dev", version, "Optional Plan to Code to Review to Commit workflow for Tapper-enabled development.", []string{"tapper"})
	if err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), "tapper-dev", ".claude-plugin", "plugin.json"), devManifest); err != nil {
		return err
	}
	workflow, err := fs.ReadFile(content, "developer/workflow.md")
	if err != nil {
		return fmt.Errorf("claude: developer workflow: %w", err)
	}
	devSkill := addSkillFrontmatter("tapper-dev", "Optional Plan, Code, Review, and Commit workflow. Requires the baseline tapper plugin.", workflow)
	return dst.Write(path.Join(a.Name(), "tapper-dev", "skills", "tapper-dev", "SKILL.md"), devSkill)
}

type claudeManifest struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Version      string   `json:"version"`
	Author       author   `json:"author"`
	Homepage     string   `json:"homepage"`
	Dependencies []string `json:"dependencies,omitempty"`
}

type author struct {
	Name string `json:"name"`
}

func renderClaudeManifest(name, version, description string, dependencies []string) ([]byte, error) {
	return marshalIndented(claudeManifest{
		Name: name, Description: description, Version: version,
		Author: author{Name: pluginAuthor}, Homepage: pluginHomepage,
		Dependencies: dependencies,
	})
}

func renderClaudeMarketplace() ([]byte, error) {
	type owner struct {
		Name string `json:"name"`
	}
	type entry struct {
		Name        string `json:"name"`
		Source      string `json:"source"`
		Description string `json:"description"`
	}
	v := struct {
		Name        string  `json:"name"`
		Owner       owner   `json:"owner"`
		Description string  `json:"description"`
		Plugins     []entry `json:"plugins"`
	}{
		Name:        marketplaceName,
		Owner:       owner{Name: pluginAuthor},
		Description: "Local plugins embedded in the Tapper CLI.",
		Plugins: []entry{
			{Name: "tapper", Source: "./tapper", Description: "MCP-first Tapper KEG access and safety."},
			{Name: "tapper-dev", Source: "./tapper-dev", Description: "Optional Plan, Code, Review, and Commit workflow."},
		},
	}
	return marshalIndented(v)
}

// renderClaudeMCP writes the Claude Code MCP registration.
//
// Deliberately no env allowlist. Claude Code passes its own environment to the
// stdio servers it spawns, so tap resolves the same config, auth store, and
// data directories as the shell that launched it. Codex does not, which is why
// renderCodexMCP carries an explicit env_vars list — the asymmetry is a
// difference between the two hosts, not an oversight here. Adding a list to
// this one would only create something to go stale.
func renderClaudeMCP() []byte {
	return []byte(`{
  "mcpServers": {
    "tapper": {
      "command": "tap",
      "args": ["mcp"]
    }
  }
}
`)
}

func stripLeadingH1(b []byte) []byte {
	if !bytes.HasPrefix(b, []byte("# ")) {
		return b
	}
	nl := bytes.IndexByte(b, '\n')
	if nl < 0 {
		return nil
	}
	rest := b[nl+1:]
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	return rest
}

func stripTrailingNewlines(b []byte) []byte { return bytes.TrimRight(b, "\n") }

func init() { integrations.Register(ClaudeAdapter{}) }
