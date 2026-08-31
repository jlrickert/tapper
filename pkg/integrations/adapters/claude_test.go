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
		"claude/tapper/skills/tapper/SKILL.md",
		"claude/tapper-dev/.claude-plugin/plugin.json",
		"claude/tapper-dev/skills/tapper-dev/SKILL.md",
	}
	if len(mem.Paths()) != len(want) {
		t.Fatalf("rendered files = %v, want exactly %v", mem.Paths(), want)
	}
	for _, name := range want {
		if _, ok := mem.Files()[name]; !ok {
			t.Errorf("missing %s; got %v", name, mem.Paths())
		}
	}
	for _, name := range mem.Paths() {
		if strings.HasSuffix(name, ".py") {
			t.Errorf("rendered plugin must not package Python hook %s", name)
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

func TestClaudeAdapter_RendersGoBackedPreToolUseGuard(t *testing.T) {
	mem := integrations.NewMemWriter()
	if err := (ClaudeAdapter{}).Render(testRuntime(t), testContentFS(t), mem); err != nil {
		t.Fatal(err)
	}
	var hooks struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(mem.Files()["claude/tapper/hooks/hooks.json"], &hooks); err != nil {
		t.Fatal(err)
	}
	pre := hooks.Hooks["PreToolUse"]
	if len(pre) != 1 || pre[0].Matcher != "^(Bash|Write|Edit|MultiEdit|NotebookEdit|Shell|exec_command|apply_patch|write_file|edit_file|delete_file|move_file|rename_file)$" || len(pre[0].Hooks) != 1 {
		t.Fatalf("Claude PreToolUse hook = %+v", pre)
	}
	hook := pre[0].Hooks[0]
	if hook.Type != "command" || hook.Command != "tap hook pre-tool-use" || hook.Timeout != 5 {
		t.Fatalf("Claude command hook = %+v", hook)
	}
	if strings.Contains(string(mem.Files()["claude/tapper/hooks/hooks.json"]), "PLUGIN_ROOT") {
		t.Fatal("Claude hooks must not reference packaged plugin-root scripts")
	}
}

func TestClaudeAdapter_BaselineExcludesDeveloperLifecycle(t *testing.T) {
	mem := integrations.NewMemWriter()
	if err := (ClaudeAdapter{}).Render(testRuntime(t), testContentFS(t), mem); err != nil {
		t.Fatal(err)
	}
	baseline := string(mem.Files()["claude/tapper/skills/tapper/SKILL.md"])
	dev := string(mem.Files()["claude/tapper-dev/skills/tapper-dev/SKILL.md"])
	for _, want := range []string{
		"`[title](../NODEID)`",
		"`[title](keg:ALIAS/NODEID)`",
		"`[title](keg:@NAMESPACE/ALIAS/NODEID)`",
		"A bare `keg:` reference in node prose is plain text",
	} {
		if !strings.Contains(baseline, want) {
			t.Errorf("baseline link guidance missing %q", want)
		}
	}
	for _, heading := range []string{"## Plan", "## Code", "## Review", "## Commit"} {
		if strings.Contains(baseline, heading) {
			t.Errorf("baseline leaked %s", heading)
		}
		if !strings.Contains(dev, heading) {
			t.Errorf("developer workflow missing %s", heading)
		}
	}
	for _, want := range []string{
		"recompute knowledge discovery",
		"`mcp__tapper__backlinks`",
		"`mcp__tapper__links`",
		"`mcp__tapper__grep`",
		"active or stale interfaces and verifications",
		"Each needs a surviving subject or consumer",
		"word `legacy`",
	} {
		if !strings.Contains(dev, want) {
			t.Errorf("developer review workflow missing %q", want)
		}
	}
	if strings.Contains(dev, "@foldwise/dev") {
		t.Error("developer review workflow must not hardcode a project-specific KEG")
	}
}

func TestStripLeadingH1(t *testing.T) {
	if got := string(stripLeadingH1([]byte("# Heading\n\nbody\n"))); got != "body\n" {
		t.Fatalf("got %q", got)
	}
}
