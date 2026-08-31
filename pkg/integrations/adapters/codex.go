package adapters

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/integrations"
)

const (
	pluginVersionEnv     = "TAPPER_PLUGIN_VERSION"
	pluginVersionDefault = "0.0.0-dev"
	pluginAuthor         = "Jared Rickert"
	pluginHomepage       = "https://github.com/jlrickert/tapper"
	marketplaceName      = "tapper-local"
)

var baselineOrder = []string{
	"agent-orient.md",
	"tool-inventory.md",
	"snapshot-policy.md",
	"linking.md",
	"secret-handling.md",
	"troubleshooting.md",
}

type CodexAdapter struct{}

func (CodexAdapter) Name() string { return "codex" }

func (a CodexAdapter) Render(rt *toolkit.Runtime, content fs.FS, dst integrations.DestWriter) error {
	version := pluginVersion(rt)
	marketplace, err := renderCodexMarketplace()
	if err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), ".agents", "plugins", "marketplace.json"), marketplace); err != nil {
		return err
	}

	baselineManifest, err := renderCodexManifest(codexManifest{
		Name:        "tapper",
		Version:     version,
		Description: "MCP-first Tapper KEG access, flight orientation, and safety guidance.",
		Skills:      "./skills/",
		MCPServers:  "./.mcp.json",
		Interface: codexInterface{
			DisplayName:      "Tapper",
			ShortDescription: "MCP-first KEG access and safety",
			LongDescription:  "Orient through the active Tapper flight and work with covered KEGs through MCP-first safety rules.",
			Capabilities:     []string{"Interactive", "Read", "Write"},
			DefaultPrompt: []string{
				"Orient to the active Tapper flight.",
				"Search Tapper KEGs for a topic.",
				"Snapshot and edit a Tapper node.",
			},
		},
	})
	if err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), "tapper", ".codex-plugin", "plugin.json"), baselineManifest); err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), "tapper", ".mcp.json"), renderCodexMCP()); err != nil {
		return err
	}
	body, err := fs.ReadFile(content, "codex/hooks/hooks.json")
	if err != nil {
		return fmt.Errorf("codex: hooks.json: %w", err)
	}
	if err := dst.Write(path.Join(a.Name(), "tapper", "hooks", "hooks.json"), body); err != nil {
		return err
	}
	baseline, err := renderSkill(content, "tapper", "Orient to Tapper flights and operate on KEGs through MCP-first safety rules.", baselineOrder)
	if err != nil {
		return fmt.Errorf("codex: baseline skill: %w", err)
	}
	if err := dst.Write(path.Join(a.Name(), "tapper", "skills", "tapper", "SKILL.md"), baseline); err != nil {
		return err
	}

	devManifest, err := renderCodexManifest(codexManifest{
		Name:        "tapper-dev",
		Version:     version,
		Description: "Optional Plan to Code to Review to Commit workflow for Tapper-enabled development.",
		Skills:      "./skills/",
		Interface: codexInterface{
			DisplayName:      "Tapper Dev",
			ShortDescription: "Plan, code, review, and commit",
			LongDescription:  "An optional four-stage developer workflow that requires the baseline Tapper plugin and follows live flight instructions.",
			Capabilities:     []string{"Interactive"},
			DefaultPrompt: []string{
				"Plan and implement a change with the Tapper developer workflow.",
			},
		},
	})
	if err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), "tapper-dev", ".codex-plugin", "plugin.json"), devManifest); err != nil {
		return err
	}
	workflow, err := fs.ReadFile(content, "developer/workflow.md")
	if err != nil {
		return fmt.Errorf("codex: developer workflow: %w", err)
	}
	devSkill := addSkillFrontmatter("tapper-dev", "Optional Plan, Code, Review, and Commit workflow. Requires the baseline tapper plugin.", workflow)
	return dst.Write(path.Join(a.Name(), "tapper-dev", "skills", "tapper-dev", "SKILL.md"), devSkill)
}

type codexManifest struct {
	Name        string         `json:"name"`
	Version     string         `json:"version"`
	Description string         `json:"description"`
	Author      codexAuthor    `json:"author"`
	Homepage    string         `json:"homepage"`
	Repository  string         `json:"repository"`
	License     string         `json:"license"`
	Keywords    []string       `json:"keywords"`
	Skills      string         `json:"skills"`
	MCPServers  string         `json:"mcpServers,omitempty"`
	Interface   codexInterface `json:"interface"`
}

