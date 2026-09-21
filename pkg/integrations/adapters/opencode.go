package adapters

import (
	"fmt"
	"io/fs"
	"path"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/integrations"
)

// tapperManifestDir holds the manifests that only Tapper reads.
//
// Claude and Codex each define a plugin format, so their manifests double as
// the host's own metadata. opencode has no plugin format to target: it reads
// an `mcp` block out of opencode.json and discovers skills by directory. The
// rendered tree still needs a plugin index, because IntegratePlugins reads the
// marketplace rather than trusting installer code to enumerate plugins. These
// files serve that purpose alone and the installer never copies them to the
// host.
const tapperManifestDir = ".tapper"

type OpenCodeAdapter struct{}

func (OpenCodeAdapter) Name() string { return "opencode" }

func (a OpenCodeAdapter) Render(_ *toolkit.Runtime, content fs.FS, dst integrations.DestWriter) error {
	marketplace, err := renderOpenCodeMarketplace()
	if err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), tapperManifestDir, "marketplace.json"), marketplace); err != nil {
		return err
	}

	baselineManifest, err := renderOpenCodeManifest("tapper", pluginVersionPlaceholder,
		"MCP-first Tapper KEG access, flight orientation, and safety guidance.", nil)
	if err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), "tapper", tapperManifestDir, "plugin.json"), baselineManifest); err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), "tapper", "opencode.json"), renderOpenCodeMCP()); err != nil {
		return err
	}
	baseline, err := renderSkill(content, "tapper", "Orient to Tapper flights and operate on KEGs through MCP-first safety rules.", baselineOrder)
	if err != nil {
		return fmt.Errorf("opencode: baseline skill: %w", err)
	}
	if err := dst.Write(path.Join(a.Name(), "tapper", "skills", "tapper", "SKILL.md"), baseline); err != nil {
		return err
	}

	guardManifest, err := renderOpenCodeManifest(guardPluginName, pluginVersionPlaceholder, guardPluginDescription, []string{"tapper"})
	if err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), guardPluginName, tapperManifestDir, "plugin.json"), guardManifest); err != nil {
		return err
	}
	// opencode auto-loads every file in its plugin/ directory, so the guard
	// ships as one module rather than as a hook table its host cannot read.
	guardPlugin, err := fs.ReadFile(content, "guard/opencode-plugin.ts")
	if err != nil {
		return fmt.Errorf("opencode: guard plugin: %w", err)
	}
	if err := dst.Write(path.Join(a.Name(), guardPluginName, "plugin", guardPluginName+".ts"), guardPlugin); err != nil {
		return err
	}

	devManifest, err := renderOpenCodeManifest("tapper-dev", pluginVersionPlaceholder,
		"Optional Plan to Code to Review to Commit workflow for Tapper-enabled development.", []string{"tapper"})
	if err != nil {
		return err
	}
	if err := dst.Write(path.Join(a.Name(), "tapper-dev", tapperManifestDir, "plugin.json"), devManifest); err != nil {
		return err
	}
	workflow, err := fs.ReadFile(content, "developer/workflow.md")
	if err != nil {
		return fmt.Errorf("opencode: developer workflow: %w", err)
	}
	devSkill := addSkillFrontmatter("tapper-dev", "Optional Plan, Code, Review, and Commit workflow. Requires the baseline tapper plugin.", workflow)
	return dst.Write(path.Join(a.Name(), "tapper-dev", "skills", "tapper-dev", "SKILL.md"), devSkill)
}

type opencodeManifest struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Version      string   `json:"version"`
	Author       author   `json:"author"`
	Homepage     string   `json:"homepage"`
	Dependencies []string `json:"dependencies,omitempty"`
}

func renderOpenCodeManifest(name, pluginVersionPlaceholder, description string, dependencies []string) ([]byte, error) {
	return marshalIndented(opencodeManifest{
		Name: name, Description: description, Version: pluginVersionPlaceholder,
		Author: author{Name: pluginAuthor}, Homepage: pluginHomepage,
		Dependencies: dependencies,
	})
}

func renderOpenCodeMarketplace() ([]byte, error) {
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
			{Name: guardPluginName, Source: "./" + guardPluginName, Description: guardMarketplaceDescription},
			{Name: "tapper-dev", Source: "./tapper-dev", Description: "Optional Plan, Code, Review, and Commit workflow."},
		},
	}
	return marshalIndented(v)
}

// renderOpenCodeMCP writes the fragment merged into the host's opencode.json.
//
// This is a fragment, not a whole config: the installer reads the user's
// existing opencode.json and replaces only the mcp.tapper key, so every other
// setting they have survives a refresh.
//
// Deliberately no "environment" allowlist, for the same reason renderClaudeMCP
// has none: opencode passes its own environment to the local MCP servers it
// spawns, so tap resolves the same config, auth store, and data directories as
// the shell that launched it. Codex is the exception there, not the rule.
func renderOpenCodeMCP() []byte {
	return []byte(`{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "tapper": {
      "type": "local",
      "command": ["tap", "mcp"],
      "enabled": true
    }
  }
}
`)
}

func init() { integrations.Register(OpenCodeAdapter{}) }
