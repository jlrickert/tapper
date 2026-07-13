package adapters

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/integrations"
)

func TestClaudeAdapter_RendersMarketplaceDependencyAndSelfContainedPlugins(t *testing.T) {
	mem := integrations.NewMemWriter()
	if err := (ClaudeAdapter{}).Render(testRuntime(t), testContentFS(t), mem); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"claude/.claude-plugin/marketplace.json",
		"claude/tapper/.claude-plugin/plugin.json",
		"claude/tapper/.mcp.json",
		"claude/tapper/hooks/hooks.json",
		"claude/tapper/hooks/block-tap-cli.py",
		"claude/tapper/skills/tapper/SKILL.md",
		"claude/tapper/skills/tapper-flight-switch/SKILL.md",
		"claude/tapper/skills/tapper-mcp-reset/SKILL.md",
		"claude/tapper-dev/.claude-plugin/plugin.json",
		"claude/tapper-dev/skills/tapper-dev/SKILL.md",
	}
	for _, name := range want {
		if _, ok := mem.Files()[name]; !ok {
			t.Errorf("missing %s; got %v", name, mem.Paths())
		}
	}

	var manifest struct {
		Dependencies []string `json:"dependencies"`
	}
	if err := json.Unmarshal(mem.Files()["claude/tapper-dev/.claude-plugin/plugin.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Dependencies) != 1 || manifest.Dependencies[0] != "tapper" {
		t.Fatalf("dependencies = %v", manifest.Dependencies)
	}
	if strings.Contains(string(mem.Files()["claude/tapper-dev/.claude-plugin/plugin.json"]), "mcpServers") {
		t.Fatal("developer plugin must not register MCP")
	}
}

func TestClaudeAdapter_RendersHostRecoveryGuidance(t *testing.T) {
	mem := integrations.NewMemWriter()
	if err := (ClaudeAdapter{}).Render(testRuntime(t), testContentFS(t), mem); err != nil {
		t.Fatal(err)
	}
	reset := string(mem.Files()["claude/tapper/skills/tapper-mcp-reset/SKILL.md"])
	if !strings.Contains(reset, "/reload-plugins") || !strings.Contains(reset, "new Claude session") {
		t.Fatalf("Claude reset guidance is incomplete: %s", reset)
	}
}

func TestClaudeAdapter_RendersHumanOnlyFlightSwitch(t *testing.T) {
	mem := integrations.NewMemWriter()
	if err := (ClaudeAdapter{}).Render(testRuntime(t), testContentFS(t), mem); err != nil {
		t.Fatal(err)
	}
	switcher := string(mem.Files()["claude/tapper/skills/tapper-flight-switch/SKILL.md"])
	for _, want := range []string{"disable-model-invocation: true", `argument-hint: "@namespace/+slug"`, "$ARGUMENTS"} {
		if !strings.Contains(switcher, want) {
			t.Errorf("flight switch command missing %q", want)
		}
	}

	var hooks struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type   string         `json:"type"`
				Server string         `json:"server"`
				Tool   string         `json:"tool"`
				Input  map[string]any `json:"input"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(mem.Files()["claude/tapper/hooks/hooks.json"], &hooks); err != nil {
		t.Fatal(err)
	}
	expansions := hooks.Hooks["UserPromptExpansion"]
	if len(expansions) != 1 || len(expansions[0].Hooks) != 1 {
		t.Fatalf("flight switch expansion hook = %+v", expansions)
	}
	hook := expansions[0].Hooks[0]
	if hook.Type != "mcp_tool" || hook.Server != "tapper" || hook.Tool != "flight_switch_control" || hook.Input["ref"] != "${command_args}" {
		t.Fatalf("flight switch MCP hook = %+v", hook)
	}
}

func TestClaudeAdapter_BaselineExcludesDeveloperLifecycle(t *testing.T) {
	mem := integrations.NewMemWriter()
	if err := (ClaudeAdapter{}).Render(testRuntime(t), testContentFS(t), mem); err != nil {
		t.Fatal(err)
	}
	baseline := string(mem.Files()["claude/tapper/skills/tapper/SKILL.md"])
	dev := string(mem.Files()["claude/tapper-dev/skills/tapper-dev/SKILL.md"])
	for _, heading := range []string{"## Plan", "## Code", "## Review", "## Commit"} {
		if strings.Contains(baseline, heading) {
			t.Errorf("baseline leaked %s", heading)
		}
		if !strings.Contains(dev, heading) {
			t.Errorf("developer workflow missing %s", heading)
		}
	}
}

func TestStripLeadingH1(t *testing.T) {
	if got := string(stripLeadingH1([]byte("# Heading\n\nbody\n"))); got != "body\n" {
		t.Fatalf("got %q", got)
	}
}