type codexAuthor struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type codexInterface struct {
	DisplayName      string   `json:"displayName"`
	ShortDescription string   `json:"shortDescription"`
	LongDescription  string   `json:"longDescription"`
	DeveloperName    string   `json:"developerName"`
	Category         string   `json:"category"`
	WebsiteURL       string   `json:"websiteURL"`
	Capabilities     []string `json:"capabilities"`
	DefaultPrompt    []string `json:"defaultPrompt"`
}

func renderCodexManifest(v codexManifest) ([]byte, error) {
	v.Author = codexAuthor{Name: pluginAuthor, URL: pluginHomepage}
	v.Homepage = pluginHomepage
	v.Repository = pluginHomepage
	v.License = "MIT"
	v.Keywords = []string{"keg", "knowledge", "mcp", "tapper"}
	v.Interface.DeveloperName = pluginAuthor
	v.Interface.Category = "Developer Tools"
	v.Interface.WebsiteURL = pluginHomepage
	return marshalIndented(v)
}

func renderCodexMarketplace() ([]byte, error) {
	type source struct {
		Source string `json:"source"`
		Path   string `json:"path"`
	}
	type policy struct {
		Installation   string `json:"installation"`
		Authentication string `json:"authentication"`
	}
	type entry struct {
		Name     string `json:"name"`
		Source   source `json:"source"`
		Policy   policy `json:"policy"`
		Category string `json:"category"`
	}
	v := struct {
		Name      string `json:"name"`
		Interface struct {
			DisplayName string `json:"displayName"`
		} `json:"interface"`
		Plugins []entry `json:"plugins"`
	}{Name: marketplaceName}
	v.Interface.DisplayName = "Tapper Local"
	for _, name := range []string{"tapper", "tapper-dev"} {
		v.Plugins = append(v.Plugins, entry{
			Name:     name,
			Source:   source{Source: "local", Path: "./" + name},
			Policy:   policy{Installation: "AVAILABLE", Authentication: "ON_INSTALL"},
			Category: "Developer Tools",
		})
	}
	return marshalIndented(v)
}

// renderCodexMCP writes the Codex MCP registration.
//
// Codex hands stdio MCP servers an allowlisted environment rather than its own,
// so every variable tap needs must be named here. Claude Code forwards
// everything and needs no equivalent — see renderClaudeMCP.
//
// HOME is not redundant with the XDG roots. tap falls back to $HOME whenever a
// root is unset, and reaches for it directly when expanding "~" in config
// values and when resolving the default keg root. Forwarding the roots without
// HOME therefore works only where every path in play happens to be XDG-rooted;
// elsewhere tap mcp fails to authenticate while the same tap in the shell
// succeeds, which is precisely how this surfaced in a dev container.
//
// TAP_AGENT carries `tap launch --agent` selection. Without it the harness has
// the agent but the MCP server it spawns does not, so the session silently
// resolves the configured flight instead of the agent's. TAP_FLIGHT must be
// forwarded too: the launcher sets it to pin the connection root when a flight
// is configured, and a human may also export it directly. It is deliberately
// unset for a no-flight launch, which is how the spawned `tap mcp` knows to
// resolve identity authority instead of treating itself as launcher-bound.
func renderCodexMCP() []byte {
	return []byte(`{
  "mcpServers": {
    "tapper": {
      "command": "tap",
      "args": ["mcp"],
      "env_vars": [
        "HOME",
        "TAP_AGENT",
        "TAP_FLIGHT",
        "XDG_CONFIG_HOME",
        "XDG_DATA_HOME",
        "XDG_STATE_HOME",
        "XDG_CACHE_HOME"
      ]
    }
  }
}
`)
}

func pluginVersion(rt *toolkit.Runtime) string {
	if rt == nil {
		return pluginVersionDefault
	}
	v := strings.TrimSpace(rt.Env().Get(pluginVersionEnv))
	if v == "" {
		return pluginVersionDefault
	}
	v = strings.TrimPrefix(v, "v")
	return v
}

func renderSkill(content fs.FS, name, description string, order []string) ([]byte, error) {
	var body bytes.Buffer
	for i, filename := range order {
		section, err := fs.ReadFile(content, filename)
		if err != nil {
			return nil, err
		}
		section = stripTrailingNewlines(section)
		if i > 0 {
			section = stripLeadingH1(section)
			body.WriteString("\n\n")
		}
		body.Write(section)
	}
	body.WriteByte('\n')
	return addSkillFrontmatter(name, description, body.Bytes()), nil
}

func addSkillFrontmatter(name, description string, body []byte) []byte {
	var out bytes.Buffer
	fmt.Fprintf(&out, "---\nname: %s\ndescription: %s\n---\n\n", name, description)
	out.Write(body)
	if len(body) == 0 || body[len(body)-1] != '\n' {
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func marshalIndented(v any) ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func init() { integrations.Register(CodexAdapter{}) }
